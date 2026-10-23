package server

import (
	"context"
	"errors"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var boardPhone = regexp.MustCompile(`^\+?[0-9]{3,20}$`)

func smsSlots(current, target voicemailBoardState) []int {
	out := []int{}
	if current.Channel != "sms" {
		return out
	}
	for _, slot := range []int{1, 2} {
		left, right := slotByNumber(current, slot), slotByNumber(target, slot)
		if !sameOptionalInt(left.Mode, right.Mode) || !sameOptionalString(left.TTS, right.TTS) {
			out = append(out, slot)
		}
	}
	return out
}

func voicemailSlotPhone(slot int, auth voicemailApplyAuth, needed []int) (string, bool) {
	if auth.Phones != nil {
		if phone := strings.TrimSpace(auth.Phones[slot]); phone != "" {
			return phone, boardPhone.MatchString(phone)
		}
	}
	if len(needed) == 1 {
		phone := strings.TrimSpace(auth.Phone)
		return phone, boardPhone.MatchString(phone)
	}
	return "", false
}

func voicemailSMSReady(current, target voicemailBoardState, auth voicemailApplyAuth) string {
	if !validBoardPassword(auth.Password) {
		return "voicemail_sms_required"
	}
	needed := smsSlots(current, target)
	if len(needed) == 0 {
		return ""
	}
	for _, slot := range needed {
		if _, ok := voicemailSlotPhone(slot, auth, needed); !ok {
			return "voicemail_sms_required"
		}
	}
	return ""
}

func (s *Server) boardSMSCommand(ctx context.Context, sess *session, slot int, phone, iccid, kind, body string, mode int, clear bool, relaxInterval bool, accept func(string) bool) (bool, bool, string) {
	s.mu.Lock()
	same := s.active == sess
	s.mu.Unlock()
	if !same {
		return false, false, "device_connection_changed"
	}
	now := time.Now()
	claimID, err := s.Store.ClaimVoicemailControl(ctx, s.Config.DeviceID, slot, phone, iccid, kind, mode, clear, now.Unix(), store.DayLimits(now.In(s.Config.Location), int64(s.Config.SendIntervalSeconds), s.Config.SendDailyLimit), relaxInterval)
	if err != nil {
		var safety *store.SafetyError
		if errors.As(err, &safety) {
			return false, false, safety.Code
		}
		return false, false, "database_unavailable"
	}
	mark := func(state string) {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if markErr := s.Store.MarkVoicemailControl(persist, claimID, state); markErr != nil {
			s.Logs.Record("voicemail_sms_submitted", diagnostics.Fields{Session: sess.diagnosticID, Slot: slot, Outcome: "device_result_unknown"})
		}
	}
	id := tid()
	ch := make(chan queryReply, 4)
	watch := make(chan protocol.Event, 8)
	sentAt := now.Unix()
	sess.mu.Lock()
	sess.tid = id
	sess.response = ch
	sess.smsWatch = watch
	sess.smsWatchSlot = slot
	sess.smsWatchPhone = phone
	sess.smsWatchICCID = iccid
	sess.smsWatchSent = sentAt
	sess.acceptSMSReport = true
	sess.mu.Unlock()
	defer func() {
		sess.mu.Lock()
		if sess.tid == id {
			sess.tid = ""
			sess.response = nil
			sess.smsWatch = nil
			sess.smsWatchSlot = 0
			sess.smsWatchPhone = ""
			sess.smsWatchICCID = ""
			sess.smsWatchSent = 0
			sess.acceptSMSReport = false
		}
		sess.mu.Unlock()
	}()
	s.Logs.Record("voicemail_sms_command", diagnostics.Fields{Session: sess.diagnosticID, Transaction: diagnostics.Reference(id), Slot: slot, SubType: 16})
	if err = s.write(sess, map[string]any{"cmd": "sendsms", "p1": strconv.Itoa(slot), "p2": phone, "p3": body, "tid": id}); err != nil {
		return false, false, "device_write_failed"
	}
	// A missing type 501 is pending verification, not a confirmed parameter change.
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	sawAccept := false
	submitted := false
	for {
		select {
		case result := <-ch:
			s.mu.Lock()
			same = s.active == sess
			s.mu.Unlock()
			if !same {
				mark("unknown")
				return false, sawAccept, "device_connection_changed"
			}
			e := result.event
			if e.Code != nil && *e.Code != 0 {
				mark("unknown")
				return false, false, "device_query_failed"
			}
			switch e.Type {
			case 401:
				sawAccept = true
			case 402, 502, 503:
				sawAccept = true
				if !submitted {
					submitted = true
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(time.Second)
				}
			}
		case ev := <-watch:
			if !controlSMSRelevant(ev, slot, phone, iccid, sentAt) || voicemailCommandSMS(ev) {
				continue
			}
			compact := compactSMS(strings.Join(smsChunks(ev), ""))
			if strings.Contains(compact, "密码不正确") || strings.Contains(compact, "格式错误") {
				mark("rejected")
				return false, sawAccept, "voicemail_sms_rejected"
			}
			if accept(strings.Join(smsChunks(ev), "")) {
				mark("accepted")
				return true, true, ""
			}
		case <-timer.C:
			outcome := "device_result_unknown"
			state := "unknown"
			if sawAccept {
				outcome = "accepted"
				state = "accepted"
			}
			mark(state)
			s.Logs.Record("voicemail_sms_submitted", diagnostics.Fields{Session: sess.diagnosticID, Transaction: diagnostics.Reference(id), Slot: slot, Outcome: outcome})
			return false, sawAccept, ""
		case <-ctx.Done():
			mark("unknown")
			return false, sawAccept, "device_result_unknown"
		}
	}
}

func slotMSISDN(info map[string]any, slot int, phone string) (string, string) {
	prefix := strconv.Itoa(slot)
	status, _ := info["slot"+prefix+"_sta"].(string)
	iccid, _ := info["sim"+prefix+"_iccId"].(string)
	number, _ := info["sim"+prefix+"_msIsdn"].(string)
	if status != "OK" {
		return "", "card_not_ready"
	}
	if strings.TrimSpace(iccid) == "" {
		return "", "card_identity_required"
	}
	if !store.PhonesMatch(number, phone) {
		return "", "card_number_mismatch"
	}
	return strings.TrimSpace(iccid), ""
}

func needsVoicemailSMS(current, target voicemailBoardState) bool {
	if current.Channel != "sms" {
		return false
	}
	for _, slot := range []int{1, 2} {
		left, right := slotByNumber(current, slot), slotByNumber(target, slot)
		if !sameOptionalInt(left.Mode, right.Mode) || !sameOptionalString(left.TTS, right.TTS) {
			return true
		}
	}
	return false
}

func validBoardPassword(password string) bool {
	if len(password) < 4 || len(password) > 20 {
		return false
	}
	for _, r := range password {
		if r <= 32 || r > 126 || strings.ContainsRune("#@&~", r) {
			return false
		}
	}
	return true
}

func voicemailSMSBody(slot int, password, kind string, mode int, tts string) (string, bool) {
	if (slot != 1 && slot != 2) || !validBoardPassword(password) {
		return "", false
	}
	switch kind {
	case "mode":
		if mode != 0 && mode != 2 {
			return "", false
		}
		return "*#slot" + strconv.Itoa(slot) + "cim#" + password + "#" + strconv.Itoa(mode), true
	case "tts":
		if strings.ContainsAny(tts, "#@&~\x00\r\n") {
			return "", false
		}
		return "*#slot" + strconv.Itoa(slot) + "tts#" + password + "#" + tts, true
	default:
		return "", false
	}
}

func compactSMS(body string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, body)
}

func smsModeOK(body string, slot, mode int) bool {
	text := compactSMS(body)
	if !strings.Contains(text, "卡槽"+strconv.Itoa(slot)) || !strings.Contains(text, "设置为来电") {
		return false
	}
	switch mode {
	case 0:
		return strings.Contains(text, "不接听")
	case 2:
		if strings.Contains(text, "不接听") {
			return false
		}
		return strings.Contains(text, "接听并录音") || strings.Contains(text, "自动挂断") || strings.Contains(text, "自动接听")
	default:
		return false
	}
}

func smsTTSOK(body string, slot int, clear bool) bool {
	text := compactSMS(body)
	if !strings.Contains(text, "卡槽"+strconv.Itoa(slot)+"的自动语音") {
		return false
	}
	if clear {
		return strings.Contains(text, "已经取消")
	}
	return strings.Contains(text, "已经设置完成")
}

// controlSMSRelevant reports whether a 501 can be evidence for this control attempt.
// The body is not enough: the source number, the slot that actually received it,
// and a confirmable SIM identity must match, and the message must not predate the attempt.
func controlSMSRelevant(e protocol.Event, slot int, phone, iccid string, sentAt int64) bool {
	if e.Type != 501 || int(e.Slot) != slot || sentAt <= 0 || int64(e.SMSTS) < sentAt {
		return false
	}
	if !store.PhonesMatch(e.Phone, phone) {
		return false
	}
	identity := store.ConfirmableSIM(e.ICCID)
	return identity != "" && identity == iccid
}

func voicemailCommandSMS(e protocol.Event) bool {
	for _, chunk := range smsChunks(e) {
		folded := strings.ToLower(strings.TrimSpace(chunk))
		for _, prefix := range []string{"*#slot1cim#", "*#slot2cim#", "*#slot1tts#", "*#slot2tts#", "*#slottts#", "*#slotts#", "*#slotcim#"} {
			if strings.Contains(folded, prefix) {
				return true
			}
		}
	}
	return false
}

func (s *Server) controlSMSSuppressed(ctx context.Context, e protocol.Event) (bool, error) {
	if e.Type != 501 {
		return false, nil
	}
	if voicemailCommandSMS(e) {
		return true, nil
	}
	text := compactSMS(strings.Join(smsChunks(e), ""))
	if !voicemailReplyText(text) {
		return false, nil
	}
	rows, err := s.Store.RecentVoicemailControls(ctx, int(e.Slot), time.Now().Unix()-900)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if !controlSMSRelevant(e, int(e.Slot), row.Phone, row.ICCID, row.SentAt) {
			continue
		}
		if strings.Contains(text, "密码不正确") || strings.Contains(text, "格式错误") {
			if row.State == "claimed" || row.State == "accepted" || row.State == "unknown" {
				return true, nil
			}
			continue
		}
		if row.Kind == "mode" && smsModeOK(text, int(e.Slot), row.Mode) {
			return true, nil
		}
		if row.Kind == "tts" && smsTTSOK(text, int(e.Slot), row.ClearTTS) {
			return true, nil
		}
	}
	return false, nil
}

func voicemailReplyText(text string) bool {
	if strings.Contains(text, "密码不正确") || strings.Contains(text, "格式错误") {
		return true
	}
	if strings.Contains(text, "自动语音") && (strings.Contains(text, "已经设置完成") || strings.Contains(text, "已经取消")) {
		return true
	}
	return strings.Contains(text, "设置为来电")
}

func smsChunks(e protocol.Event) []string {
	chunks := []string{}
	if e.Body != "" {
		chunks = append(chunks, e.Body)
	}
	for _, part := range e.Fragments {
		_, text, ok := strings.Cut(part, ",")
		if ok {
			chunks = append(chunks, text)
		} else if part != "" {
			chunks = append(chunks, part)
		}
	}
	return chunks
}
