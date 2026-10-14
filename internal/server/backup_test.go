package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/maintenance"
)

func TestBackupRequiresConsentAuthAndCleansFiles(t *testing.T) {
	token := strings.Repeat("t", 24)
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = token })
	call := func(body, auth, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://example.com/api/backups/download", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", auth)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call(`{"confirmed":true}`, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(`{"confirmed":true}`, "Bearer "+token, "http://other.invalid"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{}`, `{"confirmed":false}`, `{"confirmed":true,"path":"/tmp/foo"}`} {
		if w := call(body, "Bearer "+token, ""); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	s.backupGate.Lock()
	w := call(`{"confirmed":true}`, "Bearer "+token, "")
	s.backupGate.Unlock()
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = call(`{"confirmed":true}`, "Bearer "+token, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" || w.Header().Get("Content-Length") == "" || w.Header().Get("Cache-Control") != "no-store, no-transform" {
		t.Fatal(w.Code, w.Body)
	}
	path := filepath.Join(t.TempDir(), "backup.zip")
	if e := os.WriteFile(path, w.Body.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := maintenance.Inspect(context.Background(), path); e != nil {
		t.Fatal(e)
	}
	files, e := filepath.Glob(filepath.Join(s.Config.DataDir, ".sim-backup-*"))
	if e != nil || len(files) != 0 {
		t.Fatal("backup work files leaked", files, e)
	}
	var n int
	if e = s.Store.DB.QueryRow(`SELECT (SELECT count(*) FROM outgoing)+(SELECT count(*) FROM notifications)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("backup side effects", n, e)
	}
}
