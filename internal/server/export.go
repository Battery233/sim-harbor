package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Battery233/sim/internal/store"
)

type limitedExport struct{ bytes.Buffer }

func (b *limitedExport) Write(p []byte) (int, error) {
	if b.Len()+len(p) > store.ExportBytes {
		return 0, store.ErrExportLimit
	}
	return b.Buffer.Write(p)
}
func (b *limitedExport) WriteString(value string) (int, error) {
	if b.Len()+len(value) > store.ExportBytes {
		return 0, store.ErrExportLimit
	}
	return b.Buffer.WriteString(value)
}
func exportFilter(r *http.Request) (store.MessageFilter, string, bool) {
	f := store.MessageFilter{}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return f, "", false
	}
	for key, v := range values {
		if len(v) != 1 {
			return f, "", false
		}
		switch key {
		case "card", "slot", "phone", "from", "until", "format":
		default:
			return f, "", false
		}
	}
	format := values.Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		return f, "", false
	}
	f.Card = values.Get("card")
	f.Phone = values.Get("phone")
	if value := values.Get("slot"); value != "" {
		var e error
		f.Slot, e = strconv.Atoi(value)
		if e != nil {
			return f, "", false
		}
	}
	for key, target := range map[string]*int64{"from": &f.From, "until": &f.Until} {
		if v := values.Get(key); v != "" {
			n, e := strconv.ParseInt(v, 10, 64)
			if e != nil {
				return f, "", false
			}
			*target = n
		}
	}
	return f, format, f.Valid()
}

// Spreadsheet text may begin with whitespace/control characters before a
// formula. JSON remains the exact-text interchange format.
func spreadsheetText(s string) string {
	trimmed := strings.TrimLeftFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}
func (s *Server) exportMessages(w http.ResponseWriter, r *http.Request) {
	f, format, valid := exportFilter(r)
	if !valid {
		reply(w, 400, map[string]string{"error": "invalid_filter"})
		return
	}
	if !s.exportGate.TryLock() {
		reply(w, 409, map[string]string{"error": "export_in_progress"})
		return
	}
	defer s.exportGate.Unlock()
	items, err := s.Store.ExportMessages(r.Context(), f)
	out := &limitedExport{}
	if err == nil {
		if format == "json" {
			err = json.NewEncoder(out).Encode(map[string]any{"format_version": 2, "scope": "inbox_messages", "exported_at": time.Now().UTC().Format(time.RFC3339), "filters": f, "count": len(items), "items": items})
		} else {
			c := csv.NewWriter(out)
			c.UseCRLF = true
			err = c.Write([]string{"id", "slot", "phone", "body", "timestamp_unix", "complete", "read", "card_id", "card_name"})
			for _, m := range items {
				if err != nil {
					break
				}
				err = c.Write([]string{strconv.FormatInt(m.ID, 10), strconv.Itoa(m.Slot), spreadsheetText(m.Phone), spreadsheetText(m.Body), strconv.FormatInt(m.Timestamp, 10), strconv.FormatBool(m.Complete), strconv.FormatBool(m.Read), m.CardID, spreadsheetText(m.CardName)})
			}
			c.Flush()
			if err == nil {
				err = c.Error()
			}
		}
	}
	if err != nil {
		if errors.Is(err, store.ErrExportLimit) {
			reply(w, 413, map[string]string{"error": "export_limit_exceeded"})
		} else {
			reply(w, 503, map[string]string{"error": "database_unavailable"})
		}
		return
	}
	media := "application/json"
	if format == "csv" {
		media = "text/csv"
	}
	w.Header().Set("Content-Type", media+"; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("Content-Disposition", `attachment; filename="sim-inbox.`+format+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(out.Len()))
	w.Write(out.Bytes())
}
