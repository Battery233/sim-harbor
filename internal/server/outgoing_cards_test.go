package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Battery233/sim/internal/outbound"
	"github.com/Battery233/sim/internal/store"
)

func TestOutgoingCardReadRoutes(t *testing.T) {
	s, _ := fixture(t, 0)
	const card = "0123456789abcdef"
	_, err := s.Store.DB.Exec(`INSERT INTO cards(id,iccid,name,group_name,number) VALUES(?,?,?,?,?)`, card, "SYNTHETIC-CARD", "Synthetic card", "test", "TEST-NUMBER")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.Store.PrepareOutgoing(context.Background(), "SIM-TEST", "tid-test", "SYNTHETIC-CARD", outbound.Input{ID: "0123456789abcdef0123456789abcdef", Slot: 1, Phone: "000", Body: "synthetic"}, 1, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{"", card, "unknown", "fedcba9876543210"} {
		w := request(s, "GET", "http://example.com/api/outgoing?card="+filter, "", "")
		var page struct {
			Items []store.Outgoing `json:"items"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatal("read route failed", w.Code)
		}
		want := 0
		if filter == "" || filter == card {
			want = 1
		}
		if len(page.Items) != want {
			t.Fatal("incorrect identity filter")
		}
	}
	w := request(s, "GET", "http://example.com/api/outgoing?card=1", "", "")
	if w.Code != 400 {
		t.Fatal("malformed card filter accepted")
	}
	got, err := s.Store.Outgoing(context.Background(), o.ID, time.Now().Unix())
	if err != nil || got.State != "prepared" || got.DispatchedAt != 0 || got.CardID != card {
		t.Fatal("GET history dispatched or changed operation", err)
	}
}
