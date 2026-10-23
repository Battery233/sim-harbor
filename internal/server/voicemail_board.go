package server

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"net/http"
	"strconv"
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
		if intent == "enable" && !s.Config.VoicemailReceptionReady() {
			reply(w, 409, map[string]string{"error": "voicemail_allowlist_required"})
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
		"enabled":         s.Config.VoicemailEnabled,
		"slots":           slots,
		"environment":     s.Config.Environment,
		"upload_mode":     s.Config.VoicemailMode(),
		"receiving_ready": s.Config.VoicemailReceptionReady(),
		"url_configured":  s.Config.VoicemailAMRURL != "",
		"url_matches":     s.Config.VoicemailAMRURL != "" && current.AMRURL == s.Config.VoicemailAMRURL,
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
