package server

import (
	"sync"
	"time"
)

// One bounded bucket for the public upload endpoint. No attacker-controlled map
// of addresses or tokens is retained. Invalid credentials consume this budget too.
type voicemailIngress struct {
	mu     sync.Mutex
	last   time.Time
	tokens float64
	active int
}

func (v *voicemailIngress) admit(now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.last.IsZero() {
		v.last, v.tokens = now, 10
	}
	elapsed := now.Sub(v.last).Seconds()
	if elapsed > 0 {
		v.tokens = min(10, v.tokens+elapsed)
		v.last = now
	}
	if v.tokens < 1 || v.active >= 2 {
		return false
	}
	v.tokens--
	v.active++
	return true
}

func (v *voicemailIngress) release() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.active--
}
