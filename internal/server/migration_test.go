package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const migrationAcknowledgements = `"source_stopped":true,"history_complete":true,"unknown_acknowledged":true,"backup_saved":true`

func migrationCall(s *Server, path, body, token, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://example.com/api"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func restoredServer(t *testing.T) (*Server, string) {
	t.Helper()
	token := strings.Repeat("t", 24)
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = token; c.OutboundEnabled = true })
	if err := s.Store.HoldRestoredData(t.Context(), strings.Repeat("a", 64), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	return s, token
}
func prepareMigration(t *testing.T, s *Server, token string) string {
	t.Helper()
	if w := migrationCall(s, "/backups/download", `{"confirmed":true}`, token, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w := migrationCall(s, "/migration/preview", "{"+migrationAcknowledgements+"}", token, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var p struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || len(p.Token) != 24 {
		t.Fatal(w.Body)
	}
	return p.Token
}
func TestMigrationAuthConsentBackupAndNoSMS(t *testing.T) {
	s, token := restoredServer(t)
	good := "{" + migrationAcknowledgements + "}"
	if w := migrationCall(s, "/migration/preview", good, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := migrationCall(s, "/migration/preview", good, token, "http://other.invalid"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{}`, `{"source_stopped":true}`, `{"source_stopped":true,"path":"/tmp"}`} {
		if w := migrationCall(s, "/migration/preview", body, token, ""); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := migrationCall(s, "/migration/preview", good, token, ""); w.Code != 409 || !strings.Contains(w.Body.String(), "migration_backup_required") {
		t.Fatal(w.Code, w.Body)
	}
	p := prepareMigration(t, s, token)
	body := `{"token":"` + p + `","confirmed":true,` + migrationAcknowledgements + `}`
	if w := migrationCall(s, "/migration/confirm", body, token, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := migrationCall(s, "/migration/confirm", body, token, ""); w.Code != 409 {
		t.Fatal("token replay", w.Code)
	}
	m, err := s.Store.MigrationState(t.Context())
	if err != nil || m.Blocked || m.ConfirmedAt == 0 || m.Policy != "complete_history_v1" {
		t.Fatal(m, err)
	}
	var n int
	if err = s.Store.DB.QueryRow(`SELECT (SELECT count(*) FROM outgoing)+(SELECT count(*) FROM notifications)+(SELECT count(*) FROM call_notifications)+(SELECT count(*) FROM status_alert_jobs)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("confirmation created work", n, err)
	}
	s.Config.OutboundEnabled = false
	w := request(s, "GET", "http://example.com/api/state", "Bearer "+token, "")
	var state map[string]any
	json.Unmarshal(w.Body.Bytes(), &state)
	if state["outbound_enabled"] != false || state["outbound_restore_blocked"] != false {
		t.Fatal(w.Body)
	}
}
func TestMigrationRejectsStaleHistoryExpiredPreviewNewBackupAndRestart(t *testing.T) {
	for _, change := range []string{"expired", "ledger", "new_restore", "new_backup", "restart"} {
		t.Run(change, func(t *testing.T) {
			s, token := restoredServer(t)
			p := prepareMigration(t, s, token)
			switch change {
			case "expired":
				s.migrationPreview.expires = time.Now().Add(-time.Second)
			case "ledger":
				if _, err := s.Store.DB.Exec(`INSERT INTO outgoing(id,device_id,tid,fingerprint,slot,phone,body,segments,iccid,created_at,updated_at,expires_at) VALUES('synthetic','SIM-TEST','synthetic','synthetic',1,'000','SYNTHETIC',1,'SYNTHETIC',1,1,1)`); err != nil {
					t.Fatal(err)
				}
			case "new_restore":
				if err := s.Store.HoldRestoredData(t.Context(), strings.Repeat("a", 64), time.Now().Unix()); err != nil {
					t.Fatal(err)
				}
			case "new_backup":
				migrationCall(s, "/backups/download", `{"confirmed":true}`, token, "")
			case "restart":
				s = New(s.Config, s.Store)
			}
			w := migrationCall(s, "/migration/confirm", `{"token":"`+p+`","confirmed":true,`+migrationAcknowledgements+`}`, token, "")
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body)
			}
			blocked, err := s.Store.RestoreBlocked(t.Context())
			if err != nil || !blocked {
				t.Fatal("stale action unblocked", err)
			}
		})
	}
}

func TestSettingSMSChangesRequireNewMigrationBackup(t *testing.T) {
	for _, change := range []string{"claim", "result"} {
		t.Run(change, func(t *testing.T) {
			s, token := restoredServer(t)
			claim := func() int64 {
				now := time.Now()
				id, err := s.Store.ClaimVoicemailControl(t.Context(), "SIM-TEST", 1, "SYNTHETIC-DESTINATION", "SYNTHETIC-CARD", "mode", 0, false, now.Unix(), store.DayLimits(now, 60, 5), false)
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			var id int64
			if change == "result" {
				id = claim()
			}
			p := prepareMigration(t, s, token)
			if change == "claim" {
				claim()
			} else if err := s.Store.MarkVoicemailControl(t.Context(), id, "unknown"); err != nil {
				t.Fatal(err)
			}
			body := `{"token":"` + p + `","confirmed":true,` + migrationAcknowledgements + `}`
			if w := migrationCall(s, "/migration/confirm", body, token, ""); w.Code != 409 || !strings.Contains(w.Body.String(), "migration_state_changed") {
				t.Fatal("stale control history accepted", w.Code, w.Body)
			}
			if w := migrationCall(s, "/migration/preview", "{"+migrationAcknowledgements+"}", token, ""); w.Code != 409 || !strings.Contains(w.Body.String(), "migration_backup_required") {
				t.Fatal("stale backup reused", w.Code, w.Body)
			}
			p = prepareMigration(t, s, token)
			body = `{"token":"` + p + `","confirmed":true,` + migrationAcknowledgements + `}`
			if w := migrationCall(s, "/migration/confirm", body, token, ""); w.Code != 200 {
				t.Fatal("fresh backup did not restore confirmation", w.Code, w.Body)
			}
		})
	}
}

type brokenBackupWriter struct{ header http.Header }

func (w *brokenBackupWriter) Header() http.Header { return w.header }
func (w *brokenBackupWriter) WriteHeader(int)     {}
func (w *brokenBackupWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic disconnected download")
}
func TestFailedBackupAndExpiredEvidenceDoNotPermitMigration(t *testing.T) {
	s, token := restoredServer(t)
	r := httptest.NewRequest("POST", "http://example.com/api/backups/download", bytes.NewBufferString(`{"confirmed":true}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	s.Handler().ServeHTTP(&brokenBackupWriter{header: http.Header{}}, r)
	if s.migrationBackup != nil {
		t.Fatal("failed backup evidence retained")
	}
	prepareMigration(t, s, token)
	s.migrationBackup.created = time.Now().Add(-migrationWindow)
	if w := migrationCall(s, "/migration/preview", "{"+migrationAcknowledgements+"}", token, ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	// Persistence is readable after a lost confirmation response; GET has no mutation.
	m, _ := s.Store.MigrationState(t.Context())
	if err := s.Store.ConfirmMigration(t.Context(), m.Revision, strings.Repeat("b", 64), m.LedgerRevision, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	w := request(s, "GET", "http://example.com/api/migration", "Bearer "+token, "")
	var got store.MigrationState
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Blocked || got.ConfirmedAt == 0 {
		t.Fatal(w.Code, w.Body)
	}
}
