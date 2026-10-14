package server

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/protocol"
)

func TestRestoredStoreBlocksEnabledConfigBeforeAnyBoardQuery(t *testing.T) {
	s, l := fixture(t, 0, func(c *config.Config) { c.OutboundEnabled = true })
	if e := s.Store.HoldRestoredData(t.Context(), strings.Repeat("f", 64), time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	conn := connect(t, l)
	send(t, conn, s.codec(), map[string]any{"type": 100, "devId": "SIM-TEST"})
	reader := protocol.NewReader(conn)
	read(t, reader, s.codec())
	w := request(s, "GET", "http://example.com/api/state", "", "")
	var state struct {
		Enabled bool `json:"outbound_enabled"`
		Blocked bool `json:"outbound_restore_blocked"`
	}
	if json.Unmarshal(w.Body.Bytes(), &state) != nil || state.Enabled || !state.Blocked {
		t.Fatal(w.Body)
	}
	action := httptest.NewRequest("POST", "http://example.com/api/outgoing/prepare", strings.NewReader(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","slot":1,"phone":"000000","body":"synthetic"}`))
	action.Host = "127.0.0.1:8080"
	action.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, action)
	if recorder.Code != 409 || !strings.Contains(recorder.Body.String(), "outbound_restore_blocked") {
		t.Fatal(recorder.Code, recorder.Body)
	}
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err := reader.Next()
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatal("blocked preview sent stat or SMS", err)
	}
}
