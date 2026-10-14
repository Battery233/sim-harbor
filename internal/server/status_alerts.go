package server

import (
	"context"
	"github.com/Battery233/sim/internal/store"
	"strconv"
	"time"
)

func (s *Server) observeStatusAlerts(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	sess := s.active
	online := sess != nil && !sess.lastSeen.IsZero() && now.Sub(sess.lastSeen) >= 0 && now.Sub(sess.lastSeen) <= time.Duration(s.Config.StaleSeconds)*time.Second
	current := sess != nil && sess.hasStatus
	s.mu.Unlock()
	observations := []store.AlertObservation{{Key: "board", Healthy: &online, Label: "板子"}}
	state, err := s.Store.State(ctx, s.Config.DeviceID)
	if err != nil {
		return err
	}
	at, _ := state["status_received_at"].(int64)
	reported, _ := state["reported"].(map[string]any)
	info, _ := reported["slotInfo"].(map[string]any)
	fresh := online && current && at > 0 && now.Unix() >= at && now.Unix()-at <= int64(s.Config.StaleSeconds)
	for slot := 1; slot <= 2; slot++ {
		key := strconv.Itoa(slot)
		o := store.AlertObservation{Key: "sim" + key, Label: fmtSlot(slot)}
		if fresh {
			status, _ := info["slot"+key+"_sta"].(string)
			if status != "" {
				good := status == "OK"
				o.Healthy = &good
			}
			if name, ok := info["sim"+key+"_scName"].(string); ok && name != "" {
				o.Label = name
			}
		}
		observations = append(observations, o)
	}
	return s.Store.ObserveAlerts(ctx, observations, now.Unix())
}
