package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Battery233/sim-harbor/internal/maintenance"
	"github.com/Battery233/sim-harbor/internal/store"
)

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	var action struct {
		Confirmed bool `json:"confirmed"`
	}
	if !decodeAction(w, r, &action) {
		return
	}
	if !action.Confirmed {
		reply(w, 400, map[string]string{"error": "backup_confirmation_required"})
		return
	}
	if !s.backupGate.TryLock() {
		reply(w, 409, map[string]string{"error": "backup_in_progress"})
		return
	}
	defer s.backupGate.Unlock()
	if !s.migrationGate.TryLock() {
		reply(w, 409, map[string]string{"error": "backup_in_progress"})
		return
	}
	defer s.migrationGate.Unlock()
	s.migrationBackup = nil
	s.migrationPreview = nil
	migration, e := s.Store.MigrationState(r.Context())
	if e != nil {
		reply(w, 503, map[string]string{"error": "backup_failed"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	a, e := maintenance.Create(ctx, s.Store, s.Config.DataDir)
	if e != nil {
		code := "backup_failed"
		status := 503
		if errors.Is(e, store.ErrBackupLimit) {
			code = "backup_limit_exceeded"
			status = 413
		}
		reply(w, status, map[string]string{"error": code})
		return
	}
	defer a.Close()
	file, e := os.Open(a.Path)
	if e != nil {
		reply(w, 503, map[string]string{"error": "backup_failed"})
		return
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		reply(w, 503, map[string]string{"error": "backup_failed"})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("Content-Disposition", `attachment; filename="sim-backup.zip"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	digest := sha256.New()
	n, copyErr := io.Copy(w, io.TeeReader(file, digest))
	after, afterErr := s.Store.MigrationState(r.Context())
	if copyErr == nil && n == info.Size() && migration.Blocked && afterErr == nil && after.Revision == migration.Revision && after.LedgerRevision == migration.LedgerRevision {
		s.migrationBackup = &migrationBackup{revision: migration.Revision, digest: hex.EncodeToString(digest.Sum(nil)), ledger: migration.LedgerRevision, created: time.Now()}
	}
}
