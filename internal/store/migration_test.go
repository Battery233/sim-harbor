package store

import (
	"context"
	"github.com/Battery233/sim-harbor/internal/outbound"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMigrationConfirmationPreservesClaimsLimitsAndUnknowns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	now := int64(1700000000)
	old := preview(t, s, 1, 1, now)
	if _, ok, err := s.ClaimOutgoing(ctx, old.ID, old.ICCID, now, limit(now)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	pending := preview(t, s, 2, 1, now+1)
	if err := s.HoldRestoredData(ctx, strings.Repeat("a", 64), now+2); err != nil {
		t.Fatal(err)
	}
	m, err := s.MigrationState(ctx)
	if err != nil || !m.Blocked || m.UnknownOutgoing != 1 {
		t.Fatal(m, err)
	}
	expectSafety(t, s.ConfirmMigration(ctx, strings.Repeat("0", 32), strings.Repeat("b", 64), m.LedgerRevision, now+3), "migration_state_changed")
	expectSafety(t, s.ConfirmMigration(ctx, m.Revision, strings.Repeat("b", 64), "stale", now+3), "migration_state_changed")
	var success atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if err := s.ConfirmMigration(ctx, m.Revision, strings.Repeat("b", 64), m.LedgerRevision, now+3); err == nil {
				success.Add(1)
			}
		})
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("confirmation was not exclusive", success.Load())
	}
	got, err := s.Outgoing(ctx, old.ID, now+4)
	if err != nil || got.State != "unknown" || got.DispatchedAt != now {
		t.Fatal(got, err)
	}
	got, err = s.Outgoing(ctx, pending.ID, now+4)
	if err != nil || got.State != "canceled" || got.DispatchedAt != 0 {
		t.Fatal(got, err)
	}
	_, err = s.PrepareOutgoing(ctx, "SIM-TEST", "new-tid", old.ICCID, outbound.Input{ID: strings.Repeat("f", 32), Slot: 1, Phone: old.Phone, Body: old.Body}, 1, now+65)
	expectSafety(t, err, "duplicate_content")
	newer := preview(t, s, 3, 1, now+65)
	limits := limit(now + 65)
	limits.Daily = 1
	_, claimed, err := s.ClaimOutgoing(ctx, newer.ID, newer.ICCID, now+65, limits)
	expectSafety(t, err, "daily_limit")
	if claimed {
		t.Fatal("missing old limit")
	}
	s.DB.Close()
	reopened := open(t, path)
	m, err = reopened.MigrationState(ctx)
	if err != nil || m.Blocked || m.ConfirmedAt != now+3 || m.Policy != "complete_history_v1" || m.BackupSHA256 != strings.Repeat("b", 64) {
		t.Fatal(m, err)
	}
	if err = reopened.HoldRestoredData(ctx, strings.Repeat("a", 64), now+2); err != nil {
		t.Fatal(err)
	}
	next, err := reopened.MigrationState(ctx)
	if err != nil || !next.Blocked || next.Revision == m.Revision || next.ConfirmedAt != 0 || next.BackupSHA256 != "" {
		t.Fatal(next, err)
	}
	expectSafety(t, reopened.ConfirmMigration(ctx, m.Revision, strings.Repeat("b", 64), next.LedgerRevision, now+4), "migration_state_changed")
}

func TestLegacyV14MigrationStaysBlockedAndKeepsOrigin(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	if err := s.HoldRestoredData(ctx, strings.Repeat("c", 64), 1700000000); err != nil {
		t.Fatal(err)
	}
	_, err := s.DB.Exec(`ALTER TABLE recovery_guard DROP COLUMN restore_id;ALTER TABLE recovery_guard DROP COLUMN confirmed_at;ALTER TABLE recovery_guard DROP COLUMN confirmation_backup_sha256;ALTER TABLE recovery_guard DROP COLUMN confirmation_policy;DELETE FROM schema_version;INSERT INTO schema_version VALUES(14)`)
	if err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	m, err := s.MigrationState(ctx)
	if err != nil || !m.Blocked || len(m.Revision) != 32 || m.RestoredAt != 1700000000 || m.ArchiveSHA256 != strings.Repeat("c", 64) {
		t.Fatal(m, err)
	}
}
