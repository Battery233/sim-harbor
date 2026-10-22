package server

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/Battery233/sim-harbor/internal/store"
)

func (s *Server) notificationRecordRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/notification-records", func(w http.ResponseWriter, r *http.Request) {
		state, source := r.URL.Query().Get("state"), r.URL.Query().Get("type")
		if state == "" {
			state = "all"
		}
		if source == "" {
			source = "all"
		}
		rows, summary, err := s.Store.NotificationRecords(r.Context(), state, source, r.URL.Query().Get("before"))
		if err != nil {
			var problem *store.SafetyError
			if errors.As(err, &problem) && problem.Code == "invalid_filter" {
				reply(w, 400, map[string]string{"error": "invalid_filter"})
				return
			}
			outgoingError(w, err)
			return
		}
		next := ""
		if len(rows) > 50 {
			rows = rows[:50]
			next = store.NotificationRecordCursor(rows[49])
		}
		bark, _ := url.Parse(s.Config.BarkBaseURL)
		reply(w, 200, map[string]any{"items": rows, "next_before": next, "summary": summary, "enabled": s.Config.BarkKey != "", "bark_host": bark.Hostname(), "public_url": s.Config.PublicURL})
	})
}
