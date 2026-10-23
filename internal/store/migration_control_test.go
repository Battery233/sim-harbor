package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrationControlClaimsAndResultsInvalidateOldConfirmation(t *testing.T) {
	for _, change := range []string{"claim", "accepted", "rejected", "unknown"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			s := open(t, filepath.Join(t.TempDir(), "sim.db"))
			now := int64(1700000000)
			if err := s.HoldRestoredData(ctx, strings.Repeat("a", 64), now); err != nil {
				t.Fatal(err)
			}
			claim := func() int64 {
				id, err := s.ClaimVoicemailControl(ctx, "SIM-TEST", 1, "SYNTHETIC-DESTINATION", "SYNTHETIC-CARD", "mode", 0, false, now+1, DayLimits(time.Unix(now, 0), 60, 5), false)
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			var id int64
			if change != "claim" {
				id = claim()
			}
			before, err := s.MigrationState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if change == "claim" {
				claim()
			} else if err := s.MarkVoicemailControl(ctx, id, change); err != nil {
				t.Fatal(err)
			}
			after, err := s.MigrationState(ctx)
			if err != nil || after.LedgerRevision == before.LedgerRevision || !after.Blocked {
				t.Fatal("setting SMS did not invalidate old evidence", after, err)
			}
			wantUnknown := int64(0)
			if change == "claim" || change == "unknown" {
				wantUnknown = 1
			}
			if after.UnknownOutgoing != wantUnknown {
				t.Fatal("setting SMS missing from unresolved count", after.UnknownOutgoing)
			}
			expectSafety(t, s.ConfirmMigration(ctx, before.Revision, strings.Repeat("b", 64), before.LedgerRevision, now+2), "migration_state_changed")
			if err := s.ConfirmMigration(ctx, after.Revision, strings.Repeat("b", 64), after.LedgerRevision, now+2); err != nil {
				t.Fatal("fresh confirmation failed", err)
			}
			if count(t, s, "voicemail_controls") != 1 || count(t, s, "outgoing") != 0 {
				t.Fatal("confirmation changed send history")
			}
		})
	}
}
