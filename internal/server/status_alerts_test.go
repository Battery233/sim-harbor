package server

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/protocol"
	"github.com/Battery233/sim/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStatusAlertOnlyFreshObservedSIMAndPlainBarkSummary(t *testing.T) {
	payloads := []map[string]any{}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		payloads = append(payloads, p)
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	s, _ := fixture(t, 1, func(c *config.Config) {
		c.BarkKey = "SYNTHETIC"
		c.BarkBaseURL = receiver.URL
		c.PublicURL = "https://example.invalid"
	})
	ctx := context.Background()
	now := time.Now()
	e := protocol.Event{Type: 402, SubType: 46, DeviceID: "SIM-TEST", Code: func() *protocol.Number { n := protocol.Number(0); return &n }()}
	raw := []byte(`{"type":402,"subType":46,"code":0,"devId":"SIM-TEST","slotInfo":{"slot1_sta":"NS","slot2_sta":"OK"}}`)
	if err := s.Store.Ingest(ctx, e, raw, false); err != nil {
		t.Fatal(err)
	}
	// No session or stale status must not create a SIM incident.
	s.observeStatusAlerts(ctx, now)
	s.observeStatusAlerts(ctx, now.Add(31*time.Minute))
	var jobs int
	s.Store.DB.QueryRow(`SELECT count(*) FROM status_alert_jobs WHERE key='sim1'`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("offline inferred sim failure")
	}
	s.mu.Lock()
	s.active = &session{hasStatus: true, lastSeen: now.Add(32 * time.Minute)}
	s.mu.Unlock()
	s.Store.DB.Exec(`UPDATE device SET status_received_at=?`, now.Add(32*time.Minute).Unix())
	s.observeStatusAlerts(ctx, now.Add(32*time.Minute))
	s.mu.Lock()
	s.active.lastSeen = now.Add(62 * time.Minute)
	s.mu.Unlock()
	s.Store.DB.Exec(`UPDATE device SET status_received_at=?`, now.Add(62*time.Minute).Unix())
	s.observeStatusAlerts(ctx, now.Add(62*time.Minute))
	s.Store.DB.QueryRow(`SELECT count(*) FROM status_alert_jobs WHERE key='sim1'`).Scan(&jobs)
	if jobs != 1 {
		t.Fatal("observed sim fault absent")
	}
	n := store.Notification{AlertID: 1, AlertKey: "sim1", AlertKind: "fault", AlertLabel: "Synthetic card"}
	ok, _ := s.pushNotification(ctx, n)
	if !ok || len(payloads) != 1 || payloads[0]["title"] != "Synthetic card SIM注册异常" || payloads[0]["group"] != "SIM 运行提醒" {
		t.Fatal("wrong alert payload")
	}
	// Remove synthetic session so fixture shutdown cannot treat it as a real socket.
	s.mu.Lock()
	s.active = nil
	s.mu.Unlock()
}
