package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestCardIdentityAPIAndExportPreserveHistoricalSource(t *testing.T) {
	s, l := fixture(t, 1)
	ctx := context.Background()
	for i, iccid := range []string{"CARD-A", "CARD-B", "CARD-C", ""} {
		e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, ICCID: iccid, CardName: "Same name", Phone: "TEST", Body: "SYNTHETIC", SMSTS: protocol.Number(1700000000 + i)}
		raw, _ := json.Marshal(e)
		if err := s.Store.Ingest(ctx, e, raw, false); err != nil {
			t.Fatal(err)
		}
	}
	cards, _ := s.Store.Cards(ctx, nil, false)
	call := func(path string) []byte {
		t.Helper()
		w := request(s, "GET", "http://localhost/api/"+path, "", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		return w.Body.Bytes()
	}
	for _, card := range []string{cards[0].ID, "unknown"} {
		var v struct{ Items []store.Message }
		json.Unmarshal(call("messages?card="+card), &v)
		if len(v.Items) != 1 || (card != "unknown" && v.Items[0].CardID != card) || (card == "unknown" && v.Items[0].CardID != "") {
			t.Fatal("filter misattributed", v)
		}
		if strings.Contains(string(call("messages/export?card="+card)), "CARD-A") {
			t.Fatal("export exposed raw ICCID")
		}
	}
	for _, p := range []string{"messages?card=invalid", "messages?card=unknown&slot=1", "messages/export?card=unknown&slot=2"} {
		if w := request(s, "GET", "http://localhost/api/"+p, "", ""); w.Code != 400 {
			t.Fatal("bad filter accepted", p, w.Code)
		}
	}
	conn := connect(t, l)
	send(t, conn, s.codec(), map[string]any{"type": 402, "subType": 46, "code": 0, "devId": "SIM-TEST", "slotInfo": map[string]any{"slot2_sta": "OK", "sim2_iccId": "CARD-A", "sim2_scName": "New name"}})
	deadline := time.Now().Add(2 * time.Second)
	var state struct {
		Cards    []store.Card
		Verified bool `json:"card_locations_verified"`
	}
	for {
		json.Unmarshal(call("state"), &state)
		if state.Verified {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("status not current")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(state.Cards) != 3 || state.Cards[0].ID != cards[0].ID || state.Cards[0].Slot != 2 || state.Cards[0].Name != "New name" || state.Cards[1].Slot != 0 {
		t.Fatal("current and historical cards confused", state)
	}
	var v struct{ Items []store.Message }
	json.Unmarshal(call("messages?card="+cards[0].ID), &v)
	if v.Items[0].CardName != "Same name" || v.Items[0].Slot != 1 {
		t.Fatal("display snapshot overwritten")
	}
	if _, err := s.Store.DB.Exec(`UPDATE device SET status_received_at=?`, time.Now().Unix()-400); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(call("state"), &state)
	if state.Verified || state.Cards[0].Slot != 0 {
		t.Fatal("stale card location presented as current")
	}
}
