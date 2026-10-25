package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Battery233/sim-harbor/internal/config"
)

func TestAuthDiscoveryOnlyExposesActiveHeaderAndKeepsOtherRoutesProtected(t *testing.T) {
	s := settingsFixture(t)
	snap, _ := s.Settings.Snapshot()
	snap.Values.AdminTokenHeader = "X-SIMHarbor-Authorization"
	if _, err := s.Settings.Save(snap.Values, config.SecretChanges{}, snap.Revision); err != nil {
		t.Fatal(err)
	}
	w := request(s, http.MethodGet, "http://example.com/api/auth", "", "")
	var data map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data) != 1 || data["admin_token_header"] != "Authorization" {
		t.Fatal("discovery exposed pending settings or unexpected fields", w.Code)
	}
	if containsPrivate(w.Body.String(), s.Config.AdminToken, s.Config.AESKey, s.Config.AESIV) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("discovery exposed credentials or was cacheable")
	}
	for _, path := range []string{"/api/settings", "/api/state", "/api/health", "/api/auth/", "/api/messages/export", "/api/backups"} {
		if w := request(s, http.MethodGet, "http://example.com"+path, "", ""); w.Code != 401 {
			t.Fatal("discovery exemption escaped its route", path, w.Code)
		}
	}
	if w := request(s, http.MethodPost, "http://example.com/api/auth", "", ""); w.Code != 401 {
		t.Fatal("non-GET discovery bypassed authentication", w.Code)
	}
	if w := request(s, http.MethodGet, "http://example.com/api/auth", "", "https://other.invalid"); w.Code != 403 {
		t.Fatal("discovery bypassed origin check", w.Code)
	}
}

func TestCustomAuthenticationHeaderCoexistsWithBasicAuthAndRejectsFallback(t *testing.T) {
	token := strings.Repeat("SYNTHETIC", 4)
	s, _ := fixture(t, 0, func(c *config.Config) {
		c.AdminToken = token
		c.AdminTokenHeader = "X-SIMHarbor-Authorization"
	})
	app := s.Handler()
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "synthetic-user" || password != "synthetic-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		app.ServeHTTP(w, r)
	})
	for _, tc := range []struct {
		name   string
		values []string
		want   int
	}{
		{"correct", []string{"Bearer " + token}, 200},
		{"missing", nil, 401},
		{"wrong", []string{"Bearer wrong"}, 401},
		{"bare", []string{token}, 401},
		{"duplicate", []string{"Bearer " + token, "Bearer " + token}, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://example.com/api/state", nil)
			r.SetBasicAuth("synthetic-user", "synthetic-password")
			for _, v := range tc.values {
				r.Header.Add("X-SIMHarbor-Authorization", v)
			}
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal("proxy/application authentication mismatch", w.Code)
			}
		})
	}
	if w := request(s, http.MethodGet, "http://example.com/api/state", "Bearer "+token, ""); w.Code != 401 {
		t.Fatal("default Authorization accepted while custom header configured", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "http://example.com/api/auth", nil)
	r.SetBasicAuth("synthetic-user", "synthetic-password")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, r)
	var metadata map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &metadata) != nil || metadata["admin_token_header"] != "X-Simharbor-Authorization" {
		t.Fatal("custom discovery behind Basic Auth failed", w.Code)
	}
}
