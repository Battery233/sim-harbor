package server

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim-harbor/internal/config"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestTCPMultipartMissingPiecesAndBarkRecoveryOnlyOneJob(t *testing.T) {
	var attempts atomic.Int32
	received := make(chan map[string]any, 3)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		received <- p
		if attempts.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	s, l := fixture(t, 1, func(c *config.Config) {
		c.BarkKey = "SYNTHETIC"
		c.BarkBaseURL = receiver.URL
		c.PublicURL = "https://example.invalid"
	})
	board := connect(t, l)
	event := map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 1, "iccId": "SYNTHETIC-ICC", "scName": "Test card", "phNum": "TEST", "smsTs": 1700000000, "smsBd": "", "comp": false, "smsGroupId": "SYNTHETIC-GROUP", "smsMaxNo": 2, "smsBdFrag": []string{"2,PART-B"}}
	send(t, board, s.codec(), event)
	until := time.Now().Add(time.Second)
	for {
		rows, _ := s.Store.Messages(context.Background(), 0, "")
		if len(rows) == 1 {
			if rows[0].Complete {
				t.Fatal("missing piece complete")
			}
			break
		}
		if time.Now().After(until) {
			t.Fatal("partial absent")
		}
		time.Sleep(time.Millisecond)
	}
	var jobs int
	s.Store.DB.QueryRow(`SELECT count(*) FROM notifications`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("partial pushed")
	}
	event["smsBdFrag"] = []string{"1,PART-A"}
	// Different SMS timestamps and reverse part order must still produce one
	// persisted notification, including after the second timestamp is replayed.
	event["smsTs"] = 1700000006
	send(t, board, s.codec(), event)
	until = time.Now().Add(time.Second)
	for {
		rows, _ := s.Store.Messages(context.Background(), 0, "")
		if len(rows) == 1 && rows[0].Complete {
			if rows[0].Body != "PART-APART-B" {
				t.Fatal("incorrect assembly")
			}
			break
		}
		if time.Now().After(until) {
			t.Fatal("assembly absent")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case p := <-received:
		if p["body"] != "PART-APART-B" {
			t.Fatal("wrong payload")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first attempt absent")
	}
	until = time.Now().Add(time.Second)
	for {
		var n int
		s.Store.DB.QueryRow(`SELECT attempts FROM notifications`).Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("failure not persistent")
		}
		time.Sleep(time.Millisecond)
	}
	// Accelerate backoff only inside this synthetic fixture, preserving the same job.
	s.Store.DB.Exec(`UPDATE notifications SET next_at=0`)
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery absent")
	}
	event["msgTs"] = 1700000001
	send(t, board, s.codec(), event)
	until = time.Now().Add(time.Second)
	for {
		var state string
		s.Store.DB.QueryRow(`SELECT state FROM notifications`).Scan(&state)
		if state == "sent" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("acceptance absent")
		}
		time.Sleep(time.Millisecond)
	}
	s.Store.DB.QueryRow(`SELECT count(*) FROM notifications`).Scan(&jobs)
	if jobs != 1 {
		t.Fatal("retry created job")
	}
	select {
	case <-received:
		t.Fatal("duplicate push after accepted")
	case <-time.After(1100 * time.Millisecond):
	}
	var outgoing int
	s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&outgoing)
	if outgoing != 0 {
		t.Fatal("chargeable command")
	}
}
