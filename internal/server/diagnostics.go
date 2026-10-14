package server

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Battery233/sim/internal/diagnostics"
	"github.com/Battery233/sim/internal/outbound"
)

func diagnosticFilter(r *http.Request) (diagnostics.Filter, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return diagnostics.Filter{}, false
	}
	for key, values := range q {
		if len(values) != 1 {
			return diagnostics.Filter{}, false
		}
		switch key {
		case "level", "category", "ref", "operation_id", "before":
		default:
			return diagnostics.Filter{}, false
		}
	}
	f := diagnostics.Filter{Level: q.Get("level"), Category: q.Get("category"), Reference: q.Get("ref")}
	if id := q.Get("operation_id"); id != "" {
		if f.Reference != "" || !outbound.ValidID(id) {
			return f, false
		}
		f.Reference = diagnostics.Reference(id)
	}
	if before := q.Get("before"); before != "" {
		v, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			return f, false
		}
		f.Before = v
	}
	return f, diagnostics.ValidFilter(f)
}
func (s *Server) diagnosticRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		filter, valid := diagnosticFilter(r)
		if !valid {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		rows, status := s.Logs.Snapshot(filter, 51)
		next := int64(0)
		if len(rows) > 50 {
			rows = rows[:50]
			next = rows[49].ID
		}
		reply(w, 200, map[string]any{"items": rows, "next_before": next, "status": status})
	})
	mux.HandleFunc("GET /api/diagnostics/export", func(w http.ResponseWriter, r *http.Request) {
		filter, valid := diagnosticFilter(r)
		if !valid || filter.Before != 0 {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		rows, status := s.Logs.Snapshot(filter, 5000)
		reply(w, 200, map[string]any{"format_version": 1, "application": "sim", "exported_at_ms": time.Now().UnixMilli(), "scope": "recent_diagnostic_events", "items": rows, "status": status})
	})
}
