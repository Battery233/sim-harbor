package maintenance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim/internal/protocol"
	"github.com/Battery233/sim/internal/store"
)

func TestRestoreMoreThanTwoCardsKeepsIdentityNamesAndSafety(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	for i, id := range []string{"CARD-A", "CARD-B", "CARD-C", ""} {
		e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: protocol.Number(i%2 + 1), ICCID: id, CardName: "Synthetic name", Phone: "TEST", Body: "SYNTHETIC", SMSTS: protocol.Number(1700000000 + i)}
		raw, _ := json.Marshal(e)
		if err := s.Ingest(ctx, e, raw, true); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.Cards(ctx, nil, false)
	var group string
	s.DB.QueryRow(`SELECT group_name FROM cards WHERE id=?`, before[0].ID).Scan(&group)
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil || p.Manifest.Schema != store.SchemaVersion || p.Manifest.Counts["cards"] != 3 {
		t.Fatal("cards not backed up", p, err)
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
	after, err := r.Cards(ctx, nil, false)
	if err != nil || len(after) != 3 || after[0].ID != before[0].ID {
		t.Fatal("identity lost", after, err)
	}
	var restoredGroup string
	r.DB.QueryRow(`SELECT group_name FROM cards WHERE id=?`, after[0].ID).Scan(&restoredGroup)
	if group != restoredGroup {
		t.Fatal("Bark grouping changed during restore")
	}
	unknown, _ := r.CardMessagePage(ctx, "unknown", 0, "", 0, 10)
	known, _ := r.CardMessagePage(ctx, after[0].ID, 0, "", 0, 10)
	blocked, err := r.RestoreBlocked(ctx)
	if err != nil || !blocked || len(unknown) != 1 || len(known) != 1 || known[0].CardName != "Synthetic name" {
		t.Fatal("history or restore safety lost")
	}
	var held int
	r.DB.QueryRow(`SELECT count(*) FROM notifications WHERE state='restore_held'`).Scan(&held)
	if held != 4 {
		t.Fatal("restored notifications automatically released")
	}
}

func TestVersionSevenBackupRejectsMissingCardIdentityColumn(t *testing.T) {
	s := fixture(t)
	if _, err := s.DB.Exec(`DROP INDEX messages_card;ALTER TABLE messages DROP COLUMN card_id`); err != nil {
		t.Fatal(err)
	}
	if a, err := Create(context.Background(), s, t.TempDir()); err == nil {
		a.Close()
		t.Fatal("malformed v7 accepted")
	}
}
