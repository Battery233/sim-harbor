package server

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim/internal/protocol"
	"github.com/Battery233/sim/internal/store"
	"testing"
	"time"
)

func TestStateNotificationSuccessIsReadOnlyAndExcludesFixedTest(t *testing.T) {
	s, _ := fixture(t, 1)
	ctx := context.Background()
	now := time.Now()
	e := protocol.Event{Type: 501, Slot: 1, DeviceID: "SIM-TEST", Phone: "TEST", Body: "SYNTHETIC", SMSTS: 1700000000}
	raw, _ := json.Marshal(e)
	if err := s.Store.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	n, err := s.Store.NextNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.Store.NotificationResultCodeAt(ctx, n, true, "accepted", now.Unix())
	s.Store.ReserveNotificationTest(ctx, now.Unix())
	s.Store.CompleteNotificationTest(ctx, now.Unix(), "accepted")
	for i := 0; i < 2; i++ {
		w := request(s, "GET", "http://localhost/api/state", "", "")
		var v struct {
			Stats store.NotificationSuccessStats `json:"notification_success"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Stats.Today != 1 || v.Stats.Last30Days != 1 || v.Stats.Timezone != s.Config.Timezone {
			t.Fatal("state stats", w.Code, v)
		}
	}
	var outgoing, notifs int
	s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&outgoing)
	s.Store.DB.QueryRow(`SELECT count(*) FROM notifications`).Scan(&notifs)
	if outgoing != 0 || notifs != 1 {
		t.Fatal("stats created actions")
	}
}
