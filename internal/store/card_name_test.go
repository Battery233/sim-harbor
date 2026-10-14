package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battery233/sim/internal/protocol"
)

func TestCardNameSnapshotSurvivesReplayRetryAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := sms()
	e.CardName = "  SYNTHETIC HK\n "
	ingest(t, s, e)
	n, err := s.NextNotification(ctx)
	if err != nil || n.CardName != "SYNTHETIC HK" {
		t.Fatal("receipt name missing", n.CardName, err)
	}
	if err = s.NotificationResultCode(ctx, n, false, "transport_failed"); err != nil {
		t.Fatal(err)
	}
	e.CardName = "NEW SLOT OCCUPANT"
	ingest(t, s, e)
	if _, err = s.DB.Exec(`UPDATE notifications SET next_at=0`); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	reopened := open(t, path)
	n, err = reopened.NextNotification(ctx)
	if err != nil || n.CardName != "SYNTHETIC HK" || n.Attempts != 1 || count(t, reopened, "messages") != 1 || count(t, reopened, "notifications") != 1 {
		t.Fatal("replay/restart relabeled or duplicated task", n, err)
	}
	// A later event for the same unknown-name message cannot claim a new name.
	e.SMSTS++
	e.CardName = ""
	ingest(t, reopened, e)
	e.CardName = "NEW SLOT OCCUPANT"
	ingest(t, reopened, e)
	var unknownName string
	if err = reopened.DB.QueryRow(`SELECT card_name FROM messages ORDER BY id DESC LIMIT 1`).Scan(&unknownName); err != nil || unknownName != "" {
		t.Fatal("unknown historical name was guessed", unknownName, err)
	}
}

func TestCardNameStatusFallbackRequiresMatchingFreshIdentity(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 402, SubType: 46, Code: number(0), SlotInfo: map[string]any{"slot1_sta": "OK", "sim1_iccId": "SYNTHETIC-CARD", "sim1_scName": " SYNTHETIC HK "}}
	ingest(t, s, e)
	if _, err := s.DB.Exec(`UPDATE device SET status_received_at=100`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, device, card string
		slot               int
		earliest, latest   int64
		want               string
	}{
		{"match", "SIM-TEST", "SYNTHETIC-CARD", 1, 90, 110, "SYNTHETIC HK"},
		{"different card", "SIM-TEST", "OTHER", 1, 90, 110, ""},
		{"unknown card", "SIM-TEST", "", 1, 90, 110, ""},
		{"different device", "OTHER", "SYNTHETIC-CARD", 1, 90, 110, ""},
		{"invalid slot", "SIM-TEST", "SYNTHETIC-CARD", 0, 90, 110, ""},
		{"stale status", "SIM-TEST", "SYNTHETIC-CARD", 1, 101, 110, ""},
		{"future status", "SIM-TEST", "SYNTHETIC-CARD", 1, 80, 99, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, err := s.CardNameFromStatus(ctx, tc.device, tc.slot, tc.card, tc.earliest, tc.latest)
			if err != nil || name != tc.want {
				t.Fatal(name, err)
			}
		})
	}
	e.SlotInfo["slot1_sta"] = "NS"
	ingest(t, s, e)
	name, err := s.CardNameFromStatus(ctx, "SIM-TEST", 1, "SYNTHETIC-CARD", 0, 9999999999)
	if err != nil || name != "" {
		t.Fatal("unregistered card used as fallback", name, err)
	}
	if got := cleanCardName(strings.Repeat("名", 100)); len([]rune(got)) != 64 {
		t.Fatal("unbounded name")
	}
}

func TestVersionFiveMigrationKeepsOldNotificationNamesUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := sms()
	e.CardName = "SYNTHETIC OLD"
	ingest(t, s, e)
	if _, err := s.DB.Exec(`DROP TABLE call_notifications;DROP TABLE calls;DROP TABLE status_alert_jobs;DROP TABLE status_alert_monitors;ALTER TABLE cards DROP COLUMN notification_enabled;ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DROP INDEX notifications_success_time;ALTER TABLE notifications DROP COLUMN accepted_at;DROP INDEX outgoing_card_limits;DROP INDEX messages_card;ALTER TABLE messages DROP COLUMN card_id; ALTER TABLE messages DROP COLUMN device_id; DROP TABLE cards; ALTER TABLE messages DROP COLUMN card_name; DELETE FROM schema_version; INSERT INTO schema_version VALUES(5)`); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	reopened := open(t, path)
	var version int
	if err := reopened.DB.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}
	n, err := reopened.NextNotification(context.Background())
	if err != nil || n.CardName != "" || n.Body != e.Body || count(t, reopened, "outgoing") != 0 {
		t.Fatal("migration changed old task or created a send", n, err)
	}
}
