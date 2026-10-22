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

func TestSystemNotificationRecordAPIHasNoBusinessLinksOrPrivateLabels(t *testing.T) {
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = "SYNTHETIC-SYSTEM-ACCESS-TOKEN" })
	if _, err := s.Store.DB.Exec(`INSERT INTO status_alert_monitors(key) VALUES('board');INSERT INTO status_alert_jobs(key,episode,kind,label,created_at) VALUES('board',1,'fault','PRIVATE-SYNTHETIC-LABEL',1000)`); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.Store.PrepareCapacityNotification(t.Context(), 0, 256, 512, 270, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	s.Store.CapacityNotificationResult(t.Context(), id, false, "http_rejected", 1000)
	for _, source := range []string{"system", "everything"} {
		w := request(s, "GET", "http://example.invalid/api/notification-records?type="+source, "Bearer "+s.Config.AdminToken, "")
		var page struct {
			Items   []store.NotificationRecord `json:"items"`
			Summary store.NotificationSummary  `json:"summary"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 2 || page.Summary.Pending != 2 || page.Summary.Retrying != 1 {
			t.Fatal(w.Code, w.Body)
		}
		for _, r := range page.Items {
			if r.Source != "system" || r.MessageID != 0 || r.CallID != 0 {
				t.Fatal(r)
			}
		}
		if strings.Contains(w.Body.String(), "PRIVATE-SYNTHETIC") {
			t.Fatal("private label exposed")
		}
	}
}
