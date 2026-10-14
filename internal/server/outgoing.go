package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/outbound"
	"github.com/Battery233/sim-harbor/internal/store"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func decodeAction(w http.ResponseWriter, r *http.Request, v any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		reply(w, 415, map[string]string{"error": "json_required"})
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		reply(w, 400, map[string]string{"error": "invalid_action"})
		return false
	}
	return true
}
func outgoingError(w http.ResponseWriter, e error) {
	var se *store.SafetyError
	switch {
	case errors.As(e, &se):
		reply(w, 409, map[string]string{"error": se.Code, "existing_id": se.ExistingID})
	case errors.Is(e, sql.ErrNoRows):
		reply(w, 404, map[string]string{"error": "not_found"})
	default:
		reply(w, 503, map[string]string{"error": "database_unavailable"})
	}
}
func (s *Server) outgoingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/outgoing/prepare", s.prepareOutgoing)
	mux.HandleFunc("POST /api/outgoing/{id}/confirm", s.confirmOutgoing)
	mux.HandleFunc("GET /api/outgoing/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !outbound.ValidID(r.PathValue("id")) {
			reply(w, 400, map[string]string{"error": "invalid_id"})
			return
		}
		o, e := s.Store.Outgoing(r.Context(), r.PathValue("id"), time.Now().Unix())
		if e != nil {
			outgoingError(w, e)
			return
		}
		reply(w, 200, o)
	})
	mux.HandleFunc("POST /api/outgoing/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !outbound.ValidID(r.PathValue("id")) {
			reply(w, 400, map[string]string{"error": "invalid_id"})
			return
		}
		if e := s.Store.CancelOutgoing(r.Context(), r.PathValue("id"), time.Now().Unix()); e != nil {
			outgoingError(w, e)
			return
		}
		o, e := s.Store.Outgoing(r.Context(), r.PathValue("id"), time.Now().Unix())
		if e != nil {
			outgoingError(w, e)
			return
		}
		reply(w, 200, o)
	})
	mux.HandleFunc("GET /api/outgoing", func(w http.ResponseWriter, r *http.Request) {
		before, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		if r.URL.Query().Get("before") == "" {
			before = 0
			err = nil
		}
		card := r.URL.Query().Get("card")
		if err != nil || before < 0 || !store.ValidCardFilter(card) {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		ms, e := s.Store.OutgoingPage(r.Context(), before, time.Now().Unix(), card)
		if e != nil {
			outgoingError(w, e)
			return
		}
		var next int64
		if len(ms) > 50 {
			ms = ms[:50]
			next = ms[49].Sequence
		}
		reply(w, 200, map[string]any{"items": ms, "next_before": next})
	})
}

// queryCard always uses the common read-only stat path. The identity is checked
// again on the captured connection immediately before the irreversible claim.
func (s *Server) queryCard(ctx context.Context, slot int) (*session, string, int, string) {
	sess, result, code, problem := s.queryDevice(ctx)
	if problem != "" {
		return nil, "", code, problem
	}
	if result.receivedAt.IsZero() || time.Since(result.receivedAt) > 5*time.Second {
		return nil, "", 409, "card_state_stale"
	}
	info := result.event.SlotInfo
	prefix := strconv.Itoa(slot)
	status, _ := info["slot"+prefix+"_sta"].(string)
	icc, _ := info["sim"+prefix+"_iccId"].(string)
	if status != "OK" {
		return nil, "", 409, "card_not_ready"
	}
	if strings.TrimSpace(icc) == "" {
		return nil, "", 409, "card_identity_required"
	}
	s.mu.Lock()
	same := s.active == sess
	s.mu.Unlock()
	if !same {
		return nil, "", 409, "device_connection_changed"
	}
	return sess, icc, 200, ""
}
func (s *Server) prepareOutgoing(w http.ResponseWriter, r *http.Request) {
	var in outbound.Input
	if !decodeAction(w, r, &in) {
		return
	}
	in, segments, e := outbound.Normalize(in, s.Config.SendMaxSegments)
	if e != nil {
		reply(w, 400, map[string]string{"error": e.Error()})
		return
	}
	// Repeated preview reads its immutable record even after disconnect/disable.
	old, e := s.Store.Outgoing(r.Context(), in.ID, time.Now().Unix())
	if e == nil {
		if old.DeviceID != s.Config.DeviceID || old.Slot != in.Slot || old.Phone != in.Phone || old.Body != in.Body {
			reply(w, 409, map[string]string{"error": "operation_conflict"})
			return
		}
		reply(w, 200, old)
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		outgoingError(w, e)
		return
	}
	if !s.allowOutbound(w, r) {
		return
	}
	_, icc, code, problem := s.queryCard(r.Context(), in.Slot)
	if problem != "" {
		reply(w, code, map[string]string{"error": problem})
		return
	}
	o, e := s.Store.PrepareOutgoing(r.Context(), s.Config.DeviceID, tid(), icc, in, segments, time.Now().Unix())
	if e != nil {
		outgoingError(w, e)
		return
	}
	o, e = s.Store.Outgoing(r.Context(), o.ID, time.Now().Unix())
	if e != nil {
		outgoingError(w, e)
		return
	}
	s.Logs.Record("sms_preview_created", diagnostics.Fields{Operation: diagnostics.Reference(o.ID), Transaction: diagnostics.Reference(o.TID), Slot: o.Slot})
	reply(w, 200, o)
}
func (s *Server) confirmOutgoing(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !outbound.ValidID(id) {
		reply(w, 400, map[string]string{"error": "invalid_id"})
		return
	}
	var in struct {
		Confirmed bool   `json:"confirmed"`
		Phone     string `json:"phone"`
		Slot      int    `json:"slot"`
	}
	if !decodeAction(w, r, &in) {
		return
	}
	o, e := s.Store.Outgoing(r.Context(), id, time.Now().Unix())
	if e != nil {
		outgoingError(w, e)
		return
	}
	if o.DeviceID != s.Config.DeviceID || !in.Confirmed || in.Phone != o.Phone || in.Slot != o.Slot {
		reply(w, 409, map[string]string{"error": "confirmation_required"})
		return
	}
	if o.DispatchedAt > 0 {
		reply(w, 200, o)
		return
	} // no device command, not even stat
	if !s.allowOutbound(w, r) {
		return
	}
	if o.Segments > s.Config.SendMaxSegments {
		reply(w, 409, map[string]string{"error": "segment_limit"})
		return
	}
	if o.State != "prepared" {
		reply(w, 409, map[string]string{"error": "preview_expired"})
		return
	}
	if !s.sendGate.TryLock() {
		reply(w, 409, map[string]string{"error": "send_in_progress"})
		return
	}
	defer s.sendGate.Unlock()
	sess, icc, code, problem := s.queryCard(r.Context(), o.Slot)
	if problem != "" {
		reply(w, code, map[string]string{"error": problem})
		return
	}
	// Serialize the session replacement check and durable claim. Replacement after
	// this point can only cause uncertainty; no new session is used to retry.
	s.mu.Lock()
	if s.active != sess {
		s.mu.Unlock()
		reply(w, 409, map[string]string{"error": "device_connection_changed"})
		return
	}
	now := time.Now()
	claimed, write, e := s.Store.ClaimOutgoing(r.Context(), id, icc, now.Unix(), store.DayLimits(now.In(s.Config.Location), int64(s.Config.SendIntervalSeconds), s.Config.SendDailyLimit))
	s.mu.Unlock()
	if e != nil {
		outgoingError(w, e)
		return
	}
	if !write {
		existing, err := s.Store.Outgoing(r.Context(), id, time.Now().Unix())
		if err != nil {
			outgoingError(w, err)
			return
		}
		reply(w, 200, existing)
		return
	}
	// One frame write only. No retry on write failure or missing response.
	fields := diagnostics.Fields{Session: sess.diagnosticID, Operation: diagnostics.Reference(id), Transaction: diagnostics.Reference(claimed.TID), Slot: claimed.Slot}
	s.Logs.Record("sms_dispatch_claimed", fields)
	written := s.write(sess, map[string]any{"cmd": "sendsms", "p1": strconv.Itoa(claimed.Slot), "p2": claimed.Phone, "p3": claimed.Body, "tid": claimed.TID}) == nil
	writeCode := "sms_write_unknown"
	if written {
		writeCode = "sms_write_completed"
	}
	s.Logs.Record(writeCode, fields)
	persist, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	if e = s.Store.MarkOutgoingWrite(persist, id, written, time.Now().Unix()); e != nil {
		s.Logs.Record("sms_result_persistence_failed", fields)
		outgoingError(w, e)
		return
	}
	o, e = s.Store.Outgoing(persist, id, time.Now().Unix())
	if e != nil {
		outgoingError(w, e)
		return
	}
	reply(w, 200, o)
}

func (s *Server) allowOutbound(w http.ResponseWriter, r *http.Request) bool {
	blocked, err := s.Store.RestoreBlocked(r.Context())
	if err != nil {
		reply(w, 503, map[string]string{"error": "database_unavailable"})
		return false
	}
	if blocked {
		reply(w, 409, map[string]string{"error": "outbound_restore_blocked"})
		return false
	}
	if !s.Config.OutboundEnabled {
		reply(w, 409, map[string]string{"error": "outbound_disabled"})
		return false
	}
	return true
}
