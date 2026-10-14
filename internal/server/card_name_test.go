package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/protocol"
)

func TestTCPReceiptCapturesNameAndBarkUsesSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, eventName, card, want string
		currentStatus               bool
	}{
		{"event name", "SYNTHETIC HK", "", "SYNTHETIC HK", false},
		{"verified fallback", "", "SYNTHETIC-CARD", "STATUS NAME", true},
		{"other card", "", "OTHER-CARD", "卡 1", true},
		{"no identity", "", "", "卡 1", true},
		{"prior session", "", "SYNTHETIC-CARD", "卡 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan map[string]any, 1)
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				received <- payload
				w.Write([]byte(`{"code":200}`))
			}))
			defer receiver.Close()
			s, l := fixture(t, 1, func(c *config.Config) {
				c.BarkKey = "SYNTHETIC-KEY"
				c.BarkBaseURL = receiver.URL
				c.PublicURL = "https://example.invalid"
			})
			ctx := context.Background()
			status := map[string]any{"type": 402, "subType": 46, "code": 0, "devId": "SIM-TEST", "slotInfo": map[string]any{"slot1_sta": "OK", "sim1_iccId": "SYNTHETIC-CARD", "sim1_scName": "STATUS NAME"}}
			// Cached status from an earlier connection must not be trusted.
			raw, _ := json.Marshal(status)
			e, err := protocol.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Store.Ingest(ctx, e, raw, false); err != nil {
				t.Fatal(err)
			}
			c := connect(t, l)
			codec := s.codec()
			if tc.currentStatus {
				send(t, c, codec, status)
			}
			send(t, c, codec, map[string]any{"devId": "SIM-TEST", "type": 501, "slot": 1, "iccId": tc.card, "scName": tc.eventName, "phNum": "TEST", "smsBd": "SYNTHETIC BODY", "smsTs": 1700000000})
			deadline := time.Now().Add(3 * time.Second)
			for {
				n, lookupErr := s.Store.NextNotification(ctx)
				if lookupErr == nil {
					// Rename/change current slot before pushing. Only the stored name matters.
					if _, err = s.Store.DB.Exec(`UPDATE device SET status='{}'`); err != nil {
						t.Fatal(err)
					}
					if !s.push(ctx, n) {
						t.Fatal("synthetic push failed")
					}
					payload := <-received
					if payload["title"] != tc.want+" 收到短信" || payload["group"] != n.CardGroup || payload["body"] != "SYNTHETIC BODY" {
						t.Fatal("notification did not use receipt snapshot")
					}
					var count int
					if err = s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&count); err != nil || count != 0 {
						t.Fatal("notification created SMS send", err)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("TCP did not persist notification", lookupErr)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}
