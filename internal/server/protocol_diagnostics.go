package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"time"

	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func readFailure(err error) string {
	if errors.Is(err, io.EOF) {
		return "tcp_peer_closed"
	}
	var failure net.Error
	if errors.As(err, &failure) {
		if failure.Timeout() {
			return "tcp_idle_timeout"
		}
		return "tcp_read_failed"
	}
	return "tcp_frame_invalid"
}

func (s *Server) recordProtocolEvent(ctx context.Context, sess *session, e protocol.Event, raw []byte) {
	f := diagnostics.Fields{Session: sess.diagnosticID, Transaction: diagnostics.Reference(e.TID), Type: int(e.Type), SubType: int(e.SubType), Slot: int(e.Slot)}
	s.Logs.Record("event_received", f)
	if e.Type != 401 && e.Type != 402 && e.Type != 502 && e.Type != 503 {
		return
	}
	// This read-only lookup is diagnostic only: its failure never changes matching,
	// a stored SMS outcome, command delivery or retry behaviour.
	h := sha256.Sum256(raw)
	lookup, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	id, slot, err := s.Store.DiagnosticOperation(lookup, e.TID, hex.EncodeToString(h[:]))
	if err == nil {
		f.Operation, f.Slot = diagnostics.Reference(id), slot
	}
	code := "command_response"
	if (e.Type == 401 || e.Type == 402) && e.Code != nil {
		f.Outcome = "reported_success"
		if *e.Code != 0 {
			f.Outcome = "reported_failure"
		}
	}
	if e.Type == 502 || e.Type == 503 {
		code = "sms_report_received"
	}
	s.Logs.Record(code, f)
}
