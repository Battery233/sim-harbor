package server

import (
	"context"
	"github.com/Battery233/sim/internal/diagnostics"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// This executor is private; callers supply only the fixed commands below.
// The API owns the query gate across read/compare/write/verify.
func (s *Server) boardCommand(ctx context.Context, sess *session, cmd string, sub int, value *int, extra ...map[string]int) (queryReply, string) {
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
	if value != nil {
		body["p1"] = *value
	}
	for _, params := range extra {
		for key, val := range params {
			body[key] = val
		}
	}
	s.Logs.Record("board_parameter_command", diagnostics.Fields{Session: sess.diagnosticID, Transaction: diagnostics.Reference(id), SubType: sub})
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
		if e.Type != 402 || int(e.SubType) != sub || e.Code == nil || *e.Code != 0 {
			return queryReply{}, "device_query_failed"
		}
		return result, ""
	case <-timer.C:
		return queryReply{}, "device_result_unknown"
	case <-ctx.Done():
		return queryReply{}, "device_result_unknown"
	}
}
func parseHeartbeat(reply queryReply) (int, bool) {
	n, e := strconv.Atoi(reply.event.Val)
	return n, e == nil && n >= 5 && n <= 600
}
func parseStorage(value string) (map[string]bool, bool) {
	expected := []string{"1s", "1t", "2s", "2t"}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	if len(parts) != 4 {
		return nil, false
	}
	out := map[string]bool{}
	for i, p := range parts {
		k, v, ok := strings.Cut(p, ":")
		if !ok || k != expected[i] || (v != "on" && v != "off") {
			return nil, false
		}
		out[k] = v == "on"
	}
	return out, true
}
func (s *Server) boardSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/board/parameters", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action    string `json:"action"`
			Seconds   *int   `json:"seconds"`
			Expected  *int   `json:"expected"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decodeAction(w, r, &in) {
			return
		}
		if in.Action != "heartbeat_query" && in.Action != "heartbeat_set" && in.Action != "storage_query" {
			reply(w, 400, map[string]string{"error": "invalid_board_action"})
			return
		}
		setting := in.Action == "heartbeat_set"
		if setting {
			if !in.Confirmed || in.Seconds == nil || in.Expected == nil || *in.Seconds < 5 || *in.Seconds > 600 || *in.Seconds >= s.Config.StaleSeconds-15 {
				reply(w, 400, map[string]string{"error": "invalid_board_action"})
				return
			}
		} else if in.Seconds != nil || in.Expected != nil || in.Confirmed {
			reply(w, 400, map[string]string{"error": "invalid_board_action"})
			return
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
		cmd, sub := "pingintvl", 14
		if in.Action == "storage_query" {
			cmd = "storesmstelen"
			sub = 34
		}
		result, problem := s.boardCommand(r.Context(), sess, cmd, sub, nil)
		if problem != "" {
			reply(w, 502, map[string]string{"error": problem})
			return
		}
		if in.Action == "storage_query" {
			values, ok := parseStorage(result.event.Val)
			if !ok {
				reply(w, 502, map[string]string{"error": "board_reply_invalid"})
				return
			}
			reply(w, 200, map[string]any{"storage": values})
			return
		}
		current, ok := parseHeartbeat(result)
		if !ok {
			reply(w, 502, map[string]string{"error": "board_reply_invalid"})
			return
		}
		if !setting {
			reply(w, 200, map[string]any{"seconds": current})
			return
		}
		if current != *in.Expected {
			reply(w, 409, map[string]string{"error": "board_parameter_changed"})
			return
		}
		if current == *in.Seconds {
			reply(w, 200, map[string]any{"seconds": current, "verified": true, "unchanged": true})
			return
		}
		if _, problem = s.boardCommand(r.Context(), sess, cmd, sub, in.Seconds); problem != "" {
			reply(w, 502, map[string]string{"error": "board_setting_unknown"})
			return
		}
		result, problem = s.boardCommand(r.Context(), sess, cmd, sub, nil)
		if problem != "" {
			reply(w, 502, map[string]string{"error": "board_setting_unknown"})
			return
		}
		verified, ok := parseHeartbeat(result)
		if !ok || verified != *in.Seconds {
			reply(w, 502, map[string]string{"error": "board_setting_unknown"})
			return
		}
		s.Logs.Record("board_parameter_verified", diagnostics.Fields{Session: sess.diagnosticID, SubType: sub})
		reply(w, 200, map[string]any{"seconds": verified, "verified": true})
	})
}
