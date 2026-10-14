package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Battery233/sim/internal/protocol"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBoardHeartbeatCompareWriteVerifyAndRejectChangedValue(t *testing.T) {
	s, l := fixture(t, 1)
	board := connect(t, l)
	board.SetDeadline(time.Now().Add(8 * time.Second))
	send(t, board, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	reader := protocol.NewReader(board)
	until := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		active := s.active != nil
		s.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(until) {
			t.Fatal("not bound")
		}
		time.Sleep(time.Millisecond)
	}
	for _, body := range []map[string]any{{"action": "restart"}, {"action": "heartbeat_set", "seconds": 120, "expected": 110}, {"action": "heartbeat_query", "seconds": 10}} {
		if w := action(s, "/board/parameters", body); w.Code != 400 {
			t.Fatal("invalid allowed", w.Code)
		}
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- action(s, "/board/parameters", map[string]any{"action": "heartbeat_set", "seconds": 120, "expected": 110, "confirmed": true})
	}()
	respond := func(cmd map[string]any, value string) {
		t.Helper()
		send(t, board, s.codec(), map[string]any{"type": 402, "subType": 14, "code": 0, "tid": cmd["tid"], "val": value})
	}
	first := read(t, reader, s.codec())
	if first["cmd"] != "pingintvl" || first["p1"] != nil {
		t.Fatal("read before write absent")
	}
	respond(first, "110")
	write := read(t, reader, s.codec())
	if write["cmd"] != "pingintvl" || write["p1"] != float64(120) {
		t.Fatal("wrong write")
	}
	respond(write, "120")
	verify := read(t, reader, s.codec())
	if verify["p1"] != nil {
		t.Fatal("verify rewrote")
	}
	respond(verify, "120")
	w := <-done
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result["verified"] != true {
		t.Fatal(w.Code, w.Body)
	}
	go func() {
		done <- action(s, "/board/parameters", map[string]any{"action": "heartbeat_set", "seconds": 120, "expected": 110, "confirmed": true})
	}()
	query := read(t, reader, s.codec())
	respond(query, "120")
	if w = <-done; w.Code != 409 {
		t.Fatal("changed expected accepted", w.Code)
	}
	var out int
	s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&out)
	if out != 0 {
		t.Fatal("SMS created")
	}
}
func TestBoardStorageQueryParsingAndLostWriteNoAutomaticRetry(t *testing.T) {
	s, l := fixture(t, 1)
	board := connect(t, l)
	board.SetDeadline(time.Now().Add(8 * time.Second))
	send(t, board, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	reader := protocol.NewReader(board)
	until := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		active := s.active != nil
		s.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(until) {
			t.Fatal("not bound")
		}
		time.Sleep(time.Millisecond)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- action(s, "/board/parameters", map[string]any{"action": "storage_query"}) }()
	cmd := read(t, reader, s.codec())
	if cmd["cmd"] != "storesmstelen" || cmd["p1"] != nil {
		t.Fatal("storage mutated")
	}
	send(t, board, s.codec(), map[string]any{"type": 402, "subType": 34, "code": 0, "tid": cmd["tid"], "val": "1s:on,1t:on;2s:on,2t:on"})
	if w := <-done; w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, bad := range []string{"", "1s:on,1t:on", "1s:on,1t:on;2s:on,2t:maybe"} {
		if _, ok := parseStorage(bad); ok {
			t.Fatal("invalid storage accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "http://localhost/api/board/parameters", nil).WithContext(ctx)
	r.Body = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"action":"heartbeat_set","seconds":120,"expected":110,"confirmed":true}`)).Body
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost")
	r.Host = "localhost"
	w := httptest.NewRecorder()
	go func() { s.Handler().ServeHTTP(w, r); done <- w }()
	cmd = read(t, reader, s.codec())
	send(t, board, s.codec(), map[string]any{"type": 402, "subType": 14, "code": 0, "tid": cmd["tid"], "val": "110"})
	write := read(t, reader, s.codec())
	if write["p1"] != float64(120) {
		t.Fatal("set absent")
	}
	cancel()
	if result := <-done; result.Code != 502 {
		t.Fatal("lost set claimed success")
	}
}
