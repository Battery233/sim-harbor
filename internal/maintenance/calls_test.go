package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestCallBackupRestoreKeepsHistoryAcceptanceAndHeldQueue(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	now := time.Now().Unix()
	zero := protocol.Number(0)
	var second protocol.Event
	for i := 0; i < 2; i++ {
		e := protocol.Event{DeviceID: "SIM-TEST", Type: 603, Slot: 2, ICCID: "SYNTHETIC-CARD", CardName: "First name", Phone: "anonymous", TelStartTS: protocol.Number(now + int64(i)*30), TelConnectedTS: &zero, TelEndTS: protocol.Number(now + int64(i)*30 + 23)}
		raw, _ := json.Marshal(e)
		if err := s.Ingest(ctx, e, raw, true); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			n, err := s.NextNotification(ctx)
			if err != nil || n.CallID == 0 {
				t.Fatal(err)
			}
			if err = s.NotificationResultCodeAt(ctx, n, true, "accepted", now); err != nil {
				t.Fatal(err)
			}
		}
		second = e
	}
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil || p.Manifest.Schema != store.SchemaVersion || p.Manifest.Counts["calls"] != 2 || p.Manifest.Counts["call_notifications"] != 2 {
		t.Fatal("call backup lost", err)
	}
	target := filepath.Join(t.TempDir(), "restore")
	if _, err = Restore(ctx, a.Path, target, p.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	r, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.DB.Close()
	rows, err := r.Calls(ctx, "", "", 0, now+100)
	if err != nil || len(rows) != 2 || rows[0].NotificationState != "restore_held" || rows[1].NotificationState != "sent" || rows[1].AcceptedAt != now || rows[0].CardID != rows[1].CardID || rows[0].RingSeconds == nil || *rows[0].RingSeconds != 23 {
		t.Fatal("call restore changed evidence", err)
	}
	raw, _ := json.Marshal(second)
	if err = r.Ingest(ctx, second, raw, true); err != nil {
		t.Fatal(err)
	}
	if _, err = r.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("replayed restored call released held push", err)
	}
	blocked, _ := r.RestoreBlocked(ctx)
	stats, err := r.NotificationSuccess(ctx, time.Unix(now+100, 0), time.UTC)
	if !blocked || err != nil || stats.Today != 1 {
		t.Fatal("restore guard or successful call stats lost", err)
	}
}

func TestCallBackupRequiresNotificationSnapshotColumns(t *testing.T) {
	s := fixture(t)
	if _, err := s.DB.Exec(`ALTER TABLE call_notifications DROP COLUMN card_name`); err != nil {
		t.Fatal(err)
	}
	if a, err := Create(context.Background(), s, t.TempDir()); err == nil {
		a.Close()
		t.Fatal("incomplete call schema accepted")
	}
}

func TestCallTimeoutBackupRestoreHoldsQueuedAndUnqueuedDeadlines(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	now := time.Now().Unix()
	zero := protocol.Number(0)
	var first protocol.Event
	for i := 0; i < 2; i++ {
		e := protocol.Event{DeviceID: "SIM-TEST", Type: 601, Slot: 1, ICCID: "SYNTHETIC-CARD", Phone: "SYNTHETIC-CALLER", TelStartTS: protocol.Number(now + int64(i)*30), TelConnectedTS: &zero}
		raw, _ := json.Marshal(e)
		if err := s.Ingest(ctx, e, raw, true); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = e
		}
	}
	c, _ := s.Call(ctx, 1, now)
	s.DB.Exec(`UPDATE calls SET timeout_at=? WHERE id=2`, c.TimeoutAt+1000)
	if err := s.QueueCallTimeouts(ctx, c.TimeoutAt); err != nil {
		t.Fatal(err)
	}
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil || p.Manifest.Schema != store.SchemaVersion {
		t.Fatal("timeout backup", err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(ctx, a.Path, target, p.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	r, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.DB.Close()
	got, _ := r.Call(ctx, 1, now)
	if got.NotificationKind != "timeout" || got.NotificationState != "restore_held" || got.TimeoutAt != 0 {
		t.Fatal("restored fallback was released", got)
	}
	raw, _ := json.Marshal(first)
	r.Ingest(ctx, first, raw, true)
	r.QueueCallTimeouts(ctx, now+10000)
	if _, err := r.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restored old unqueued timeout generated push", err)
	}
	var jobs int
	r.DB.QueryRow(`SELECT count(*) FROM call_notifications`).Scan(&jobs)
	if jobs != 2 {
		t.Fatal("restore did not hold the unexpired old start")
	}
	first.TelStartTS += 30
	first.Type = 603
	first.TelEndTS = first.TelStartTS + 23
	raw, _ = json.Marshal(first)
	r.Ingest(ctx, first, raw, true)
	if _, err := r.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("late final released restored old start", err)
	}
}
