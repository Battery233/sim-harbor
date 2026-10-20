package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestGreetingStaysWithTheOriginalCall(t *testing.T) {
	s, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, stringsRepeat("ab"), 2) })
	board := connect(t, listener)
	send(t, board, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, s)
	board.SetDeadline(time.Now().Add(20 * time.Second))

	s.sendGate.Lock()
	start := time.Now().Unix()
	connected := start + 3
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": connected})
	waitCallState(t, s, start, "connected")
	send(t, board, s.codec(), map[string]any{"type": 603, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": connected, "telEndTs": start + 8})
	waitCallState(t, s, start, "answered")
	s.sendGate.Unlock()
	expectNoBoardCommand(t, board)
	waitGreetingOutcome(t, s, "voicemail_greeting_skipped", "call_ended")
	if greetingLogs(t, s, "voicemail_greeting", "") != 0 {
		t.Fatal("ended call still received a greeting")
	}

	s.sendGate.Lock()
	next := start + 30
	nextConnected := next + 2
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": next, "telConnectedTs": nextConnected})
	waitCallState(t, s, next, "connected")
	send(t, board, s.codec(), map[string]any{"type": 603, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": next, "telConnectedTs": nextConnected, "telEndTs": next + 6})
	waitCallState(t, s, next, "answered")
	third := next + 40
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-OTHER", "telStartTs": third, "telConnectedTs": third + 1})
	waitCallState(t, s, third, "connected")
	s.sendGate.Unlock()
	cmd := read(t, protocol.NewReader(board), s.codec())
	assertGreeting(t, cmd, s.Config.VoicemailTTS)
	send(t, board, s.codec(), map[string]any{"type": 401, "subType": 26, "code": 0, "tid": cmd["tid"]})
	expectNoBoardCommand(t, board)
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-OTHER", "telStartTs": third, "telConnectedTs": third + 1})
	expectNoBoardCommand(t, board)
	if greetingLogs(t, s, "voicemail_greeting", "") != 1 {
		t.Fatal("new call did not get exactly one greeting")
	}
	if greetingLogs(t, s, "voicemail_greeting_skipped", "call_ended") < 2 {
		t.Fatal("stale greeting was not dropped")
	}
}

func TestGreetingDroppedWhenConnectionReplacedOrUnidentifiable(t *testing.T) {
	s, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, stringsRepeat("cd"), 2) })
	board := connect(t, listener)
	send(t, board, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, s)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	s.mu.Lock()
	previous := ""
	if s.active != nil {
		previous = s.active.diagnosticID
	}
	s.mu.Unlock()
	s.sendGate.Lock()
	start := time.Now().Unix()
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": start + 2})
	waitCallState(t, s, start, "connected")
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 1, "phNum": "SYNTHETIC-OTHER", "telStartTs": start + 10, "telConnectedTs": start + 11})
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": 0, "telConnectedTs": start + 12})
	replacement := connect(t, listener)
	send(t, replacement, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActiveReplaced(t, s, previous)
	s.sendGate.Unlock()
	expectNoBoardCommand(t, replacement)
	waitGreetingOutcome(t, s, "voicemail_greeting_skipped", "device_connection_changed")
	if greetingLogs(t, s, "voicemail_greeting", "") != 0 {
		t.Fatal("replaced connection or another slot received a greeting")
	}
}

func TestVoicemailOffAnswersNoCallControl(t *testing.T) {
	s, listener := fixture(t, 1)
	board := connect(t, listener)
	send(t, board, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, s)
	start := time.Now().Unix()
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": start + 1})
	send(t, board, s.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 2, "phNum": "13800138000", "smsTs": start, "smsBd": "合成短信"})
	waitCallState(t, s, start, "connected")
	expectNoBoardCommand(t, board)
}

func stringsRepeat(part string) string {
	out := ""
	for len(out) < 32 {
		out += part
	}
	return out[:32]
}

func waitCallState(t *testing.T, s *Server, start int64, state string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got string
		err := s.Store.DB.QueryRow(`SELECT state FROM calls WHERE start_ts=?`, start).Scan(&got)
		if err == nil && got == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("call state", start, state)
}

func waitActiveReplaced(t *testing.T, s *Server, previous string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		id := ""
		if s.active != nil {
			id = s.active.diagnosticID
		}
		s.mu.Unlock()
		if id != "" && id != previous {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("connection was not replaced")
}

func waitGreetingOutcome(t *testing.T, s *Server, code, outcome string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if greetingLogs(t, s, code, outcome) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("missing greeting diagnostic", code, outcome)
}

func greetingLogs(t *testing.T, s *Server, code, outcome string) int {
	t.Helper()
	rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 200)
	n := 0
	for _, row := range rows {
		if row.Code == code && (outcome == "" || row.Outcome == outcome) {
			n++
		}
		if row.Code == "voicemail_greeting" || row.Code == "voicemail_greeting_skipped" || row.Code == "voicemail_greeting_failed" {
			encoded, _ := json.Marshal(row)
			if strings.Contains(string(encoded), "SYNTHETIC") || strings.Contains(string(encoded), "请在提示音") {
				t.Fatal("greeting diagnostic kept a caller or the greeting text")
			}
		}
	}
	return n
}
