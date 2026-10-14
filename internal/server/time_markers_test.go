package server

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/diagnostics"
	"strings"
	"testing"
	"time"
)

func TestTimeMarkerBoundaries(t *testing.T) {
	now := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		stamp int64
		want  string
	}{{0, "event_time_missing"}, {-1, "event_time_missing"}, {10000000000, "event_time_missing"}, {now.Unix() - 43200, ""}, {now.Unix() - 43201, "event_time_old"}, {now.Unix() + 600, ""}, {now.Unix() + 601, "event_time_future"}, {now.Unix(), ""}} {
		if got := timeMarker(tc.stamp, now); got != tc.want {
			t.Fatal(tc.stamp, got, tc.want)
		}
	}
}
func TestTimeMarkersNeverRejectOldSMSOrChangeDeduplication(t *testing.T) {
	for _, tc := range []struct {
		stamp int64
		code  string
	}{{time.Now().Unix() - 86400, "event_time_old"}, {time.Now().Unix() + 3600, "event_time_future"}, {0, "event_time_missing"}} {
		s, l := fixture(t, 1, func(c *config.Config) {
			c.BarkKey = "SYNTHETIC-KEY"
			c.BarkBaseURL = "https://bark.example.invalid"
			c.PublicURL = "https://example.invalid"
		})
		c := connect(t, l)
		codec := s.codec()
		e := map[string]any{"devId": "SIM-TEST", "type": 501, "slot": 1, "iccId": "SYNTHETIC-CARD", "phNum": "TEST", "smsBd": "SYNTHETIC BODY", "smsTs": time.Now().Unix() - 86400, "msgTs": tc.stamp}
		send(t, c, codec, e)
		send(t, c, codec, e)
		row := waitDiagnostic(t, s, tc.code)
		if row.Session == "" {
			t.Fatal("missing correlation")
		}
		until := time.Now().Add(time.Second)
		for {
			var n, j int
			s.Store.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM messages").Scan(&n)
			s.Store.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM notifications").Scan(&j)
			if n == 1 && j == 1 {
				break
			}
			if time.Now().After(until) {
				t.Fatal("old SMS not ingested", n, j)
			}
			time.Sleep(5 * time.Millisecond)
		}
		rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 5000)
		p, _ := json.Marshal(rows)
		if containsPrivate(string(p), "SYNTHETIC BODY", "SYNTHETIC-CARD", s.Config.AESKey) {
			t.Fatal("private marker fields")
		}
		s.mu.Lock()
		bound := s.active != nil
		s.mu.Unlock()
		if !bound {
			t.Fatal("marker disconnected board")
		}
		c.Close()
	}
}

func containsPrivate(v string, secrets ...string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(v, secret) {
			return true
		}
	}
	return false
}
