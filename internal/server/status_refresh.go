package server

import (
	"context"
	"time"

	"github.com/Battery233/sim/internal/protocol"
)

// These reports signal a possible change, not a complete replacement for stat.
// A delayed/replayed event is also treated conservatively and rechecked.
func invalidatesStatus(eventType protocol.Number) bool {
	switch eventType {
	case 202, 203, 204, 205, 209, 301:
		return true
	}
	return false
}

func (s *Server) statusRefreshInterval() time.Duration {
	seconds := 120
	if s.Config.StaleSeconds > 0 && s.Config.StaleSeconds/2 < seconds {
		seconds = max(1, s.Config.StaleSeconds/2)
	}
	return time.Duration(seconds) * time.Second
}

// Reserve an attempt before querying so failed/busy queries cannot cause a
// tight retry loop. Manual and outgoing queries share queryDevice's gate.
func (s *Server) reserveStatusRefresh(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.active
	if sess == nil || sess.lastSeen.IsZero() || now.Before(sess.lastSeen) || now.Sub(sess.lastSeen) > time.Duration(s.Config.StaleSeconds)*time.Second {
		return false
	}
	interval := s.statusRefreshInterval()
	if sess.hasStatus && !sess.statusAt.IsZero() && now.Sub(sess.statusAt) < interval {
		return false
	}
	retry := min(60*time.Second, interval/2)
	if !sess.autoAttempt.IsZero() && now.Sub(sess.autoAttempt) < retry {
		return false
	}
	sess.autoAttempt = now
	return true
}

// Runs even with no browser open, through the same read-only TCP query used by
// the panel. Heartbeats keep the connection alive but never renew the snapshot.
func (s *Server) RunStatusRefresh(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if s.reserveStatusRefresh(now) {
				s.queryDevice(ctx)
			}
		}
	}
}
