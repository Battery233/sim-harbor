package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestNotificationAPIConsentPrivacyCooldownAndNoChargeableAction(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if p["title"] != "SIM 通知连接测试" || p["url"] != "https://example.invalid" {
			t.Error("test impersonated SMS", p["title"])
		}
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	s, _ := fixture(t, 0, func(c *config.Config) {
		c.BarkKey = "SYNTHETIC-SECRET"
		c.BarkBaseURL = receiver.URL
		c.PublicURL = "https://example.invalid"
		c.AdminToken = strings.Repeat("t", 24)
	})
	auth := "Bearer " + s.Config.AdminToken
	if w := request(s, "GET", "http://example.com/api/notifications", "", ""); w.Code != 401 {
		t.Fatal("diagnostics bypassed access")
	}
	w := request(s, "GET", "http://example.com/api/notifications", auth, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), s.Config.BarkKey) {
		t.Fatal("diagnostics disclosed key", w.Code)
	}
	if w = action(s, "/notifications/test", map[string]bool{"confirmed": false}); w.Code != 400 || calls.Load() != 0 {
		t.Fatal("unconfirmed test sent")
	}
	if w = action(s, "/notifications/test", map[string]bool{"confirmed": true}); w.Code != 200 || calls.Load() != 1 {
		t.Fatal("test failed", w.Code, w.Body)
	}
	restarted := New(s.Config, s.Store)
	if w = action(restarted, "/notifications/test", map[string]bool{"confirmed": true}); w.Code != 409 || calls.Load() != 1 {
		t.Fatal("restart bypassed cooldown")
	}
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, Phone: "TEST-SENDER", Body: "PRIVATE SYNTHETIC SMS", SMSTS: 1700000000}
	p, _ := json.Marshal(e)
	if err := s.Store.Ingest(context.Background(), e, p, true); err != nil {
		t.Fatal(err)
	}
	n, err := s.Store.NextNotification(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.NotificationResultCode(context.Background(), n, false, "http_rejected"); err != nil {
		t.Fatal(err)
	}
	w = request(s, "GET", "http://example.com/api/notifications", auth, "")
	if strings.Contains(w.Body.String(), e.Body) || strings.Contains(w.Body.String(), e.Phone) || !strings.Contains(w.Body.String(), "http_rejected") {
		t.Fatal("unsafe diagnostics")
	}
	if w = action(s, "/notifications/1/retry", map[string]bool{"confirmed": true}); w.Code != 200 {
		t.Fatal("retry rejected", w.Code, w.Body)
	}
	if w = action(s, "/notifications/1/retry", map[string]bool{"confirmed": true}); w.Code != 409 {
		t.Fatal("retry duplicated")
	}
	var count int
	if err = s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&count); err != nil || count != 0 || calls.Load() != 1 {
		t.Fatal("diagnostics sent SMS or directly retried notification", err, count, calls.Load())
	}
	for _, path := range []string{"?before=-1", "?before=no", "?state=bad"} {
		if w = request(s, "GET", "http://example.com/api/notifications"+path, auth, ""); w.Code != 400 {
			t.Fatal("invalid filter accepted")
		}
	}
}

func TestNotificationPushFailureCodesAndDisabledTest(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
	}{
		{200, `{"code":200} trailing`, "invalid_response"},
		{200, strings.Repeat(" ", 65537) + `{"code":200}`, "invalid_response"},
		{200, `{"code":400,"message":"SENSITIVE-URL-KEY"}`, "bark_rejected"},
		{302, `{"code":200}`, "http_rejected"},
		{500, `{"code":200}`, "http_rejected"},
	} {
		t.Run(tc.code+string(rune(tc.status)), func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://example.invalid")
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer receiver.Close()
			s, _ := fixture(t, 0, func(c *config.Config) {
				c.BarkKey = "SYNTHETIC"
				c.BarkBaseURL = receiver.URL
				c.PublicURL = "https://example.invalid"
			})
			w := action(s, "/notifications/test", map[string]bool{"confirmed": true})
			if w.Code != 502 || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "SENSITIVE") {
				t.Fatal("failure mislabeled/leaked", w.Code, w.Body)
			}
			_, sum, err := s.Store.NotificationPage(context.Background(), "all", 0)
			if err != nil || sum.TestOutcome != tc.code {
				t.Fatal("test result not durable", sum, err)
			}
		})
	}
	s, _ := fixture(t, 0)
	if w := action(s, "/notifications/test", map[string]bool{"confirmed": true}); w.Code != 409 {
		t.Fatal("disabled test not rejected")
	}
}
