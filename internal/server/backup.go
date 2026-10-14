package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Battery233/sim/internal/maintenance"
	"github.com/Battery233/sim/internal/store"
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
	io.Copy(w, file)
}
