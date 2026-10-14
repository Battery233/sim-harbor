package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestCardPushMutePreservesHistoryAndNeverBackfillsAfterSwap(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := sms()
	e.ICCID = "SYNTHETIC-A"
	ingest(t, s, e)
	cards, _ := s.Cards(ctx, nil, false)
	id := cards[0].ID
	start := callEvent(601, 1700001000)
	start.ICCID = e.ICCID
	recordCall(t, s, start, true)
	if err := s.SetCardNotifications(ctx, id, false, true); err != nil {
		t.Fatal(err)
	}
	e.SMSTS++
	e.Slot = 2
	ingest(t, s, e)
	// A muted incomplete message and start must not appear on enabling/finalization.
	no := false
	e.SMSTS++
	e.Group = "SYNTHETIC-GROUP"
	e.Complete = &no
	e.Max = 2
	e.Fragments = []string{"1,PART-A"}
	ingest(t, s, e)
	start.TelStartTS += 100
	recordCall(t, s, start, true)
	if err := s.SetCardNotifications(ctx, id, true, false); err != nil {
		t.Fatal(err)
	}
	e.Fragments = []string{"2,PART-B"}
	ingest(t, s, e)
	start.Type = 603
	start.TelEndTS = start.TelStartTS + 10
	recordCall(t, s, start, true)
	s.QueueCallTimeouts(ctx, 2000000000)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old muted job revived", err)
	}
	rows, _ := s.Messages(ctx, 0, "")
	calls, _ := s.Calls(ctx, "", "", 0, 2000000000)
	if len(rows) != 3 || len(calls) != 2 {
		t.Fatal("history lost")
	}
	e.Complete = nil
	e.Group = ""
	e.Fragments = nil
	e.SMSTS++
	ingest(t, s, e)
	n, err := s.NextNotification(ctx)
	if err != nil || n.CallID != 0 {
		t.Fatal("new enabled message unavailable", err)
	}
	s.DB.Close()
	s = open(t, path)
	cards, _ = s.Cards(ctx, map[string]any{"slot2_sta": "OK", "sim2_iccId": "SYNTHETIC-A"}, true)
	if len(cards) != 1 || cards[0].ID != id || cards[0].Slot != 2 || !cards[0].NotificationEnabled {
		t.Fatal("identity preference changed", cards)
	}
	if err := s.SetCardNotifications(ctx, "unknown", false, true); err == nil {
		t.Fatal("unknown assigned policy")
	}
}
