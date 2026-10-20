package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

type voicemailSlotState struct {
	Slot int     `json:"slot"`
	Mode *int    `json:"mode"`
	TTS  *string `json:"tts"`
}

type voicemailBoardState struct {
	AMRURL  string               `json:"amr_url"`
	Channel string               `json:"channel"`
	Slots   []voicemailSlotState `json:"slots"`
}

type voicemailApplyAuth struct {
	Phone          string
	Phones         map[int]string
	Password       string
	CostConfirmed  bool
	StopAutoAnswer bool
}

func (s *Server) handleVoicemailBoard(w http.ResponseWriter, r *http.Request, action, intent string, confirmed bool, seconds *int, expectedRaw json.RawMessage, auth voicemailApplyAuth) {
	if seconds != nil {
		reply(w, 400, map[string]string{"error": "invalid_board_action"})
		return
	}
	if action == "voicemail_query" {
		if confirmed || intent != "" || len(expectedRaw) > 0 && string(expectedRaw) != "null" || auth.Phone != "" || auth.Password != "" || auth.CostConfirmed || auth.StopAutoAnswer || len(auth.Phones) > 0 {
			reply(w, 400, map[string]string{"error": "invalid_board_action"})
			return
		}
	} else {
		if !confirmed || (intent != "enable" && intent != "restore") || len(expectedRaw) == 0 {
			reply(w, 400, map[string]string{"error": "invalid_board_action"})
			return
		}
		if intent == "enable" && !s.Config.VoicemailEnabled {
			reply(w, 409, map[string]string{"error": "voicemail_disabled"})
			return
		}
	}
	if !s.sendGate.TryLock() {
		reply(w, 409, map[string]string{"error": "send_in_progress"})
		return
	}
	defer s.sendGate.Unlock()
	if !s.query.TryLock() {
		reply(w, 409, map[string]string{"error": "query_in_progress"})
		return
	}
	defer s.query.Unlock()
	s.mu.Lock()
	sess := s.active
	s.mu.Unlock()
	if sess == nil {
		reply(w, 409, map[string]string{"error": "device_offline"})
		return
	}
	current, problem := s.readVoicemailBoard(r.Context(), sess)
	if problem != "" {
		reply(w, 502, map[string]string{"error": problem})
		return
	}
	if action == "voicemail_query" {
		reply(w, 200, map[string]any{"voicemail": current, "running": s.voicemailRunning(current)})
		return
	}
	var expected voicemailBoardState
	// SMS reads leave mode and TTS nil. That means TCP did not return them, not that a restore snapshot of 0 and "" has drifted.
	if json.Unmarshal(expectedRaw, &expected) != nil || !voicemailObservationAgrees(current, expected) {
		reply(w, 409, map[string]string{"error": "board_parameter_changed"})
		return
	}
	target, ok := s.voicemailTarget(intent, current)
	if !ok {
		reply(w, 400, map[string]string{"error": "invalid_board_action"})
		return
	}
	unchanged := sameVoicemailState(current, target)
	var written voicemailWriteResult
	if !unchanged {
		if needsVoicemailSMS(current, target) {
			if problem = voicemailSMSReady(current, target, auth); problem != "" {
				reply(w, 409, map[string]string{"error": problem})
				return
			}
			if !auth.CostConfirmed {
				reply(w, 409, map[string]string{"error": "sms_cost_required"})
				return
			}
			if intent == "enable" {
				if problem = s.voicemailEnableAllowed(r.Context()); problem != "" {
					reply(w, boardProblemStatus(problem), map[string]string{"error": problem})
					return
				}
			}
			if intent == "restore" && !auth.StopAutoAnswer {
				reply(w, 409, map[string]string{"error": "stop_auto_answer_required"})
				return
			}
		}
		written, problem = s.writeVoicemailBoard(r.Context(), sess, intent, auth, current, target)
		if problem != "" {
			reply(w, boardProblemStatus(problem), map[string]string{"error": problem})
			return
		}
	}
	observed, urlVerified, problem := s.readBackVoicemailBoard(r.Context(), sess, target)
	if problem != "" {
		if written.Steps == 0 {
			reply(w, 502, map[string]string{"error": "board_setting_unknown"})
			return
		}
		observed = voicemailBoardState{Channel: target.Channel, Slots: []voicemailSlotState{{Slot: 1}, {Slot: 2}}}
		urlVerified = false
	}
	if written.Steps > 0 {
		for i, slot := range observed.Slots {
			if mode, ok := written.Modes[slot.Slot]; ok {
				value := mode
				observed.Slots[i].Mode = &value
			}
			if written.TTSClear[slot.Slot] {
				value := ""
				observed.Slots[i].TTS = &value
			}
		}
	}
	submission := ""
	verified := false
	if written.Steps == 0 {
		verified = unchanged || sameVoicemailState(observed, target)
		urlVerified = observed.AMRURL == target.AMRURL
		if !verified {
			reply(w, 502, map[string]string{"error": "board_setting_unknown"})
			return
		}
	} else {
		switch {
		case written.Confirmed == written.Steps:
			submission = "verified"
		case written.Accepted > 0 || written.Confirmed > 0:
			submission = "accepted"
		default:
			submission = "submitted"
		}
		verified = submission == "verified" && urlVerified && voicemailEvidenceCovers(current, target, written)
	}
	if verified {
		s.Logs.Record("board_parameter_verified", diagnostics.Fields{Session: sess.diagnosticID})
	}
	reply(w, 200, map[string]any{
		"voicemail":     observed,
		"verified":      verified,
		"url_verified":  urlVerified,
		"submission":    submission,
		"unchanged":     unchanged,
		"sms_submitted": written.Sent > 0 && written.Confirmed < written.Steps,
	})
}

func (s *Server) voicemailRunning(current voicemailBoardState) map[string]any {
	slots := []int{}
	slots = append(slots, s.Config.VoicemailSlots...)
	return map[string]any{
		"enabled":        s.Config.VoicemailEnabled,
		"slots":          slots,
		"environment":    s.Config.Environment,
		"url_configured": s.Config.VoicemailAMRURL != "",
		"url_matches":    s.Config.VoicemailAMRURL != "" && current.AMRURL == s.Config.VoicemailAMRURL,
	}
}

func (s *Server) voicemailEnableAllowed(ctx context.Context) string {
	blocked, err := s.Store.RestoreBlocked(ctx)
	if err != nil {
		return "database_unavailable"
	}
	if blocked {
		return "outbound_restore_blocked"
	}
	if !s.Config.OutboundEnabled {
		return "outbound_disabled"
	}
	return ""
}

func boardProblemStatus(problem string) int {
	switch problem {
	case "outbound_restore_blocked", "outbound_disabled", "send_interval", "daily_limit", "card_changed", "card_identity_required", "card_not_ready", "card_number_mismatch", "control_already_dispatched", "sms_cost_required", "stop_auto_answer_required", "voicemail_sms_required", "invalid_send_limits":
		return 409
	case "database_unavailable":
		return 503
	default:
		return 502
	}
}

func (s *Server) voicemailTarget(intent string, current voicemailBoardState) (voicemailBoardState, bool) {
	target := current
	target.Slots = append([]voicemailSlotState(nil), current.Slots...)
	touch := map[int]bool{}
	if len(s.Config.VoicemailSlots) > 0 {
		for _, slot := range s.Config.VoicemailSlots {
			touch[slot] = true
		}
	} else if intent == "restore" && current.Channel != "sms" {
		for _, slot := range current.Slots {
			if slot.Mode == nil || *slot.Mode != 0 || slot.TTS == nil || *slot.TTS != "" {
				touch[slot.Slot] = true
			}
		}
	} else {
		return voicemailBoardState{}, false
	}
	switch intent {
	case "enable":
		if !s.Config.VoicemailEnabled || s.Config.VoicemailAMRURL == "" || s.Config.VoicemailTTS == "" {
			return voicemailBoardState{}, false
		}
		target.AMRURL = s.Config.VoicemailAMRURL
		for i, slot := range target.Slots {
			if touch[slot.Slot] {
				mode := 2
				target.Slots[i].Mode = &mode
			}
		}
	case "restore":
		target.AMRURL = ""
		for i, slot := range target.Slots {
			if touch[slot.Slot] {
				mode, text := 0, ""
				target.Slots[i].Mode = &mode
				target.Slots[i].TTS = &text
			}
		}
	default:
		return voicemailBoardState{}, false
	}
	return target, true
}

type voicemailWriteResult struct {
	Steps     int
	Sent      int
	Skipped   int
	Confirmed int
	Accepted  int
	Modes     map[int]int
	TTSClear  map[int]bool
	TTSSet    map[int]bool
}

func (s *Server) writeVoicemailBoard(ctx context.Context, sess *session, intent string, auth voicemailApplyAuth, current, target voicemailBoardState) (voicemailWriteResult, string) {
	type tcpStep struct {
		cmd    string
		sub    int
		params map[string]string
	}
	type smsStep struct {
		slot  int
		phone string
		body  string
		kind  string
		mode  int
		clear bool
		ok    func(string) bool
	}
	var tcp []tcpStep
	var messages []smsStep
	result := voicemailWriteResult{Modes: map[int]int{}, TTSClear: map[int]bool{}, TTSSet: map[int]bool{}}
	modeWrites := func() {
		for _, slot := range target.Slots {
			cur := slotByNumber(current, slot.Slot)
			if sameOptionalInt(cur.Mode, slot.Mode) {
				continue
			}
			if current.Channel == "sms" {
				phone, ok := voicemailSlotPhone(slot.Slot, auth, smsSlots(current, target))
				body, bodyOK := voicemailSMSBody(slot.Slot, auth.Password, "mode", *slot.Mode, "")
				if !ok || !bodyOK {
					messages = append(messages, smsStep{})
					return
				}
				mode := *slot.Mode
				n := slot.Slot
				messages = append(messages, smsStep{slot: n, phone: phone, body: body, kind: "mode", mode: mode, ok: func(text string) bool { return smsModeOK(text, n, mode) }})
				continue
			}
			messages = nil
			tcp = append(tcp, tcpStep{"slotcim", -1, map[string]string{"p1": strconv.Itoa(slot.Slot), "p2": strconv.Itoa(*slot.Mode)}})
		}
	}
	textWrites := func() {
		for _, slot := range target.Slots {
			cur := slotByNumber(current, slot.Slot)
			if sameOptionalString(cur.TTS, slot.TTS) {
				continue
			}
			if current.Channel == "sms" {
				phone, ok := voicemailSlotPhone(slot.Slot, auth, smsSlots(current, target))
				body, bodyOK := voicemailSMSBody(slot.Slot, auth.Password, "tts", 0, *slot.TTS)
				if !ok || !bodyOK {
					messages = append(messages, smsStep{})
					return
				}
				clear := *slot.TTS == ""
				n := slot.Slot
				messages = append(messages, smsStep{slot: n, phone: phone, body: body, kind: "tts", clear: clear, ok: func(text string) bool { return smsTTSOK(text, n, clear) }})
				continue
			}
			tcp = append(tcp, tcpStep{"slottts", -1, map[string]string{"p1": strconv.Itoa(slot.Slot), "p2": *slot.TTS}})
		}
	}
	urlWrite := func() {
		if current.AMRURL == target.AMRURL {
			return
		}
		value := target.AMRURL
		if value == "" {
			value = "clear"
		}
		tcp = append(tcp, tcpStep{"setamrurl", 55, map[string]string{"p1": value}})
	}
	if intent == "restore" {
		modeWrites()
		textWrites()
		urlWrite()
	} else {
		urlWrite()
		textWrites()
		modeWrites()
	}
	for _, item := range messages {
		if item.body == "" || item.ok == nil {
			return result, "voicemail_sms_required"
		}
	}
	runTCP := func(items []tcpStep) string {
		for _, item := range items {
			if _, problem := s.boardTextCommand(ctx, sess, item.cmd, item.sub, item.params); problem != "" {
				return problem
			}
		}
		return ""
	}
	runSMS := func() string {
		blocked := false
		for i, item := range messages {
			info, problem := s.boardSlotInfo(ctx, sess)
			if problem != "" {
				return problem
			}
			iccid, problem := slotMSISDN(info, item.slot, item.phone)
			if problem != "" {
				return problem
			}
			confirmed, accepted, problem := s.boardSMSCommand(ctx, sess, item.slot, item.phone, iccid, item.kind, item.body, item.mode, item.clear, i > 0, item.ok)
			if problem == "control_already_dispatched" {
				// This step was already recorded for the open attempt. Keep the
				// remaining steps, including a TCP upload-URL clear.
				result.Steps++
				result.Skipped++
				blocked = true
				continue
			}
			result.Steps++
			result.Sent++
			if confirmed {
				result.Confirmed++
				result.Accepted++
				if item.kind == "mode" {
					result.Modes[item.slot] = item.mode
				} else if item.clear {
					result.TTSClear[item.slot] = true
				} else {
					result.TTSSet[item.slot] = true
				}
			} else if accepted {
				result.Accepted++
			}
			if problem != "" {
				return problem
			}
		}
		if blocked && result.Sent == 0 {
			return "control_already_dispatched"
		}
		return ""
	}
	if intent == "restore" {
		problem := runSMS()
		if problem != "" && problem != "control_already_dispatched" {
			return result, problem
		}
		if len(tcp) > 0 {
			if tcpProblem := runTCP(tcp); tcpProblem != "" {
				return result, tcpProblem
			}
			return result, ""
		}
		return result, problem
	}
	if problem := runTCP(tcp); problem != "" {
		return result, problem
	}
	if problem := runSMS(); problem != "" {
		return result, problem
	}
	return result, ""
}

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

func voicemailEvidenceCovers(current, target voicemailBoardState, written voicemailWriteResult) bool {
	for _, slot := range []int{1, 2} {
		left, right := slotByNumber(current, slot), slotByNumber(target, slot)
		if !sameOptionalInt(left.Mode, right.Mode) {
			mode, ok := written.Modes[slot]
			if !ok || right.Mode == nil || mode != *right.Mode {
				return false
			}
		}
		if !sameOptionalString(left.TTS, right.TTS) {
			if right.TTS == nil {
				return false
			}
			if *right.TTS == "" {
				if !written.TTSClear[slot] {
					return false
				}
			} else if !written.TTSSet[slot] {
				return false
			}
		}
	}
	return true
}

func (s *Server) readVoicemailBoard(ctx context.Context, sess *session) (voicemailBoardState, string) {
	url, problem := s.boardTextCommand(ctx, sess, "askamrurl", 56, nil)
	if problem != "" {
		return voicemailBoardState{}, problem
	}
	parsedURL, ok := parseAMRURL(url.event.Val)
	if !ok {
		return voicemailBoardState{}, "board_reply_invalid"
	}
	state := voicemailBoardState{AMRURL: parsedURL, Channel: "tcp", Slots: []voicemailSlotState{{Slot: 1}, {Slot: 2}}}
	modes, problem := s.boardTextCommand(ctx, sess, "slotcim", -1, nil)
	if problem == "device_query_failed" {
		state.Channel = "sms"
		s.Logs.Record("voicemail_sms_channel", diagnostics.Fields{Session: sess.diagnosticID})
		return state, ""
	}
	if problem != "" {
		return voicemailBoardState{}, problem
	}
	parsed, ok := parseSlotModes(modes.event.Val)
	if !ok {
		return voicemailBoardState{}, "board_reply_invalid"
	}
	for i, slot := range state.Slots {
		mode := parsed[slot.Slot]
		state.Slots[i].Mode = &mode
		reply, problem := s.boardTextCommand(ctx, sess, "slottts", -1, map[string]string{"p1": strconv.Itoa(slot.Slot)})
		if problem != "" {
			return voicemailBoardState{}, problem
		}
		text, ok := parseSlotTTS(reply.event.Val)
		if !ok {
			return voicemailBoardState{}, "board_reply_invalid"
		}
		state.Slots[i].TTS = &text
	}
	return state, ""
}

func (s *Server) readBackVoicemailBoard(ctx context.Context, sess *session, target voicemailBoardState) (voicemailBoardState, bool, string) {
	if target.Channel != "sms" {
		state, problem := s.readVoicemailBoard(ctx, sess)
		return state, problem == "" && state.AMRURL == target.AMRURL, problem
	}
	url, problem := s.boardTextCommand(ctx, sess, "askamrurl", 56, nil)
	if problem != "" {
		return voicemailBoardState{}, false, problem
	}
	parsedURL, ok := parseAMRURL(url.event.Val)
	if !ok {
		return voicemailBoardState{}, false, "board_reply_invalid"
	}
	// Mode and TTS stay unknown until a control reply states them. The target is not a readback.
	return voicemailBoardState{AMRURL: parsedURL, Channel: "sms", Slots: []voicemailSlotState{{Slot: 1}, {Slot: 2}}}, parsedURL == target.AMRURL, ""
}

func (s *Server) boardTextCommand(ctx context.Context, sess *session, cmd string, sub int, params map[string]string) (queryReply, string) {
	s.mu.Lock()
	same := s.active == sess
	s.mu.Unlock()
	if !same {
		return queryReply{}, "device_connection_changed"
	}
	id := tid()
	ch := make(chan queryReply, 1)
	sess.mu.Lock()
	sess.tid = id
	sess.response = ch
	sess.mu.Unlock()
	defer func() { sess.mu.Lock(); sess.tid = ""; sess.response = nil; sess.mu.Unlock() }()
	body := map[string]any{"cmd": cmd, "tid": id}
	for key, value := range params {
		body[key] = value
	}
	loggedSub := sub
	if loggedSub < 0 {
		loggedSub = 0
	}
	s.Logs.Record("board_parameter_command", diagnostics.Fields{Session: sess.diagnosticID, Transaction: diagnostics.Reference(id), SubType: loggedSub})
	if err := s.write(sess, body); err != nil {
		return queryReply{}, "device_write_failed"
	}
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	select {
	case result := <-ch:
		s.mu.Lock()
		same = s.active == sess
		s.mu.Unlock()
		if !same {
			return queryReply{}, "device_connection_changed"
		}
		e := result.event
		if e.Type != 402 || e.Code == nil || *e.Code != 0 || (sub >= 0 && int(e.SubType) != sub) {
			return queryReply{}, "device_query_failed"
		}
		return result, ""
	case <-timer.C:
		return queryReply{}, "device_result_unknown"
	case <-ctx.Done():
		return queryReply{}, "device_result_unknown"
	}
}

func (s *Server) boardSlotInfo(ctx context.Context, sess *session) (map[string]any, string) {
	reply, problem := s.boardTextCommand(ctx, sess, "stat", 46, nil)
	if problem != "" {
		return nil, problem
	}
	if reply.event.SlotInfo == nil {
		return nil, "board_reply_invalid"
	}
	return reply.event.SlotInfo, ""
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

func parseSlotModes(value string) (map[int]int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ";")
	if len(parts) != 2 {
		return nil, false
	}
	labeled := strings.Contains(value, ":")
	out := map[int]int{}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		slot, modeText := i+1, part
		if labeled {
			left, right, ok := strings.Cut(part, ":")
			n, err := strconv.Atoi(strings.TrimSpace(left))
			if !ok || err != nil {
				return nil, false
			}
			slot, modeText = n, strings.TrimSpace(right)
		}
		mode, err := strconv.Atoi(modeText)
		if err != nil || slot < 1 || slot > 2 || mode < 0 || mode > 3 {
			return nil, false
		}
		if _, exists := out[slot]; exists {
			return nil, false
		}
		out[slot] = mode
	}
	if len(out) != 2 {
		return nil, false
	}
	return out, true
}

func parseSlotTTS(value string) (string, bool) {
	if strings.ContainsAny(value, "\x00\r\n") || utf8.RuneCountInString(value) > 274 {
		return "", false
	}
	return value, true
}

func parseAMRURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\x00\r\n ") || len(value) > 1500 {
		return "", false
	}
	return value, true
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

func voicemailObservationAgrees(current, expected voicemailBoardState) bool {
	if current.AMRURL != expected.AMRURL || current.Channel != expected.Channel || len(current.Slots) != 2 || len(expected.Slots) != 2 {
		return false
	}
	for _, slot := range []int{1, 2} {
		left, right := slotByNumber(current, slot), slotByNumber(expected, slot)
		if left.Slot != slot || right.Slot != slot || !unknownIntAgrees(left.Mode, right.Mode) || !unknownStringAgrees(left.TTS, right.TTS) {
			return false
		}
	}
	return true
}

func unknownIntAgrees(a, b *int) bool {
	if a == nil || b == nil {
		return true
	}
	return *a == *b
}

func unknownStringAgrees(a, b *string) bool {
	if a == nil || b == nil {
		return true
	}
	return *a == *b
}

func sameVoicemailState(a, b voicemailBoardState) bool {
	if a.AMRURL != b.AMRURL || a.Channel != b.Channel || len(a.Slots) != 2 || len(b.Slots) != 2 {
		return false
	}
	for _, slot := range []int{1, 2} {
		left, right := slotByNumber(a, slot), slotByNumber(b, slot)
		if left.Slot != slot || right.Slot != slot || !sameOptionalInt(left.Mode, right.Mode) || !sameOptionalString(left.TTS, right.TTS) {
			return false
		}
	}
	return true
}

func slotByNumber(state voicemailBoardState, slot int) voicemailSlotState {
	for _, item := range state.Slots {
		if item.Slot == slot {
			return item
		}
	}
	return voicemailSlotState{}
}

func sameOptionalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

var boardPhone = regexp.MustCompile(`^\+?[0-9]{3,20}$`)

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
