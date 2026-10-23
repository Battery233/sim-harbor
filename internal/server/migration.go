package server

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Battery233/sim-harbor/internal/store"
)

const migrationWindow = 15 * time.Minute

type migrationBackup struct {
	revision, digest, ledger string
	created                  time.Time
	invalid                  atomic.Bool
}
type migrationPreview struct {
	token, revision, backup, ledger string
	expires                         time.Time
}
type migrationConsent struct {
	SourceStopped       bool `json:"source_stopped"`
	HistoryComplete     bool `json:"history_complete"`
	UnknownAcknowledged bool `json:"unknown_acknowledged"`
	BackupSaved         bool `json:"backup_saved"`
}

func (c migrationConsent) valid() bool {
	return c.SourceStopped && c.HistoryComplete && c.UnknownAcknowledged && c.BackupSaved
}

func (s *Server) migrationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/migration", s.migrationStatus)
	mux.HandleFunc("POST /api/migration/preview", s.previewMigration)
	mux.HandleFunc("POST /api/migration/confirm", s.confirmMigration)
}
func (s *Server) migrationStatus(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.MigrationState(r.Context())
	if err != nil {
		reply(w, 503, map[string]string{"error": "database_unavailable"})
		return
	}
	reply(w, 200, m)
}
func (s *Server) previewMigration(w http.ResponseWriter, r *http.Request) {
	var c migrationConsent
	if !decodeAction(w, r, &c) {
		return
	}
	if !c.valid() {
		reply(w, 400, map[string]string{"error": "migration_consent_required"})
		return
	}
	if !s.migrationGate.TryLock() {
		reply(w, 409, map[string]string{"error": "migration_in_progress"})
		return
	}
	defer s.migrationGate.Unlock()
	m, err := s.Store.MigrationState(r.Context())
	if err != nil {
		reply(w, 503, map[string]string{"error": "database_unavailable"})
		return
	}
	if !m.Blocked {
		reply(w, 409, map[string]string{"error": "migration_state_changed"})
		return
	}
	now := time.Now()
	b := s.migrationBackup
	if b == nil || b.invalid.Load() || b.revision != m.Revision || b.ledger != m.LedgerRevision || now.Before(b.created) || now.Sub(b.created) >= migrationWindow {
		reply(w, 409, map[string]string{"error": "migration_backup_required"})
		return
	}
	// One outstanding preview per application: a second preview retires the first.
	p := &migrationPreview{token: tid(), revision: m.Revision, backup: b.digest, ledger: m.LedgerRevision, expires: b.created.Add(migrationWindow)}
	s.migrationPreview = p
	reply(w, 200, map[string]any{"token": p.token, "expires_at": p.expires.Unix(), "migration": m, "backup_sha256": p.backup})
}
func (s *Server) confirmMigration(w http.ResponseWriter, r *http.Request) {
	var action struct {
		migrationConsent
		Token     string `json:"token"`
		Confirmed bool   `json:"confirmed"`
	}
	if !decodeAction(w, r, &action) {
		return
	}
	if !action.valid() || !action.Confirmed {
		reply(w, 400, map[string]string{"error": "migration_consent_required"})
		return
	}
	if !s.migrationGate.TryLock() {
		reply(w, 409, map[string]string{"error": "migration_in_progress"})
		return
	}
	defer s.migrationGate.Unlock()
	p := s.migrationPreview
	now := time.Now()
	if p == nil || action.Token != p.token || !now.Before(p.expires) {
		reply(w, 409, map[string]string{"error": "migration_preview_expired"})
		return
	}
	if s.migrationBackup == nil || s.migrationBackup.invalid.Load() {
		s.migrationPreview = nil
		reply(w, 409, map[string]string{"error": "migration_backup_required"})
		return
	}
	// Consume before writing. Any failure or lost response requires a GET and a
	// new explicit preview, never an automatic retry of the confirmation POST.
	s.migrationPreview = nil
	if err := s.Store.ConfirmMigration(r.Context(), p.revision, p.backup, p.ledger, now.Unix()); err != nil {
		code := "database_unavailable"
		status := 503
		if _, ok := err.(*store.SafetyError); ok {
			code = "migration_state_changed"
			status = 409
		}
		reply(w, status, map[string]string{"error": code})
		return
	}
	s.migrationBackup = nil
	s.migrationStatus(w, r)
}
