package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestDeletedMessageBackupRestoreDoesNotResurrect(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, ICCID: "SYNTHETIC-CARD", Phone: "TEST", Body: "SYNTHETIC", SMSTS: 1700000000}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMessage(ctx, 1, 1700000100); err != nil {
		t.Fatal(err)
	}
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil || p.Manifest.Schema != store.SchemaVersion {
		t.Fatal(p, err)
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
	e.Slot = 2
	raw, _ = json.Marshal(e)
	if err = r.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	ms, err := r.Messages(ctx, 0, "")
	if err != nil || len(ms) != 0 {
		t.Fatal("restore replay resurrected", err)
	}
	if _, err = r.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restored canceled push", err)
	}
	blocked, _ := r.RestoreBlocked(ctx)
	if !blocked {
		t.Fatal("restore guard lost")
	}
}
