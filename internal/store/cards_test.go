package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim/internal/outbound"
)

func TestCardHistoryFollowsIdentityAcrossSlotsAndNames(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := sms()
	e.ICCID = "SYNTHETIC-A"
	e.CardName = "Shared name"
	ingest(t, s, e)
	first, _ := s.MessagePage(ctx, 0, "", 0, 10)
	id := first[0].CardID
	// A replay after moving slots must not create a second business message/notification.
	e.Slot = 2
	e.CardName = "Renamed"
	ingest(t, s, e)
	rows, _ := s.CardMessagePage(ctx, id, 0, "", 0, 10)
	if len(rows) != 1 || rows[0].Slot != 1 || rows[0].CardName != "Shared name" || count(t, s, "notifications") != 1 {
		t.Fatal("swap rewrote receipt or duplicated", rows)
	}
	e.SMSTS++
	ingest(t, s, e)
	for i := 0; i < 3; i++ {
		e.ICCID = fmt.Sprintf("SYNTHETIC-B%d", i)
		e.CardName = "Shared name"
		e.SMSTS++
		ingest(t, s, e)
	}
	e.ICCID = ""
	e.CardName = "Unverified label"
	e.SMSTS++
	ingest(t, s, e)
	rows, _ = s.CardMessagePage(ctx, id, 0, "", 0, 1)
	if len(rows) != 1 || rows[0].CardName != "Renamed" {
		t.Fatal("name snapshots lost", rows)
	}
	older, _ := s.CardMessagePage(ctx, id, 0, "", rows[0].ID, 10)
	if len(older) != 1 || older[0].CardID != id {
		t.Fatal("card pagination", older)
	}
	unknown, _ := s.CardMessagePage(ctx, "unknown", 0, "", 0, 10)
	if len(unknown) != 1 || unknown[0].CardID != "" {
		t.Fatal("unknown guessed", unknown)
	}
	info := map[string]any{"slot2_sta": "OK", "sim2_iccId": "SYNTHETIC-A"}
	cards, err := s.Cards(ctx, info, true)
	if err != nil || len(cards) != 4 || cards[0].ID != id || cards[0].Slot != 2 {
		t.Fatal("history capped at two or identity changed", cards, err)
	}
	oldGroup := map[string]string{}
	for _, c := range cards {
		var g string
		s.DB.QueryRow(`SELECT group_name FROM cards WHERE id=?`, c.ID).Scan(&g)
		oldGroup[c.ID] = g
	}
	if oldGroup[cards[0].ID] == oldGroup[cards[1].ID] {
		t.Fatal("same-name cards merged Bark group")
	}
	s.DB.Close()
	s = open(t, path)
	cards, _ = s.Cards(ctx, info, false)
	if len(cards) != 4 || cards[0].ID != id || cards[0].Slot != 0 {
		t.Fatal("restart trusts stale location", cards)
	}
	var group string
	s.DB.QueryRow(`SELECT group_name FROM cards WHERE id=?`, id).Scan(&group)
	if group != oldGroup[id] {
		t.Fatal("stable group changed")
	}
	exported, err := s.ExportMessages(ctx, MessageFilter{Card: id})
	if err != nil || len(exported) != 2 || exported[1].CardName != "Shared name" {
		t.Fatal("identity export differs", exported, err)
	}
}

func TestLegacyMigrationDoesNotGuessCardFromCurrentSlot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := sms()
	e.ICCID = "SYNTHETIC-OLD"
	e.CardName = "Old snapshot"
	ingest(t, s, e)
	if _, err := s.DB.Exec(`DROP TABLE call_notifications;DROP TABLE calls;DROP TABLE status_alert_jobs;DROP TABLE status_alert_monitors;ALTER TABLE cards DROP COLUMN notification_enabled;ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DROP INDEX notifications_success_time;ALTER TABLE notifications DROP COLUMN accepted_at;DROP INDEX outgoing_card_limits;DROP INDEX messages_card;ALTER TABLE messages DROP COLUMN card_id;ALTER TABLE messages DROP COLUMN device_id;DROP TABLE cards;DELETE FROM schema_version;INSERT INTO schema_version VALUES(6)`); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	rows, err := s.CardMessagePage(ctx, "unknown", 0, "", 0, 10)
	if err != nil || len(rows) != 1 || rows[0].CardName != "Old snapshot" {
		t.Fatal("migration guessed", rows, err)
	}
	cards, _ := s.Cards(ctx, nil, false)
	if len(cards) != 0 {
		t.Fatal("migration invented cards")
	}
	// Matching the old key on a genuine replay is evidence; preserve old name/slot.
	ingest(t, s, e)
	e.Slot = 2
	ingest(t, s, e)
	rows, _ = s.MessagePage(ctx, 0, "", 0, 10)
	if len(rows) != 1 || rows[0].CardID == "" || rows[0].CardName != "Old snapshot" || count(t, s, "notifications") != 1 {
		t.Fatal("verified replay lost old record", rows)
	}
}

func TestIdentitySendLimitsAndDuplicateContentSurviveSwap(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	now := int64(1700000000)
	prepare := func(n, slot int, card, body string) Outgoing {
		t.Helper()
		o, err := s.PrepareOutgoing(ctx, "SIM-TEST", fmt.Sprintf("tid-%d", n), card, outbound.Input{ID: fmt.Sprintf("%032x", n), Slot: slot, Phone: "TEST", Body: body}, 1, now)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	a := prepare(1, 1, "A", "first")
	if _, ok, err := s.ClaimOutgoing(ctx, a.ID, "A", now, limit(now)); err != nil || !ok {
		t.Fatal(err)
	}
	b := prepare(2, 2, "A", "second")
	_, _, err := s.ClaimOutgoing(ctx, b.ID, "A", now+1, limit(now))
	expectSafety(t, err, "send_interval")
	l := limit(now)
	l.Daily = 1
	_, _, err = s.ClaimOutgoing(ctx, b.ID, "A", now+61, l)
	expectSafety(t, err, "daily_limit")
	_, err = s.PrepareOutgoing(ctx, "SIM-TEST", "dup", "A", outbound.Input{ID: fmt.Sprintf("%032x", 3), Slot: 2, Phone: "TEST", Body: "first"}, 1, now)
	expectSafety(t, err, "duplicate_content")
	c := prepare(4, 1, "B", "first")
	if _, ok, err := s.ClaimOutgoing(ctx, c.ID, "B", now+1, l); err != nil || !ok {
		t.Fatal("new card inherited other's limits", err)
	}
	s.DB.Close()
	s = open(t, path)
	_, _, err = s.ClaimOutgoing(ctx, b.ID, "A", now+61, l)
	expectSafety(t, err, "daily_limit")
	_, _, err = s.ClaimOutgoing(ctx, b.ID, "B", now+61, l)
	expectSafety(t, err, "card_changed")
}

func TestCardMultipartMergeAcrossSlotChange(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := sms()
	e.ICCID = "SYNTHETIC-A"
	e.Group = "GROUP"
	e.Max = 2
	done := false
	e.Complete = &done
	e.Body = ""
	e.Fragments = []string{"1,first"}
	ingest(t, s, e)
	e.Slot = 2
	done = false
	e.Fragments = []string{"2,second"}
	ingest(t, s, e)
	rows, err := s.Messages(context.Background(), 0, "")
	if err != nil || len(rows) != 1 || !rows[0].Complete || count(t, s, "notifications") != 1 {
		t.Fatal("multipart identity split", rows, err)
	}
}

func TestCardFilterValidation(t *testing.T) {
	for _, f := range []MessageFilter{{Card: "x"}, {Card: "unknown", Slot: 1}, {Card: "ABCDEF0123456789"}} {
		if f.Valid() {
			t.Fatal("invalid card filter accepted", f)
		}
	}
	if !ValidCardFilter("abcdef0123456789") {
		t.Fatal("valid ID rejected")
	}
}
