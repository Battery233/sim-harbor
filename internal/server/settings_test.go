package server

import (
	"bytes"
	"encoding/json"
	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/diagnostics"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func settingsFixture(t *testing.T) *Server {
	s, _ := fixture(t, 1, func(c *config.Config) { c.AdminToken = strings.Repeat("T", 32); c.StaticDir = "web/dist" })
	var err error
	s.Settings, err = config.OpenSettings(s.Config)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func settingsRequest(s *Server, method, path string, input any, auth bool) *httptest.ResponseRecorder {
	var p []byte
	if input != nil {
		p, _ = json.Marshal(input)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(p))
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+s.Config.AdminToken)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}
func TestSettingsAPIReadSaveRedactionAndRevision(t *testing.T) {
	s := settingsFixture(t)
	if w := settingsRequest(s, "GET", "/api/settings", nil, false); w.Code != 401 {
		t.Fatal("unauthenticated settings", w.Code)
	}
	w := settingsRequest(s, "GET", "/api/settings", nil, true)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var snap config.SettingsSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if containsPrivate(w.Body.String(), s.Config.AdminToken, s.Config.AESKey, s.Config.AESIV) {
		t.Fatal("read exposed secrets")
	}
	snap.Values.LogLevel = "debug"
	body := map[string]any{"values": snap.Values, "secrets": map[string]any{"admin_token": strings.Repeat("NEW", 12)}, "revision": snap.Revision, "confirmed": true}
	w = settingsRequest(s, "PUT", "/api/settings", body, true)
	if w.Code != 200 || containsPrivate(w.Body.String(), strings.Repeat("NEW", 12)) {
		t.Fatal("save failed or leaked", w.Code)
	}
	var saved config.SettingsSnapshot
	json.Unmarshal(w.Body.Bytes(), &saved)
	if !saved.Pending || s.Config.LogLevel == "debug" {
		t.Fatal("runtime unexpectedly mutated")
	}
	if w = settingsRequest(s, "PUT", "/api/settings", body, true); w.Code != 409 {
		t.Fatal("stale duplicate accepted", w.Code)
	}
	rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 5000)
	p, _ := json.Marshal(rows)
	if containsPrivate(string(p), s.Config.AdminToken, strings.Repeat("NEW", 12)) {
		t.Fatal("logged credential")
	}
}
func TestSettingsAPIValidationConfirmationAndNoRestartOnSave(t *testing.T) {
	s := settingsFixture(t)
	snap, _ := s.Settings.Snapshot()
	var restarts atomic.Int32
	s.Restart = func() { restarts.Add(1) }
	for _, body := range []any{
		map[string]any{"values": snap.Values, "revision": snap.Revision},
		map[string]any{"values": snap.Values, "revision": snap.Revision, "confirmed": true, "unknown": true},
		map[string]any{"values": snap.Values, "revision": snap.Revision, "confirmed": true, "secrets": map[string]string{"aes_key": "short"}},
	} {
		if w := settingsRequest(s, "PUT", "/api/settings", body, true); w.Code < 400 {
			t.Fatal("unsafe request accepted", w.Code)
		}
	}
	if restarts.Load() != 0 {
		t.Fatal("save restarted service")
	}
	latest, _ := s.Settings.Snapshot()
	if latest.Revision != snap.Revision {
		t.Fatal("invalid request changed settings")
	}
	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"confirmed":true}`))
	req.Header.Set("Authorization", "Bearer "+s.Config.AdminToken)
	req.Header.Set("Origin", "https://other.invalid")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("foreign origin", w.Code)
	}
}
func TestSettingsRestartOnlyConfirmedLatestRevisionOnce(t *testing.T) {
	s := settingsFixture(t)
	snap, _ := s.Settings.Snapshot()
	var n atomic.Int32
	s.Restart = func() { n.Add(1) }
	if w := settingsRequest(s, "POST", "/api/settings/restart", map[string]any{"revision": snap.Revision}, true); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := settingsRequest(s, "POST", "/api/settings/restart", map[string]any{"revision": "old", "confirmed": true}, true); w.Code != 409 {
		t.Fatal(w.Code)
	}
	body := map[string]any{"revision": snap.Revision, "confirmed": true}
	if w := settingsRequest(s, "POST", "/api/settings/restart", body, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := settingsRequest(s, "POST", "/api/settings/restart", body, true); w.Code != 409 || n.Load() != 1 {
		t.Fatal("repeated restart", w.Code, n.Load())
	}
	if w := settingsRequest(s, "PUT", "/api/settings", map[string]any{"values": snap.Values, "revision": snap.Revision, "confirmed": true}, true); w.Code != 409 {
		t.Fatal("save during restart", w.Code)
	}
}
func TestLoopbackHealthIndependentOfPendingTokenAndRemoteStillProtected(t *testing.T) {
	s := settingsFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal("local health tied to pending token", w.Code)
	}
	req.RemoteAddr = "203.0.113.9:12345"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("remote health bypassed auth", w.Code)
	}
}
