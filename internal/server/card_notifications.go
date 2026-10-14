package server

import (
	"database/sql"
	"errors"
	"github.com/Battery233/sim/internal/diagnostics"
	"net/http"
)

func (s *Server) cardNotificationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/cards/{id}/notifications", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled  *bool `json:"enabled"`
			Expected *bool `json:"expected"`
		}
		if !decodeAction(w, r, &in) {
			return
		}
		if in.Enabled == nil || in.Expected == nil {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		s.notificationGate.Lock()
		err := s.Store.SetCardNotifications(r.Context(), r.PathValue("id"), *in.Enabled, *in.Expected)
		s.notificationGate.Unlock()
		if errors.Is(err, sql.ErrNoRows) {
			reply(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if err != nil {
			outgoingError(w, err)
			return
		}
		s.Logs.Record("card_notifications_changed", diagnostics.Fields{})
		reply(w, 200, map[string]any{"card_id": r.PathValue("id"), "notification_enabled": *in.Enabled})
	})
}
