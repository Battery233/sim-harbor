package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/store"
)

type unlinkedVoicemailView struct {
	store.UnlinkedVoicemail
	FileName  string `json:"file_name"`
	FileState string `json:"file_state"`
}

func (s *Server) unlinkedVoicemailRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/voicemails/activity", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.Store.VoicemailActivity(r.Context(), s.Config.DeviceID)
		if err != nil {
			outgoingError(w, err)
			return
		}
		for i, item := range items {
			if item.RecordingAt > 0 {
				file, err := s.Store.VoicemailFile(r.Context(), item.CallID)
				if err == nil {
					f, openErr := s.openVoicemailFile(file)
					if openErr == nil {
						items[i].RecordingAvailable = true
						f.Close()
					}
				}
			}
		}
		reply(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("DELETE /api/voicemails/{media}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Confirmed bool `json:"confirmed"`
		}
		if !decodeAction(w, r, &in) {
			return
		}
		media := r.PathValue("media")
		if !in.Confirmed || !voicemailFileName.MatchString(media+".amr") {
			reply(w, 400, map[string]string{"error": "voicemail_delete_confirmation_required"})
			return
		}
		s.notificationGate.Lock()
		defer s.notificationGate.Unlock()
		// Once explicitly confirmed, a disconnected browser must not interrupt the
		// database tombstone / file cleanup sequence or cause automatic resubmits.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Second)
		defer cancel()
		err := s.Store.DeleteVoicemail(ctx, s.Config.DataDir, media)
		if errors.Is(err, sql.ErrNoRows) {
			reply(w, 404, map[string]string{"error": "voicemail_unavailable"})
			return
		}
		if errors.Is(err, store.ErrVoicemailCleanup) {
			s.Logs.Record("voicemail_delete_cleanup_failed", diagnostics.Fields{})
			reply(w, 503, map[string]string{"error": "voicemail_cleanup_pending"})
			return
		}
		if err != nil {
			reply(w, 503, map[string]string{"error": "voicemail_delete_failed"})
			return
		}
		s.Logs.Record("voicemail_deleted", diagnostics.Fields{})
		reply(w, 200, map[string]bool{"deleted": true})
	})
	mux.HandleFunc("GET /api/voicemails/storage", func(w http.ResponseWriter, r *http.Request) {
		used, files, err := s.Store.VoicemailDirectoryStats(r.Context(), s.Config.DataDir)
		if err != nil {
			reply(w, 503, map[string]string{"error": "voicemail_storage_unavailable"})
			return
		}
		reply(w, 200, map[string]any{"files": files, "bytes": used, "limit_bytes": s.Config.VoicemailStorageLimit(), "limited": s.Config.VoicemailMode() == "public"})
	})
	mux.HandleFunc("GET /api/voicemails/unlinked", func(w http.ResponseWriter, r *http.Request) {
		before, err := callCursor(r)
		if err != nil {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		items, err := s.Store.UnlinkedVoicemails(r.Context(), before)
		if err != nil {
			outgoingError(w, err)
			return
		}
		next := int64(0)
		if len(items) > 50 {
			items = items[:50]
			next = items[49].ID
		}
		views := make([]unlinkedVoicemailView, 0, len(items))
		for _, item := range items {
			view := unlinkedVoicemailView{UnlinkedVoicemail: item, FileState: "unavailable"}
			if voicemailFileName.MatchString(item.StoredName) {
				view.FileName = item.StoredName
				file, openErr := s.openVoicemailFile(store.VoicemailFile{Name: item.StoredName, Size: item.Size})
				if openErr == nil {
					view.FileState = "available"
					file.Close()
				} else if errors.Is(openErr, os.ErrNotExist) {
					view.FileState = "missing"
				}
			}
			views = append(views, view)
		}
		reply(w, 200, map[string]any{"items": views, "next_before": next})
	})
	mux.HandleFunc("GET /api/voicemails/unlinked/{id}/audio", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			reply(w, 400, map[string]string{"error": "invalid_id"})
			return
		}
		file, err := s.Store.UnlinkedVoicemailFile(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				reply(w, 404, map[string]string{"error": "voicemail_unavailable"})
			} else {
				reply(w, 503, map[string]string{"error": "database_unavailable"})
			}
			return
		}
		s.serveVoicemailFile(w, r, file, fmt.Sprintf("unlinked-voicemail-%d.amr", id))
	})
}

func (s *Server) openVoicemailFile(file store.VoicemailFile) (*os.File, error) {
	if !voicemailFileName.MatchString(file.Name) || file.Size <= 0 || file.Size > maxVoicemailBytes {
		return nil, os.ErrInvalid
	}
	path := filepath.Join(s.Config.DataDir, "voicemail", file.Name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != file.Size {
		return nil, os.ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
		f.Close()
		return nil, os.ErrInvalid
	}
	return f, nil
}
