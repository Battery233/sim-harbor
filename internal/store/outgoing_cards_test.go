package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battery233/sim/internal/outbound"
)

func TestOutgoingIdentityFilterAndPagination(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	const a, b = "0123456789abcdef", "fedcba9876543210"
	_, err := s.DB.Exec(`INSERT INTO cards(id,iccid,name,group_name,number) VALUES(?,?,?,?,?),(?,?,?,?,?)`, a, "SYNTHETIC-A", "Same name", "A", "TEST-NUMBER", b, "SYNTHETIC-B", "Same name", "B", "TEST-NUMBER")
	if err != nil {
		t.Fatal(err)
	}
	const now = int64(1700000000)
	for i := 1; i <= 72; i++ {
		iccid := "SYNTHETIC-A"
		if i > 65 {
			iccid = "SYNTHETIC-B"
		}
		if i > 70 {
			iccid = "SYNTHETIC-UNREGISTERED"
		}
		_, err = s.PrepareOutgoing(ctx, "SIM-TEST", fmt.Sprintf("tid-%d", i), iccid, outbound.Input{ID: fmt.Sprintf("%032x", i), Slot: 1, Phone: "000", Body: fmt.Sprintf("synthetic-%d", i)}, 1, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.OutgoingPage(ctx, 0, now, a)
	if err != nil || len(first) != 51 || first[0].Sequence != 65 {
		t.Fatal("filter must precede limit", err)
	}
	for _, o := range first {
		if o.CardID != a || o.Slot != 1 || o.CardCurrentName != "Same name" || o.CardNumber != "TEST-NUMBER" {
			t.Fatal("identity mapping was guessed from slot or name")
		}
	}
	second, err := s.OutgoingPage(ctx, first[49].Sequence, now, a)
	if err != nil || len(second) != 15 || second[14].Sequence != 1 {
		t.Fatal("filtered cursor missed or duplicated rows", err)
	}
	other, err := s.OutgoingPage(ctx, 0, now, b)
	if err != nil || len(other) != 5 || other[0].CardID != b {
		t.Fatal("same name/number identities merged", err)
	}
	unknown, err := s.OutgoingPage(ctx, 0, now, "unknown")
	if err != nil || len(unknown) != 2 || unknown[0].CardID != "" {
		t.Fatal("unregistered identity guessed", err)
	}
	if _, err = s.OutgoingPage(ctx, 0, now, "1"); err == nil {
		t.Fatal("slot accepted as card identity")
	}
	// A later rename changes the explicitly current name, never operation slot.
	if _, err = s.DB.Exec(`UPDATE cards SET name='Current renamed card' WHERE id=?`, a); err != nil {
		t.Fatal(err)
	}
	o, err := s.Outgoing(ctx, first[0].ID, now)
	if err != nil || o.CardCurrentName != "Current renamed card" || o.CardID != a || o.Slot != 1 || o.State != "prepared" {
		t.Fatal("read-only enrichment changed operation", err)
	}
	raw, err := json.Marshal(o)
	if err != nil || strings.Contains(string(raw), "SYNTHETIC-A") || strings.Contains(string(raw), "iccid") {
		t.Fatal("private identity exposed by enrichment", err)
	}
}
