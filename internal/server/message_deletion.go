package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		reply(w, 400, map[string]string{"error": "invalid_id"})
		return
	}
	var action struct {
		Confirmed bool `json:"confirmed"`
	}
	if !decodeAction(w, r, &action) {
		return
	}
	if !action.Confirmed {
		reply(w, 400, map[string]string{"error": "delete_confirmation_required"})
		return
	}
	// Serialize deletion with task selection and sending, so a queued message
	// cannot start a push after the deletion has completed.
	s.notificationGate.Lock()
	err = s.Store.DeleteMessage(r.Context(), id, time.Now().Unix())
	s.notificationGate.Unlock()
	if errors.Is(err, sql.ErrNoRows) {
		reply(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if err != nil {
		reply(w, 503, map[string]string{"error": "database_unavailable"})
		return
	}
	reply(w, 200, map[string]any{"ok": true, "id": id})
}
