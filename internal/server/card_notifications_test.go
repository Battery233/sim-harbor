package server

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/protocol"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCardPushAPIRequiresIdentityAuthAndDoesNotSendCommands(t *testing.T) {
	s, _ := fixture(t, 1, func(c *config.Config) { c.AdminToken = strings.Repeat("t", 24) })
	e := protocol.Event{Type: 501, DeviceID: "SIM-TEST", Slot: 1, ICCID: "SYNTHETIC-CARD", Body: "SYNTHETIC", SMSTS: 1700000000}
	raw, _ := json.Marshal(e)
	s.Store.Ingest(context.Background(), e, raw, true)
	cards, _ := s.Store.Cards(context.Background(), nil, false)
	put := func(url, auth, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", url, strings.NewReader(body))
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("Content-Type", "application/json")
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}

	path := "http://example.com/api/cards/" + cards[0].ID + "/notifications"
	if w := put(path, "", `{"enabled":false,"expected":true}`); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{}`, `{"enabled":false}`, `{"enabled":false,"expected":true,"cmd":"restart"}`} {
		if w := put(path, "Bearer "+s.Config.AdminToken, body); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	w := put(path, "Bearer "+s.Config.AdminToken, `{"enabled":false,"expected":true}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	after, _ := s.Store.Cards(context.Background(), nil, false)
	if after[0].NotificationEnabled {
		t.Fatal("not saved")
	}
	var total int
	s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&total)
	if total != 0 {
		t.Fatal("chargeable action created")
	}
}
