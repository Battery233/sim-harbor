package server

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Battery233/sim-harbor/internal/recording"
	"github.com/Battery233/sim-harbor/internal/store"
)

func (s *Server) callRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/call-groups", func(w http.ResponseWriter, r *http.Request) {
		card, q := r.URL.Query().Get("card"), r.URL.Query().Get("q")
		before, err := callCursor(r)
		if err != nil || !store.ValidCardFilter(card) || len(q) > 128 {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		groups, err := s.Store.CallGroups(r.Context(), card, q, before, time.Now().Unix())
		if err != nil {
			outgoingError(w, err)
			return
		}
		next := int64(0)
		if len(groups) > 50 {
			groups = groups[:50]
			next = groups[49].Latest.ID
		}
		reply(w, 200, map[string]any{"items": groups, "next_before": next})
	})
	mux.HandleFunc("GET /api/call-groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			reply(w, 400, map[string]string{"error": "invalid_id"})
			return
		}
		group, err := s.Store.CallGroup(r.Context(), id, time.Now().Unix())
		if err != nil {
			outgoingError(w, err)
			return
		}
		reply(w, 200, group)
	})
	mux.HandleFunc("GET /api/calls", func(w http.ResponseWriter, r *http.Request) {
		card, q := r.URL.Query().Get("card"), r.URL.Query().Get("q")
		before := int64(0)
		var err error
		if value := r.URL.Query().Get("before"); value != "" {
			before, err = strconv.ParseInt(value, 10, 64)
		}
		if err != nil || before < 0 || !store.ValidCardFilter(card) || len(q) > 128 {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		var calls []store.Call
		if value := r.URL.Query().Get("group"); value != "" {
			id, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || id <= 0 || card != "" || q != "" {
				reply(w, 400, map[string]string{"error": "invalid_filter"})
				return
			}
			calls, err = s.Store.CallsInGroup(r.Context(), id, before, time.Now().Unix())
		} else {
			calls, err = s.Store.Calls(r.Context(), card, q, before, time.Now().Unix())
		}
		if err != nil {
			outgoingError(w, err)
			return
		}
		next := int64(0)
		if len(calls) > 50 {
			calls = calls[:50]
			next = calls[49].ID
		}
		reply(w, 200, map[string]any{"items": calls, "next_before": next})
	})
	mux.HandleFunc("GET /api/calls/{id}/voicemail", s.downloadVoicemail)
	mux.HandleFunc("GET /api/calls/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			reply(w, 400, map[string]string{"error": "invalid_id"})
			return
		}
		call, err := s.Store.Call(r.Context(), id, time.Now().Unix())
		if err != nil {
			outgoingError(w, err)
			return
		}
		reply(w, 200, call)
	})
}

func (s *Server) downloadVoicemail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		reply(w, 400, map[string]string{"error": "invalid_id"})
		return
	}
	file, err := s.Store.VoicemailFile(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			reply(w, 404, map[string]string{"error": "not_found"})
			return
		}
		reply(w, 503, map[string]string{"error": "database_unavailable"})
		return
	}
	s.serveVoicemailFile(w, r, file, "voicemail-"+strconv.FormatInt(id, 10)+".amr")
}

func (s *Server) serveVoicemailFile(w http.ResponseWriter, r *http.Request, file store.VoicemailFile, downloadName string) {
	if !recording.ValidFileName(file.Name) || file.Size <= 0 || file.Size > maxVoicemailBytes {
		reply(w, 404, map[string]string{"error": "not_found"})
		return
	}
	f, err := s.openVoicemailFile(file)
	if err != nil {
		reply(w, 404, map[string]string{"error": "voicemail_audio_missing"})
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "audio/amr")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+downloadName+"\"")
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}

func callCursor(r *http.Request) (int64, error) {
	value := r.URL.Query().Get("before")
	if value == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err == nil && id < 0 {
		err = strconv.ErrSyntax
	}
	return id, err
}
