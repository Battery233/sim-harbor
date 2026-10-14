package maintenance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim/internal/protocol"
	"github.com/Battery233/sim/internal/store"
)

func TestCardNumbersSurviveRestoreAndRequireVersionTenColumn(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	code := protocol.Number(0)
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 402, SubType: 46, Code: &code, SlotInfo: map[string]any{"slot1_sta": "OK", "sim1_iccId": "SYNTHETIC-A", "sim1_scName": "Synthetic Card", "sim1_msIsdn": "SYNTHETIC-NUMBER"}}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(ctx, e, raw, false); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Cards(ctx, nil, false)
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil {
		t.Fatal(err)
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
	after, _ := r.Cards(ctx, nil, false)
	if len(after) != 1 || after[0].ID != before[0].ID || after[0].Number != "SYNTHETIC-NUMBER" {
		t.Fatal("restored number/identity missing")
	}
	blocked, _ := r.RestoreBlocked(ctx)
	if !blocked {
		t.Fatal("guard lost")
	}
	if _, err = s.DB.Exec(`ALTER TABLE cards DROP COLUMN number`); err != nil {
		t.Fatal(err)
	}
	if invalid, err := Create(ctx, s, t.TempDir()); err == nil {
		invalid.Close()
		t.Fatal("malformed v10 accepted")
	}
}
