package server

import (
	"context"
	"strconv"
	"time"

	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

type greetingJob struct {
	Slot      int
	Start     int64
	Connected int64
}

func (s *Server) claimVoicemailGreeting(e protocol.Event) (greetingJob, bool) {
	if !s.Config.VoicemailEnabled || s.Config.VoicemailTTS == "" || e.Type != 602 || e.TelConnectedTS == nil || *e.TelConnectedTS <= 0 || e.TelStartTS <= 0 {
		return greetingJob{}, false
	}
	slot := int(e.Slot)
	selected := false
	for _, n := range s.Config.VoicemailSlots {
		if n == slot {
			selected = true
		}
	}
	if !selected {
		return greetingJob{}, false
	}
	job := greetingJob{Slot: slot, Start: int64(e.TelStartTS), Connected: int64(*e.TelConnectedTS)}
	key := strconv.Itoa(slot) + ":" + strconv.FormatInt(job.Start, 10) + ":" + strconv.FormatInt(job.Connected, 10)
	s.greetingMu.Lock()
	defer s.greetingMu.Unlock()
	if s.greetingSent == nil {
		s.greetingSent = map[string]time.Time{}
	}
	now := time.Now()
	for saved, at := range s.greetingSent {
		if now.Sub(at) > 6*time.Hour {
			delete(s.greetingSent, saved)
		}
	}
	if _, exists := s.greetingSent[key]; exists {
		return greetingJob{}, false
	}
	s.greetingSent[key] = now
	return job, true
}

func (s *Server) playVoicemailGreeting(sess *session, job greetingJob, text string) {
	s.sendGate.Lock()
	defer s.sendGate.Unlock()
	s.query.Lock()
	defer s.query.Unlock()
	s.mu.Lock()
	same := s.active == sess
	s.mu.Unlock()
	if !same {
		s.skipVoicemailGreeting(sess, job.Slot, "device_connection_changed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reason, err := s.Store.AnsweredCallStillCurrent(ctx, s.Config.DeviceID, job.Slot, job.Start, job.Connected)
	if err != nil {
		s.skipVoicemailGreeting(sess, job.Slot, "database_unavailable")
		return
	}
	if reason != "" {
		s.skipVoicemailGreeting(sess, job.Slot, reason)
		return
	}
	if problem := s.writeVoicemailGreeting(sess, job.Slot, text); problem != "" {
		s.Logs.Record("voicemail_greeting_failed", diagnostics.Fields{Session: sess.diagnosticID, Slot: job.Slot, Outcome: problem})
		return
	}
	s.Logs.Record("voicemail_greeting", diagnostics.Fields{Session: sess.diagnosticID, Slot: job.Slot, SubType: 26})
}

func (s *Server) skipVoicemailGreeting(sess *session, slot int, outcome string) {
	s.Logs.Record("voicemail_greeting_skipped", diagnostics.Fields{Session: sess.diagnosticID, Slot: slot, Outcome: outcome})
}

func (s *Server) writeVoicemailGreeting(sess *session, slot int, text string) string {
	if slot < 1 || slot > 2 || text == "" {
		return "device_query_failed"
	}
	s.mu.Lock()
	same := s.active == sess
	s.mu.Unlock()
	if !same {
		return "device_connection_changed"
	}
	id := tid()
	ch := make(chan queryReply, 1)
	sess.mu.Lock()
	if sess.tid != "" || sess.response != nil {
		sess.mu.Unlock()
		return "device_query_failed"
	}
	sess.tid = id
	sess.response = ch
	sess.acceptTTSAck = true
	sess.mu.Unlock()
	defer func() {
		sess.mu.Lock()
		if sess.tid == id {
			sess.tid = ""
			sess.response = nil
			sess.acceptTTSAck = false
		}
		sess.mu.Unlock()
	}()
	if err := s.write(sess, map[string]any{"cmd": "telstarttts", "p1": slot, "p2": text, "p3": 1, "p5": 0, "tid": id}); err != nil {
		return "device_write_failed"
	}
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	select {
	case result := <-ch:
		s.mu.Lock()
		same = s.active == sess
		s.mu.Unlock()
		if !same {
			return "device_connection_changed"
		}
		e := result.event
		if e.Type != 401 || int(e.SubType) != 26 || e.Code == nil || *e.Code != 0 {
			return "device_query_failed"
		}
		return ""
	case <-timer.C:
		return "device_result_unknown"
	}
}
