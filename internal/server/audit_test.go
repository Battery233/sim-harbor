package server

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestCardIdentityUsesCorrelatedReplyNotSharedCache(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	s := New(config.Config{}, db)
	app, board := net.Pipe()
	defer app.Close()
	defer board.Close()
	sess := &session{conn: app}
	s.active = sess
	done := make(chan string, 1)
	go func() {
		_, icc, _, problem := s.queryCard(context.Background(), 1)
		done <- icc + ":" + problem
	}()
	cmd := read(t, protocol.NewReader(board), s.codec())
	p, _ := json.Marshal(map[string]any{"type": 402, "subType": 46, "code": 0, "tid": cmd["tid"], "slotInfo": map[string]any{"slot1_sta": "OK", "sim1_iccId": "SYNTHETIC-CORRELATED"}})
	e, err := protocol.Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	// A newer unrelated status exists in the shared cache before consumption.
	raw := []byte(`{"devId":"SIM-TEST","type":402,"subType":46,"code":0,"slotInfo":{"slot1_sta":"OK","sim1_iccId":"SYNTHETIC-UNRELATED"}}`)
	cached, err := protocol.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Ingest(context.Background(), cached, raw, false); err != nil {
		t.Fatal(err)
	}
	sess.mu.Lock()
	sess.response <- queryReply{e, time.Now()}
	sess.mu.Unlock()
	select {
	case got := <-done:
		if got != "SYNTHETIC-CORRELATED:" {
			t.Fatal("used unrelated identity", got)
		}
	case <-time.After(time.Second):
		t.Fatal("query stalled")
	}
}

func TestReconnectDoesNotRevalidatePreviousCardStatus(t *testing.T) {
	s, l := fixture(t, 0)
	a := connect(t, l)
	send(t, a, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 100})
	read(t, protocol.NewReader(a), s.codec())
	query := make(chan struct{})
	go func() { request(s, "POST", "http://example.com/api/state/query", "", ""); close(query) }()
	cmd := read(t, protocol.NewReader(a), s.codec())
	send(t, a, s.codec(), map[string]any{"type": 402, "subType": 46, "code": 0, "tid": cmd["tid"], "slotInfo": map[string]any{"slot1_sta": "OK"}})
	<-query
	check := func(want bool) {
		t.Helper()
		w := request(s, "GET", "http://example.com/api/state", "", "")
		var v map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v["status_current_session"] != want {
			t.Fatal("cached status escaped session boundary", v)
		}
	}
	check(true)
	b := connect(t, l)
	send(t, b, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 100})
	read(t, protocol.NewReader(b), s.codec())
	check(false)
}
