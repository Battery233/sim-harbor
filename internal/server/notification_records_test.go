package server

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
	"strings"
	"testing"
)

func TestMixedNotificationRecordsRequireAuthAndExposeSafeLinks(t *testing.T) {
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = "SYNTHETIC-MANAGEMENT-ACCESS-TOKEN" })
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, Phone: "PRIVATE-SYNTHETIC-SENDER", Body: "PRIVATE-SYNTHETIC-BODY", SMSTS: 1000}
	raw, _ := json.Marshal(e)
	if err := s.Store.Ingest(context.Background(), e, raw, true); err != nil {
		t.Fatal(err)
	}
	zero := protocol.Number(0)
	e = protocol.Event{DeviceID: "SIM-TEST", Type: 603, Slot: 1, Phone: "PRIVATE-SYNTHETIC-CALLER", TelStartTS: 1000, TelConnectedTS: &zero, TelEndTS: 1002}
	raw, _ = json.Marshal(e)
	if err := s.Store.Ingest(context.Background(), e, raw, true); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", "http://example.invalid/api/notification-records", "", ""); w.Code != 401 {
		t.Fatal("anonymous records", w.Code)
	}
	for _, tc := range []struct {
		query string
		count int
	}{{"", 2}, {"?type=call", 1}, {"?type=sms", 1}, {"?state=sent", 0}} {
		w := request(s, "GET", "http://example.invalid/api/notification-records"+tc.query, "Bearer "+s.Config.AdminToken, "")
		var result struct {
			Items []store.NotificationRecord `json:"items"`
			Next  string                     `json:"next_before"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != tc.count {
			t.Fatal("mixed records", tc.query, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "PRIVATE-SYNTHETIC") {
			t.Fatal("private content in task feed")
		}
	}
	for _, query := range []string{"?type=alert", "?state=invalid", "?before=bad"} {
		if w := request(s, "GET", "http://example.invalid/api/notification-records"+query, "Bearer "+s.Config.AdminToken, ""); w.Code != 400 {
			t.Fatal("invalid filter", w.Code)
		}
	}
}
