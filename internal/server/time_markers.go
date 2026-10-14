package server

import (
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"time"
)

// Time is diagnostic only. Old business times and clock drift never reject
// retries, terminate a session, or change ingestion/notification semantics.
func timeMarker(stamp int64, now time.Time) string {
	if stamp <= 0 || stamp > 9999999999 {
		return "event_time_missing"
	}
	delta := now.Unix() - stamp
	if delta > 12*60*60 {
		return "event_time_old"
	}
	if delta < -10*60 {
		return "event_time_future"
	}
	return ""
}
func (s *Server) recordTimeMarker(sess *session, e protocol.Event, now time.Time) {
	if code := timeMarker(int64(e.MessageTS), now); code != "" {
		s.Logs.Record(code, diagnostics.Fields{Session: sess.diagnosticID, Type: int(e.Type), SubType: int(e.SubType), Slot: int(e.Slot)})
	}
}
