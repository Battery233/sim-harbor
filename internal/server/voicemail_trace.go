package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func callReference(key string) string {
	if key == "" {
		return ""
	}
	return diagnostics.Reference("call:" + key)
}

func (s *Server) recordCallProgress(ctx context.Context, sess *session, e protocol.Event, raw []byte) {
	f := diagnostics.Fields{Session: sess.diagnosticID, Slot: int(e.Slot), Type: int(e.Type)}
	if protocol.IsIncomingCall(e.Type) && (e.TelDir == nil || *e.TelDir == 1) {
		f.CallReference = callReference(store.CallEventKey(e, raw))
		s.Logs.Record("call_report", f)
		return
	}
	code := ""
	switch e.Type {
	case 683, 685, 687, 689:
		code = "voicemail_playback_report"
	case 695:
		code = "voicemail_upload_report"
	case 696:
		code = "voicemail_upload_failed"
	default:
		return
	}
	lookup, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	f.CallReference = callReference(s.Store.AudioReportCallKey(lookup, e))
	s.Logs.Record(code, f)
}

func (s *Server) recordRecordingStored(ctx context.Context, media string, slot int, duplicate bool) {
	lookup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 50*time.Millisecond)
	defer cancel()
	key, state := s.Store.RecordingTraceKeys(lookup, media)
	f := diagnostics.Fields{CallReference: callReference(key), MediaReference: diagnostics.Reference(media), Slot: slot, Outcome: state}
	if duplicate && state != "deleted" {
		f.Outcome = "duplicate"
	}
	s.Logs.Record("voicemail_stored", f)
}

// Bounded, fixed reason set: no caller-controlled URL, address or number is kept.
type voicemailRejections struct {
	mu      sync.Mutex
	last    map[string]time.Time
	skipped map[string]int
}

func (s *Server) recordVoicemailRejection(reason string) {
	switch reason {
	case "source_rejected", "token_rejected", "upload_busy", "media_rejected", "capacity_bytes", "capacity_files", "capacity_records", "reception_disabled", "store_failed":
	default:
		reason = "store_failed"
	}
	v := &s.voicemailRejects
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.last == nil {
		v.last, v.skipped = map[string]time.Time{}, map[string]int{}
	}
	if time.Since(v.last[reason]) < 30*time.Second {
		v.skipped[reason] = min(v.skipped[reason]+1, 1000000)
		return
	}
	code := "voicemail_rejected"
	if strings.HasPrefix(reason, "capacity_") {
		code = "voicemail_capacity"
	}
	s.Logs.Record(code, diagnostics.Fields{Outcome: reason, Count: v.skipped[reason] + 1})
	v.last[reason], v.skipped[reason] = time.Now(), 0
}

func (s *Server) recordingTraceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/voicemails/trace/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			reply(w, 400, map[string]string{"error": "invalid_id"})
			return
		}
		trace, err := s.Store.CallTrace(r.Context(), s.Config.DeviceID, id)
		if errors.Is(err, sql.ErrNoRows) {
			reply(w, 404, map[string]string{"error": "call_not_found"})
			return
		}
		if err != nil {
			outgoingError(w, err)
			return
		}
		if trace.Recording == "stored" {
			trace.Recording = "missing"
			if file, err := s.Store.VoicemailFile(r.Context(), id); err == nil {
				if f, err := s.openVoicemailFile(file); err == nil {
					trace.Recording = "available"
					f.Close()
				}
			}
		}
		events, status := s.Logs.Snapshot(diagnostics.Filter{Reference: callReference(trace.Key)}, 100)
		if trace.MediaID != "" {
			media, _ := s.Logs.Snapshot(diagnostics.Filter{Reference: diagnostics.Reference(trace.MediaID)}, 100)
			seen := map[int64]bool{}
			for _, e := range events {
				seen[e.ID] = true
			}
			for _, e := range media {
				if !seen[e.ID] {
					events = append(events, e)
				}
			}
		}
		sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
		reply(w, 200, map[string]any{"call": trace, "events": events, "diagnostics": status})
	})
}
