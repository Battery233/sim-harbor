package server

import (
	"encoding/json"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBoardHistoryQueriesOnlyNeverImportNotifyOrWrite(t *testing.T) {
	s, l := fixture(t, 1)
	board := connect(t, l)
	board.SetDeadline(time.Now().Add(8 * time.Second))
	send(t, board, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	reader := protocol.NewReader(board)
	until := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		ok := s.active != nil
		s.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(until) {
			t.Fatal("not connected")
		}
		time.Sleep(time.Millisecond)
	}
	for _, kind := range []string{"sms", "calls"} {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- action(s, "/board/history", map[string]any{"kind": kind, "slot": 2, "offset": 1}) }()
		cmd := read(t, reader, s.codec())
		want, sub, key := "querysms", 30, "p4"
		if kind == "calls" {
			want, sub, key = "querytel", 31, "p5"
		}
		if cmd["cmd"] != want || cmd["p1"] != float64(1) || cmd["p2"] != float64(10) || cmd[key] != float64(2) {
			t.Fatal("wrong read params", cmd)
		}
		rows := []map[string]any{}
		for i := 0; i < 11; i++ {
			rows = append(rows, map[string]any{"slot": 2, "dir": 0, "phNum": "SYNTHETIC-CALLER", "smsBd": "SYNTHETIC-BOARD-ONLY", "smsTs": 1700000000, "telStartTs": 1700000000, "telEndTs": 1700000020, "conn": 0})
		}
		send(t, board, s.codec(), map[string]any{"type": 402, "subType": sub, "code": 0, "tid": cmd["tid"], "results": rows})
		w := <-done
		var result struct {
			Items []BoardHistoryItem
			More  bool `json:"has_more"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		if w.Code != 200 || len(result.Items) != 10 || !result.More {
			t.Fatal("pagination", w.Code)
		}
	}
	for _, table := range []string{"messages", "calls", "notifications", "call_notifications", "outgoing"} {
		var n int
		if err := s.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("history modified business", table, n, err)
		}
	}
	events, _ := s.Logs.Snapshot(diagnostics.Filter{}, 100)
	serialized, _ := json.Marshal(events)
	if strings.Contains(string(serialized), "SYNTHETIC-BOARD-ONLY") || strings.Contains(string(serialized), "SYNTHETIC-CALLER") {
		t.Fatal("history leaked log")
	}
}
func TestBoardHistoryRejectsMixedScopeMalformedAndUnsafeActions(t *testing.T) {
	s, _ := fixture(t, 1)
	for _, input := range []map[string]any{{"kind": "delete", "slot": 1, "offset": 1}, {"kind": "sms", "slot": 0, "offset": 1}, {"kind": "calls", "slot": 1, "offset": 0}, {"kind": "sms", "slot": 1, "offset": 1, "cmd": "restart"}} {
		if w := action(s, "/board/history", input); w.Code != 400 {
			t.Fatal("unsafe input accepted", w.Code)
		}
	}
	for _, raw := range []string{`null`, `{}`, `[{"slot":1,"dir":0}]`, `[{"slot":2,"dir":5}]`, `[{"slot":2,"dir":0,"conn":2}]`} {
		if _, ok := parseBoardHistory(json.RawMessage(raw), "calls", 2); ok {
			t.Fatal("malformed accepted")
		}
	}
	if items, ok := parseBoardHistory(json.RawMessage(`[]`), "sms", 1); !ok || len(items) != 0 {
		t.Fatal("empty rejected")
	}
}
