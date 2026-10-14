package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim/internal/protocol"
)

func TestCallTimeoutDeadlineReplayAndLateFinalOnce(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	start := time.Now().Unix() - 10000
	e := callEvent(601, start)
	recordCall(t, s, e, true)
	first, _ := s.Call(ctx, 1, time.Now().Unix())
	if first.TimeoutAt < time.Now().Unix()+299 || first.TimeoutAt > time.Now().Unix()+301 {
		t.Fatal("deadline followed board clock")
	}
	e.MessageTS++
	recordCall(t, s, e, true)
	replay, _ := s.Call(ctx, 1, time.Now().Unix())
	if replay.TimeoutAt != first.TimeoutAt {
		t.Fatal("duplicate renewed deadline")
	}
	if err := s.QueueCallTimeouts(ctx, first.TimeoutAt-1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("early fallback", err)
	}
	s.QueueCallTimeouts(ctx, first.TimeoutAt)
	s.QueueCallTimeouts(ctx, first.TimeoutAt+1)
	n, err := s.NextNotification(ctx)
	if err != nil || n.CallKind != "timeout" || n.CallID != 1 {
		t.Fatal("missing fallback", n, err)
	}
	recordCall(t, s, e, true)
	if next, err := s.NextNotification(ctx); err != nil || next.ID != n.ID {
		t.Fatal("late start canceled fallback", err)
	}
	s.NotificationResultCodeAt(ctx, n, true, "accepted", first.TimeoutAt)
	e.Type, e.TelEndTS = 603, protocol.Number(start+23)
	recordCall(t, s, e, true)
	got, _ := s.Call(ctx, 1, first.TimeoutAt+2)
	if got.State != "missed" || got.RingSeconds == nil || *got.RingSeconds != 23 || got.NotificationKind != "timeout" || got.NotificationState != "sent" {
		t.Fatal("late final lost evidence", got)
	}
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("late final sent twice", err)
	}
	var jobs int
	s.DB.QueryRow(`SELECT count(*) FROM call_notifications`).Scan(&jobs)
	if jobs != 1 {
		t.Fatal("split notification")
	}
}

func TestCallTimeoutLateFinalBeforeOrAfterAttempt(t *testing.T) {
	for _, attempted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unattempted", true: "uncertain_attempt"}[attempted], func(t *testing.T) {
			ctx := context.Background()
			s := open(t, filepath.Join(t.TempDir(), "sim.db"))
			start := time.Now().Unix()
			e := callEvent(601, start)
			recordCall(t, s, e, true)
			c, _ := s.Call(ctx, 1, start)
			s.QueueCallTimeouts(ctx, c.TimeoutAt)
			n, _ := s.NextNotification(ctx)
			if attempted {
				if reserved, err := s.ReserveCallNotification(ctx, n, start); err != nil || !reserved {
					t.Fatal("attempt not persisted", err)
				}
				s.NotificationResultCodeAt(ctx, n, false, "transport_failed", start)
			}
			e.Type, e.TelEndTS = 603, protocol.Number(start+23)
			recordCall(t, s, e, true)
			got, _ := s.Call(ctx, 1, start+24)
			want := "missed"
			if attempted {
				want = "timeout"
			}
			if got.NotificationKind != want {
				t.Fatal("replaced attempted payload", got)
			}
			var jobs int
			s.DB.QueryRow(`SELECT count(*) FROM call_notifications`).Scan(&jobs)
			if jobs != 1 {
				t.Fatal("late end allocated another notification")
			}
		})
	}
}

func TestCallTimeoutExcludesKnownEndConnectionConflictAndDisabled(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	start := time.Now().Unix()
	for i := 0; i < 4; i++ {
		e := callEvent(601, start+int64(i*100))
		recordCall(t, s, e, i != 3)
		switch i {
		case 0:
			e.Type = 603
			e.TelEndTS = e.TelStartTS + 5
			e.TelConnectedTS = nil
			recordCall(t, s, e, true)
		case 1:
			e.Type = 602
			connected := e.TelStartTS + 1
			e.TelConnectedTS = &connected
			recordCall(t, s, e, true)
		case 2:
			e.Type = 602
			e.TelConnectedTS = nil
			recordCall(t, s, e, true)
		case 3:
			recordCall(t, s, e, true) // enabling later does not backfill an old start
		}
	}
	s.QueueCallTimeouts(ctx, start+10000)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("known/disabled event generated timeout", err)
	}
}

func TestCallTimeoutRestartLegacyMigrationAndRestore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	start := time.Now().Unix()
	e := callEvent(601, start)
	recordCall(t, s, e, true)
	first, _ := s.Call(ctx, 1, start)
	s.DB.Close()
	s = open(t, path)
	s.QueueCallTimeouts(ctx, first.TimeoutAt)
	n, err := s.NextNotification(ctx)
	if err != nil || n.CallKind != "timeout" {
		t.Fatal("restart lost deadline", err)
	}
	e = callEvent(601, start+100)
	recordCall(t, s, e, true)
	if err := s.HoldRestoredData(ctx, "SYNTHETIC-ARCHIVE", start); err != nil {
		t.Fatal(err)
	}
	s.QueueCallTimeouts(ctx, start+10000)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restored timeout escaped hold", err)
	}
	s.DB.Close()
	legacy := open(t, filepath.Join(t.TempDir(), "legacy.db"))
	recordCall(t, legacy, callEvent(601, start), true)
	if _, err := legacy.DB.Exec(`DROP TABLE status_alert_jobs;DROP TABLE status_alert_monitors;ALTER TABLE cards DROP COLUMN notification_enabled;ALTER TABLE calls DROP COLUMN timeout_at;ALTER TABLE call_notifications DROP COLUMN attempted_at;ALTER TABLE call_notifications DROP COLUMN kind;DELETE FROM schema_version;INSERT INTO schema_version VALUES(11)`); err != nil {
		t.Fatal(err)
	}
	legacyPath := ""
	legacy.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&legacyPath)
	legacy.DB.Close()
	legacy = open(t, legacyPath)
	c, _ := legacy.Call(ctx, 1, start)
	if c.TimeoutAt != 0 {
		t.Fatal("migration backfilled timeout")
	}
	legacy.QueueCallTimeouts(ctx, start+10000)
	if _, err := legacy.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("legacy start unexpectedly notified", err)
	}
}
