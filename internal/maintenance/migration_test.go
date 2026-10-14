package maintenance

import (
	"github.com/Battery233/sim-harbor/internal/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoringConfirmedBackupRequiresNewConfirmation(t *testing.T) {
	s := fixture(t)
	ctx := t.Context()
	if err := s.HoldRestoredData(ctx, strings.Repeat("a", 64), 1700000000); err != nil {
		t.Fatal(err)
	}
	m, err := s.MigrationState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmMigration(ctx, m.Revision, strings.Repeat("b", 64), m.LedgerRevision, 1700000001); err != nil {
		t.Fatal(err)
	}
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(ctx, a.Path, target, p.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.DB.Close()
	next, err := restored.MigrationState(ctx)
	if err != nil || !next.Blocked || next.ConfirmedAt != 0 || next.Revision == m.Revision || next.Policy != "" {
		t.Fatal(next, err)
	}
}
