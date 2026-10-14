package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battery233/sim/internal/protocol"
)

func TestNotificationDiagnosticsAndRetryNeverCreateMessageOrSend(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	for i := 0; i < 75; i++ {
		e := sms()
		e.SMSTS += protocol.Number(i)
		ingest(t, s, e)
	}
	n, err := s.NextNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.NotificationResultCode(ctx, n, false, "transport_failed"); err != nil {
		t.Fatal(err)
	}
	rows, summary, err := s.NotificationPage(ctx, "all", 0)
	if err != nil || len(rows) != 51 || summary.Pending != 75 || summary.Retrying != 1 {
		t.Fatal(rows, summary, err)
	}
	older, _, err := s.NotificationPage(ctx, "all", rows[49].ID)
	if err != nil || len(older) != 25 {
		t.Fatal("pagination", len(older), err)
	}
	b, _ := json.Marshal(rows)
	if strings.Contains(string(b), "合成短信") || strings.Contains(string(b), "TEST-SENDER") {
		t.Fatal("private message leaked in diagnostics")
	}
	now := int64(1700000000)
	if err = s.RetryNotification(ctx, n.ID, now); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryNotification(ctx, n.ID, now); err == nil {
		t.Fatal("same retry accepted twice")
	}
	if err = s.NotificationResultCode(ctx, n, false, "sensitive URL"); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryNotification(ctx, n.ID, now+1); err == nil {
		t.Fatal("manual cooldown lost")
	}
	if err = s.NotificationResult(ctx, n, true); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryNotification(ctx, n.ID, now+61); err == nil {
		t.Fatal("accepted task retried")
	}
	if count(t, s, "messages") != 75 || count(t, s, "notifications") != 75 || count(t, s, "outgoing") != 0 {
		t.Fatal("retry created business actions")
	}
	// Queue diagnostics must remain accurate even without a current device row.
	if _, err = s.DB.Exec(`DELETE FROM device`); err != nil {
		t.Fatal(err)
	}
	state, err := s.State(ctx, "SIM-TEST")
	if err != nil || state["notifications_pending"] != 74 || state["last_seen"] != int64(0) {
		t.Fatal("queue hidden by absent device", state, err)
	}
}

func TestNotificationTestReservationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ctx := context.Background()
	now := int64(1700000000)
	if err := s.ReserveNotificationTest(ctx, now); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	if err := s.ReserveNotificationTest(ctx, now+1); err == nil {
		t.Fatal("restart bypassed test cooldown")
	}
	_, sum, err := s.NotificationPage(ctx, "all", 0)
	if err != nil || sum.TestOutcome != "unknown" {
		t.Fatal("crash lost uncertainty", sum, err)
	}
	if err = s.ReserveNotificationTest(ctx, now+60); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteNotificationTest(ctx, now, "accepted"); err != nil {
		t.Fatal(err)
	}
	_, sum, err = s.NotificationPage(ctx, "all", 0)
	if err != nil || sum.TestOutcome != "unknown" {
		t.Fatal("older test overwrote newer test", sum, err)
	}
	if err = s.RetryNotification(ctx, 999, now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestNotificationV3MigrationPreservesQueueAndOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ingest(t, s, sms())
	o := preview(t, s, 1, 1, 1700000000)
	_, err := s.DB.Exec(`DROP TABLE call_notifications;DROP TABLE calls;DROP TABLE status_alert_jobs;DROP TABLE status_alert_monitors;ALTER TABLE cards DROP COLUMN notification_enabled;ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DROP INDEX notifications_success_time;ALTER TABLE notifications DROP COLUMN accepted_at;DROP INDEX outgoing_card_limits;DROP INDEX messages_card;ALTER TABLE messages DROP COLUMN card_id; ALTER TABLE messages DROP COLUMN device_id; DROP TABLE cards; ALTER TABLE messages DROP COLUMN card_name; ALTER TABLE notifications DROP COLUMN manual_retry_at; DROP TABLE notification_test; DELETE FROM schema_version; INSERT INTO schema_version VALUES(3);`)
	if err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	var version int
	if err = s.DB.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}
	rows, sum, err := s.NotificationPage(context.Background(), "all", 0)
	if err != nil || len(rows) != 1 || sum.Pending != 1 {
		t.Fatal("migration lost queue", rows, sum, err)
	}
	if v := view(t, s, o, 1700000000); v.State != "prepared" {
		t.Fatal("migration changed preview", v)
	}
}
