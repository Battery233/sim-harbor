package store

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"path/filepath"
	"testing"
	"time"
)

func TestVoicemailSlotConfirmedMissedNotifiesOnceAndDoesNotBackfill(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	now := time.Now().Unix()
	ingest := func(e protocol.Event, notify bool) {
		t.Helper()
		raw, _ := json.Marshal(e)
		if err := s.Ingest(ctx, e, raw, notify, 1); err != nil {
			t.Fatal(err)
		}
	}
	e := callEvent(601, now)
	e.Phone = "anonymous"
	ingest(e, true)
	e.Type = 603
	e.TelEndTS = e.TelStartTS + 2
	ingest(e, true)
	ingest(e, true)
	if notificationCount(t, s, 0) != 1 {
		t.Fatal("missed retry duplicated notification")
	}
	c, err := s.Call(ctx, 1, now+3)
	if err != nil || c.State != "missed" || c.NotificationKind != "missed" {
		t.Fatal(c, err)
	}
	// A connected call waits for the file; its end alone creates no reminder.
	e = callEvent(602, now+10)
	connected := protocol.Number(now + 12)
	e.TelConnectedTS = &connected
	ingest(e, true)
	e.Type = 603
	e.TelEndTS = protocol.Number(now + 20)
	ingest(e, true)
	if notificationCount(t, s, 0) != 1 {
		t.Fatal("answered call notified without audio")
	}
	// Calls received with Bark disabled are not retroactively queued on replay.
	e = callEvent(603, now+30)
	e.TelEndTS = e.TelStartTS + 2
	ingest(e, false)
	ingest(e, true)
	if notificationCount(t, s, 0) != 1 {
		t.Fatal("replay backfilled old missed call")
	}
}

func TestVoicemailSlotLateConnectionCancelsPendingMissed(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	now := time.Now().Unix()
	e := callEvent(603, now)
	e.TelEndTS = e.TelStartTS + 2
	raw, _ := json.Marshal(e)
	if err := s.Ingest(ctx, e, raw, true, 1); err != nil {
		t.Fatal(err)
	}
	e.Type = 602
	e.TelEndTS = 0
	connected := protocol.Number(now + 1)
	e.TelConnectedTS = &connected
	raw, _ = json.Marshal(e)
	if err := s.Ingest(ctx, e, raw, true, 1); err != nil {
		t.Fatal(err)
	}
	c, err := s.Call(ctx, 1, now+3)
	if err != nil || c.State != "unknown" || c.NotificationState != "canceled" {
		t.Fatal("stale missed reminder retained", c, err)
	}
}
