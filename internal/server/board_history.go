package server

import (
	"encoding/json"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"net/http"
)

type BoardHistoryItem struct {
	Slot      int    `json:"slot"`
	Direction int    `json:"direction"`
	Phone     string `json:"phone"`
	Body      string `json:"body,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
	StartedAt int64  `json:"started_at,omitempty"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	Connected bool   `json:"connected"`
}

func parseBoardHistory(raw json.RawMessage, kind string, slot int) ([]BoardHistoryItem, bool) {
	var records []struct {
		Slot      protocol.Number  `json:"slot"`
		Dir       *protocol.Number `json:"dir"`
		Phone     string           `json:"phNum"`
		Body      string           `json:"smsBd"`
		Timestamp protocol.Number  `json:"smsTs"`
		Start     protocol.Number  `json:"telStartTs"`
		End       protocol.Number  `json:"telEndTs"`
		Conn      *protocol.Number `json:"conn"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &records) != nil || len(records) > 11 {
		return nil, false
	}
	out := []BoardHistoryItem{}
	for _, r := range records {
		if int(r.Slot) != slot || r.Dir == nil || (*r.Dir != 0 && *r.Dir != 1) || len(r.Phone) > 128 || len(r.Body) > 32768 {
			return nil, false
		}
		item := BoardHistoryItem{Slot: slot, Direction: int(*r.Dir), Phone: r.Phone}
		validTime := func(t protocol.Number) bool { return t >= 0 && t <= 9999999999 }
		if kind == "sms" {
			if !validTime(r.Timestamp) {
				return nil, false
			}
			item.Timestamp = int64(r.Timestamp)
			item.Body = r.Body
		} else {
			if r.Conn == nil || (*r.Conn != 0 && *r.Conn != 1) || !validTime(r.Start) || !validTime(r.End) {
				return nil, false
			}
			item.StartedAt = int64(r.Start)
			item.EndedAt = int64(r.End)
			item.Connected = *r.Conn == 1
		}
		out = append(out, item)
	}
	return out, true
}
func (s *Server) boardHistoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/board/history", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kind   string `json:"kind"`
			Slot   int    `json:"slot"`
			Offset int    `json:"offset"`
		}
		if !decodeAction(w, r, &in) {
			return
		}
		if (in.Kind != "sms" && in.Kind != "calls") || (in.Slot != 1 && in.Slot != 2) || in.Offset < 1 || in.Offset > 1000 {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
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
		cmd, sub, param := "querysms", 30, "p4"
		if in.Kind == "calls" {
			cmd, sub, param = "querytel", 31, "p5"
		}
		result, problem := s.boardCommand(r.Context(), sess, cmd, sub, nil, map[string]int{"p1": in.Offset, "p2": 10, param: in.Slot})
		if problem != "" {
			reply(w, 502, map[string]string{"error": problem})
			return
		}
		items, ok := parseBoardHistory(result.event.Results, in.Kind, in.Slot)
		if !ok {
			reply(w, 502, map[string]string{"error": "board_reply_invalid"})
			return
		}
		more := len(items) > 10
		if more {
			items = items[:10]
		}
		reply(w, 200, map[string]any{"kind": in.Kind, "slot": in.Slot, "offset": in.Offset, "items": items, "has_more": more})
	})
}
