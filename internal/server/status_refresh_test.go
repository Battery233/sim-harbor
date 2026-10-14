package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestStatusRefreshReservesBoundedAttempts(t *testing.T) {
	s := New(config.Config{StaleSeconds: 345}, nil)
	now := time.Now()
	if s.reserveStatusRefresh(now) {
		t.Fatal("queried disconnected board")
	}
	s.active = &session{lastSeen: now}
	if !s.reserveStatusRefresh(now) || s.reserveStatusRefresh(now.Add(59*time.Second)) || !s.reserveStatusRefresh(now.Add(60*time.Second)) {
		t.Fatal("failed/busy attempt did not respect retry interval")
	}
	s.active.hasStatus = true
	s.active.statusAt = now.Add(60 * time.Second)
	if s.reserveStatusRefresh(now.Add(179*time.Second)) || !s.reserveStatusRefresh(now.Add(180*time.Second)) {
		t.Fatal("successful stat not respected or never renewed")
	}
	s.active.lastSeen = now.Add(180 * time.Second)
	if s.reserveStatusRefresh(now.Add(526 * time.Second)) {
		t.Fatal("queried board beyond online threshold")
	}
	if s.reserveStatusRefresh(now) {
		t.Fatal("future heartbeat treated as current")
	}
	s.Config.StaleSeconds = 30
	if s.statusRefreshInterval() != 15*time.Second {
		t.Fatal("shorter freshness threshold ignored")
	}
}

func TestAutomaticStatusRefreshAndChangeInvalidation(t *testing.T) {
	s, l := fixture(t, 1)
	a := connect(t, l)
	send(t, a, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 100})
	reader := protocol.NewReader(a)
	read(t, reader, s.codec())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunStatusRefresh(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	answer := func() {
		t.Helper()
		a.SetDeadline(time.Now().Add(3 * time.Second))
		cmd := read(t, reader, s.codec())
		if cmd["cmd"] != "stat" {
			t.Fatal("background operation was not read-only stat", cmd)
		}
		s.mu.Lock()
		previous := s.active.statusAt
		s.mu.Unlock()
		send(t, a, s.codec(), map[string]any{"type": 402, "subType": 46, "code": 0, "tid": cmd["tid"], "slotInfo": map[string]any{"slot1_sta": "OK", "sim1_iccId": "SYNTHETIC-CARD-A", "slot2_sta": "NOSIM"}})
		wait := time.Now().Add(time.Second)
		for time.Now().Before(wait) {
			s.mu.Lock()
			ready := s.active != nil && s.active.hasStatus && s.active.statusAt.After(previous)
			s.mu.Unlock()
			if ready {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("automatic status not persisted")
	}
	answer() // No browser or manual request was required.
	s.mu.Lock()
	first := s.active.statusAt
	s.mu.Unlock()
	send(t, a, s.codec(), map[string]any{"type": 998, "cnt": 1})
	heartbeatDeadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		seen, at, current := s.active.lastSeen, s.active.statusAt, s.active.hasStatus
		s.mu.Unlock()
		if seen.After(first) {
			if !at.Equal(first) || !current {
				t.Fatal("heartbeat changed snapshot freshness")
			}
			break
		}
		if time.Now().After(heartbeatDeadline) {
			t.Fatal("heartbeat was not observed")
		}
		time.Sleep(time.Millisecond)
	}
	// Advance only the snapshot/attempt clock, leaving heartbeats live.
	s.mu.Lock()
	s.active.statusAt = time.Now().Add(-121 * time.Second)
	s.active.autoAttempt = time.Now().Add(-61 * time.Second)
	s.mu.Unlock()
	answer()
	send(t, a, s.codec(), map[string]any{"type": 205, "slot": 1})
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		invalid := !s.active.hasStatus
		s.mu.Unlock()
		if invalid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("removal did not invalidate snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	w := request(s, "GET", "http://example.com/api/state", "", "")
	var state map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state["card_locations_verified"] != false || state["status_current_session"] != false || state["status_refresh_seconds"] != float64(120) {
		t.Fatal("invalidated status remained verified")
	}
	s.mu.Lock()
	s.active.autoAttempt = time.Now().Add(-61 * time.Second)
	s.mu.Unlock()
	answer()
	// A replacement connection must be queried even while the old status is young.
	a = connect(t, l)
	send(t, a, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 100})
	reader = protocol.NewReader(a)
	read(t, reader, s.codec())
	answer()
}

func TestAutomaticStatusRefreshCancelsInFlightQuery(t *testing.T) {
	s, l := fixture(t, 0)
	a := connect(t, l)
	send(t, a, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 998})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.RunStatusRefresh(ctx); close(done) }()
	cmd := read(t, protocol.NewReader(a), s.codec())
	if cmd["cmd"] != "stat" {
		t.Fatal("unexpected command")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for query timeout")
	}
}
