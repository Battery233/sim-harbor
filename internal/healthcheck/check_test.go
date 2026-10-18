package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckRequiresHealthResponseAndNeverFollowsRedirect(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		ok     bool
	}{
		{200, `{"status":"ok"}`, true},
		{401, `{"status":"ok"}`, false},
		{503, `{"status":"degraded"}`, false},
		{302, `{"status":"ok"}`, false},
		{200, `{"status":"ok"} trailing`, false},
		{200, `<html>login</html>`, false},
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != "/api/health" || r.Header.Get("Authorization") != "Bearer SYNTHETIC" {
				t.Error("wrong health request")
			}
			w.Header().Set("Location", "https://example.invalid")
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		address := strings.Replace(strings.TrimPrefix(s.URL, "http://"), "127.0.0.1:", "0.0.0.0:", 1)
		err := Check(ctx, address, "SYNTHETIC", "")
		cancel()
		s.Close()
		if (err == nil) != tc.ok {
			t.Fatal(tc.status, err)
		}
	}
	if err := Check(context.Background(), "invalid", "SYNTHETIC", ""); err == nil {
		t.Fatal("invalid address accepted")
	}
}

func TestCheckUsesCustomAuthenticationHeader(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-SIMHarbor-Authorization") != "Bearer SYNTHETIC" {
			t.Error("health check did not use only the configured header")
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer s.Close()
	address := strings.TrimPrefix(s.URL, "http://")
	if err := Check(context.Background(), address, "SYNTHETIC", "X-SIMHarbor-Authorization"); err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), address, "SYNTHETIC", "Cookie"); err == nil {
		t.Fatal("health check accepted a reserved authentication header")
	}
}
