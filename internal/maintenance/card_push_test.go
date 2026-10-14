package maintenance

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim/internal/protocol"
	"github.com/Battery233/sim/internal/store"
	"path/filepath"
	"testing"
)

func TestCardPushPreferenceSurvivesBackupRestore(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	e := protocol.Event{Type: 501, DeviceID: "SIM-TEST", Slot: 1, ICCID: "SYNTHETIC", Body: "TEXT", SMSTS: 1700000000}
	raw, _ := json.Marshal(e)
	s.Ingest(ctx, e, raw, true)
	cards, _ := s.Cards(ctx, nil, false)
	s.SetCardNotifications(ctx, cards[0].ID, false, true)
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
	r, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.DB.Close()
	got, _ := r.Cards(ctx, nil, false)
	if len(got) != 1 || got[0].NotificationEnabled || got[0].ID != cards[0].ID {
		t.Fatal("preference lost")
	}
}
