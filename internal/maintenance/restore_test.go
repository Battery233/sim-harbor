package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/outbound"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestRestoreKeepsHistoryButBlocksEveryOutboundAndHoldsOldPushes(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	now := time.Now().Unix()
	old := strings.Repeat("a", 32)
	sent := strings.Repeat("b", 32)
	if _, e := s.DB.Exec(`INSERT INTO messages(key,slot,phone,body,sms_ts,complete,is_read) VALUES('restore',1,'TEST','合成恢复短信',?,1,1); INSERT INTO notifications(message_id) VALUES(1);`, now); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		id         string
		dispatched int64
		state      string
		rank       int
	}{{old, 0, "prepared", 0}, {sent, now, "waiting_device", 1}} {
		if _, e := s.DB.Exec(`INSERT INTO outgoing(id,device_id,tid,fingerprint,slot,phone,body,segments,iccid,created_at,updated_at,expires_at,dispatched_at,state,rank) VALUES(?,'SIM-TEST',?, ?,1,'000000','test',1,'synthetic-card',?,?,?, ?,?,?)`, v.id, v.id, v.id, now, now, now+300, v.dispatched, v.state, v.rank); e != nil {
			t.Fatal(e)
		}
	}
	a, e := Create(ctx, s, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	preview, e := Inspect(ctx, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "restored")
	result, e := Restore(ctx, a.Path, target, preview.ArchiveSHA256)
	if e != nil || !result.OutboundBlocked {
		t.Fatal(result, e)
	}
	_, originalHash, _ := fileDigest(a.Path)
	for pass := 0; pass < 2; pass++ {
		restored, e := store.Open(filepath.Join(target, "sim.db"))
		if e != nil {
			t.Fatal(e)
		}
		blocked, e := restored.RestoreBlocked(ctx)
		if e != nil || !blocked {
			t.Fatal("restart lost guard", e)
		}
		m, e := restored.Message(ctx, 1)
		if e != nil || !m.Read || m.Body != "合成恢复短信" {
			t.Fatal(m, e)
		}
		o, e := restored.Outgoing(ctx, old, now)
		if e != nil || o.State != "canceled" || o.DispatchedAt != 0 {
			t.Fatal(o, e)
		}
		o, e = restored.Outgoing(ctx, sent, now)
		if e != nil || o.State != "unknown" || o.DispatchedAt != now {
			t.Fatal(o, e)
		}
		input := outbound.Input{ID: strings.Repeat("c", 32), Slot: 1, Phone: "000000", Body: "new synthetic"}
		_, e = restored.PrepareOutgoing(ctx, "SIM-TEST", "new-tid", "synthetic-card", input, 1, now)
		var se *store.SafetyError
		if !errors.As(e, &se) || se.Code != "outbound_restore_blocked" {
			t.Fatal("new preview permitted", e)
		}
		_, claimed, e := restored.ClaimOutgoing(ctx, old, "synthetic-card", now, store.SendLimits{Interval: 60, Daily: 5, DayStart: now - 3600, DayEnd: now + 3600})
		if claimed || !errors.As(e, &se) || se.Code != "outbound_restore_blocked" {
			t.Fatal("old claim permitted", e)
		}
		rows, sum, e := restored.NotificationPage(ctx, "restore_held", 0)
		if e != nil || len(rows) != 1 || sum.Held != 1 || sum.Pending != 0 {
			t.Fatal(rows, sum, e)
		}
		if _, e = restored.NextNotification(ctx); !errors.Is(e, sql.ErrNoRows) {
			t.Fatal("old notification became runnable", e)
		}
		if e = restored.RetryNotification(ctx, 1, now+3600); e == nil {
			t.Fatal("held notification retry allowed")
		}
		restored.DB.Close()
	}
	_, digest, e := fileDigest(a.Path)
	if e != nil || digest != originalHash {
		t.Fatal("original archive changed", e)
	}
	var originalState string
	if e = s.DB.QueryRow(`SELECT state FROM outgoing WHERE id=?`, old).Scan(&originalState); e != nil || originalState != "prepared" {
		t.Fatal("source changed", originalState, e)
	}
}

func TestRestoreRejectsConfirmationExistingDestinationAndCanceledWork(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	a, e := Create(ctx, s, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	p, e := Inspect(ctx, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "new")
	if _, e = Restore(ctx, a.Path, target, strings.Repeat("0", 64)); !errors.Is(e, ErrConfirmation) {
		t.Fatal(e)
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("invalid confirmation created target", e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = Restore(canceled, a.Path, target, p.ArchiveSHA256); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = os.Mkdir(target, 0700); e != nil {
		t.Fatal(e)
	}
	sentinel := filepath.Join(target, "preserve")
	os.WriteFile(sentinel, []byte("original"), 0600)
	if _, e = Restore(ctx, a.Path, target, p.ArchiveSHA256); e == nil {
		t.Fatal("existing target accepted")
	}
	b, _ := os.ReadFile(sentinel)
	if string(b) != "original" {
		t.Fatal("existing target changed")
	}
	changed := rewrite(t, a.Path, func(n string, b []byte) (string, []byte) { return n, b }, false)
	if _, e = Restore(ctx, changed, filepath.Join(t.TempDir(), "other"), p.ArchiveSHA256); !errors.Is(e, ErrConfirmation) {
		t.Fatal("preview binding failed", e)
	}
}

func TestVersionFourRestoreMigrationAndIncompleteDirectoryRefusal(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	if _, e := s.DB.Exec(`DROP TABLE IF EXISTS voicemail_controls; DROP TABLE IF EXISTS voicemails;DROP TABLE sms_receipts;DROP TABLE call_notifications;DROP TABLE calls;DROP TABLE status_alert_jobs;DROP TABLE status_alert_monitors;ALTER TABLE cards DROP COLUMN notification_enabled;ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DROP INDEX notifications_success_time;ALTER TABLE notifications DROP COLUMN accepted_at;DROP INDEX outgoing_card_limits;DROP INDEX messages_card;ALTER TABLE messages DROP COLUMN card_id; ALTER TABLE messages DROP COLUMN device_id; DROP TABLE cards; ALTER TABLE messages DROP COLUMN card_name;DROP TABLE recovery_guard;DELETE FROM schema_version;INSERT INTO schema_version VALUES(4)`); e != nil {
		t.Fatal(e)
	}
	a, e := Create(ctx, s, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	p, e := Inspect(ctx, a.Path)
	if e != nil || p.Manifest.Schema != 4 {
		t.Fatal(p, e)
	}
	target := filepath.Join(t.TempDir(), "new")
	if _, e = Restore(ctx, a.Path, target, p.ArchiveSHA256); e != nil {
		t.Fatal(e)
	}
	restored, e := store.Open(filepath.Join(target, "sim.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer restored.DB.Close()
	var v int
	if e = restored.DB.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&v); e != nil || v != store.SchemaVersion {
		t.Fatal(v, e)
	}
	broken := t.TempDir()
	if e = os.WriteFile(filepath.Join(broken, ".restore-incomplete"), []byte("unfinished"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Open(filepath.Join(broken, "sim.db")); e == nil {
		t.Fatal("unfinished restore opened")
	}
	if _, e = os.Stat(filepath.Join(broken, "sim.db")); !os.IsNotExist(e) {
		t.Fatal("unfinished restore created DB", e)
	}
}

func TestLeaseAndOfflineBackupDoNotAllowRunningOrOverwrite(t *testing.T) {
	s := fixture(t)
	var file string
	if e := s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&file); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Dir(file)
	first, e := Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Acquire(dir); !errors.Is(e, ErrBusy) {
		t.Fatal("second process lease accepted", e)
	}
	output := filepath.Join(t.TempDir(), "offline.zip")
	if e = BackupDirectory(context.Background(), dir, output); !errors.Is(e, ErrBusy) {
		t.Fatal("backup running source permitted", e)
	}
	first.Close()
	s.DB.Close()
	if e = BackupDirectory(context.Background(), dir, output); e != nil {
		t.Fatal(e)
	}
	if _, e = Inspect(context.Background(), output); e != nil {
		t.Fatal(e)
	}
	_, hash, _ := fileDigest(output)
	if e = BackupDirectory(context.Background(), dir, output); e == nil {
		t.Fatal("existing backup overwritten")
	}
	_, after, _ := fileDigest(output)
	if hash != after {
		t.Fatal("existing backup changed")
	}
	second, e := Acquire(dir)
	if e != nil {
		t.Fatal("lease not released", e)
	}
	second.Close()
}
