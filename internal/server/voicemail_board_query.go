package server

import (
	"context"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

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
