package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Battery233/sim/internal/config"
)

func TestCredentialFreeHostBoundaryAndAuthenticatedDomain(t *testing.T) {
	s, _ := fixture(t, 0)
	for _, host := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		r := httptest.NewRequest("GET", "/api/health", nil)
		r.Host = host
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("loopback host refused", host, w.Code)
		}
	}
	for _, path := range []string{"/", "/api/state", "/api/messages/export", "/api/state/query"} {
		r := httptest.NewRequest("GET", path, nil)
		if path == "/api/state/query" {
			r.Method = "POST"
		}
		r.Host = "synthetic-rebinding.invalid:8080"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "host_rejected") {
			t.Fatal("domain accessed credential-free service", path, w.Code)
		}
	}
	token := strings.Repeat("t", 24)
	authenticated, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = token })
	if w := request(authenticated, "GET", "https://management.invalid/api/health", "Bearer "+token, "https://management.invalid"); w.Code != 200 {
		t.Fatal("authenticated domain failed", w.Code)
	}
}

func TestInboxExportRefusesConcurrentGenerationAndReleasesGate(t *testing.T) {
	s, _ := fixture(t, 0)
	s.exportGate.Lock()
	w := request(s, "GET", "/api/messages/export", "", "")
	s.exportGate.Unlock()
	if w.Code != 409 || !strings.Contains(w.Body.String(), "export_in_progress") || w.Header().Get("Content-Disposition") != "" {
		t.Fatal("concurrent export accepted", w.Code, w.Body)
	}
	if w = request(s, "GET", "/api/messages/export", "", ""); w.Code != 200 {
		t.Fatal("gate did not release", w.Code)
	}
}
