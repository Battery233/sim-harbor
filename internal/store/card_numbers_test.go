package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim/internal/protocol"
)

func setNumberStatus(t *testing.T, s *Store, slot, identity, name string, value any, present bool) {
	t.Helper()
	info := map[string]any{"slot" + slot + "_sta": "OK", "sim" + slot + "_iccId": identity, "sim" + slot + "_scName": name}
	if present {
		info["sim"+slot+"_msIsdn"] = value
	}
	ingest(t, s, protocol.Event{DeviceID: "SIM-TEST", Type: 402, SubType: 46, Code: number(0), SlotInfo: info})
}

func TestCardNumberFollowsIdentityAndExplicitBlankClears(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	setNumberStatus(t, s, "1", "SYNTHETIC-A", "First", " +TEST-NUMBER ", true)
	cards, err := s.Cards(ctx, nil, false)
	if err != nil || len(cards) != 1 || cards[0].Number != "+TEST-NUMBER" {
		t.Fatal("number missing", err)
	}
	id := cards[0].ID
	var group string
	s.DB.QueryRow(`SELECT group_name FROM cards WHERE id=?`, id).Scan(&group)
	e := sms()
	e.ICCID = "SYNTHETIC-A"
	e.CardName = "First"
	ingest(t, s, e)
	setNumberStatus(t, s, "2", "SYNTHETIC-A", "Renamed", "UPDATED-NUMBER", true)
	cards, _ = s.Cards(ctx, nil, false)
	var currentGroup string
	s.DB.QueryRow(`SELECT group_name FROM cards WHERE id=?`, id).Scan(&currentGroup)
	if cards[0].ID != id || cards[0].Number != "UPDATED-NUMBER" || cards[0].MessageCount != 1 || group != currentGroup {
		t.Fatal("rename changed identity/group/count")
	}
	m, _ := s.Message(ctx, 1)
	if m.CardName != "First" {
		t.Fatal("number update relabeled history")
	}
	setNumberStatus(t, s, "2", "SYNTHETIC-A", "Renamed", nil, false)
	cards, _ = s.Cards(ctx, nil, false)
	if cards[0].Number != "UPDATED-NUMBER" {
		t.Fatal("absent field erased last known number")
	}
	setNumberStatus(t, s, "2", "SYNTHETIC-A", "Renamed", 123, true)
	cards, _ = s.Cards(ctx, nil, false)
	if cards[0].Number != "UPDATED-NUMBER" {
		t.Fatal("invalid field replaced number")
	}
	if err = s.DeleteMessage(ctx, 1, 1700000200); err != nil {
		t.Fatal(err)
	}
	cards, _ = s.Cards(ctx, nil, false)
	if cards[0].MessageCount != 0 || cards[0].Number != "UPDATED-NUMBER" {
		t.Fatal("deletion lost card or visible count wrong")
	}
	setNumberStatus(t, s, "2", "SYNTHETIC-A", "Renamed", "", true)
	cards, _ = s.Cards(ctx, nil, false)
	if cards[0].Number != "" {
		t.Fatal("explicit clear ignored")
	}
}

func TestSameNumberCardsRemainSeparateAndVersionNineMigratesUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ctx := context.Background()
	for _, id := range []string{"SYNTHETIC-A", "SYNTHETIC-B"} {
		setNumberStatus(t, s, "1", id, "Shared", "SAME-NUMBER", true)
	}
	cards, _ := s.Cards(ctx, nil, false)
	if len(cards) != 2 || cards[0].ID == cards[1].ID {
		t.Fatal("same phone merged different SIMs")
	}
	originalID := cards[0].ID
	if _, err := s.DB.Exec(`DROP TABLE call_notifications;DROP TABLE calls;DROP TABLE status_alert_jobs;DROP TABLE status_alert_monitors;ALTER TABLE cards DROP COLUMN notification_enabled;ALTER TABLE cards DROP COLUMN number;DELETE FROM schema_version;INSERT INTO schema_version VALUES(9)`); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	cards, err := s.Cards(ctx, nil, false)
	if err != nil || len(cards) != 2 || cards[0].ID != originalID || cards[0].Number != "" {
		t.Fatal("legacy migration guessed number or changed identity", err)
	}
	setNumberStatus(t, s, "1", "SYNTHETIC-A", "Shared", "FRESH-NUMBER", true)
	cards, _ = s.Cards(ctx, nil, false)
	if cards[0].Number != "FRESH-NUMBER" || cards[1].Number != "" {
		t.Fatal("number applied to wrong historical SIM")
	}
}
