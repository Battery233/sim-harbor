package server

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func waitDiagnostic(t *testing.T, s *Server, code string) diagnostics.Event {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 1000)
		for _, row := range rows {
			if row.Code == code {
				return row
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("missing diagnostic", code)
	return diagnostics.Event{}
}

func TestTCPFailureDiagnosticsWithoutPrivateContent(t *testing.T) {
	secret := "synthetic-private-frame-key-device-note"
	for _, tc := range []struct {
		code       string
		mode       int
		packet     []byte
		closeWrite bool
	}{
		{"tcp_parse_failed", 0, append([]byte(secret), protocol.Delimiter...), false},
		{"tcp_decode_failed", 2, append([]byte(secret), protocol.Delimiter...), false},
		{"tcp_identity_rejected", 0, append([]byte(`{"type":100,"devId":"`+secret+`"}`), protocol.Delimiter...), false},
		{"tcp_frame_invalid", 0, []byte(secret), true},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s, listener := fixture(t, tc.mode, func(c *config.Config) { c.LogLevel = "debug" })
			conn := connect(t, listener)
			if _, err := conn.Write(tc.packet); err != nil {
				t.Fatal(err)
			}
			if tc.closeWrite {
				if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
					t.Fatal(err)
				}
			}
			row := waitDiagnostic(t, s, tc.code)
			if row.Session == "" {
				t.Fatal("missing connection correlation", row)
			}
			rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 1000)
			p, _ := json.Marshal(rows)
			if strings.Contains(string(p), secret) || strings.Contains(string(p), s.Config.AESKey) {
				t.Fatal("private content in diagnostics")
			}
		})
	}
}

func TestTCPSourceAndReplacementDiagnostics(t *testing.T) {
	rejected, listener := fixture(t, 0, func(c *config.Config) { c.DeviceCIDRs = []string{"192.0.2.0/24"} })
	connect(t, listener)
	waitDiagnostic(t, rejected, "tcp_source_rejected")
	s, listener := fixture(t, 0)
	a := connect(t, listener)
	send(t, a, s.codec(), map[string]any{"type": 100, "devId": "SIM-TEST"})
	read(t, protocol.NewReader(a), s.codec())
	first := waitDiagnostic(t, s, "tcp_bound")
	b := connect(t, listener)
	send(t, b, s.codec(), map[string]any{"type": 100, "devId": "SIM-TEST"})
	read(t, protocol.NewReader(b), s.codec())
	replaced := waitDiagnostic(t, s, "tcp_session_replaced")
	if replaced.Session != first.Session {
		t.Fatal("replacement correlation lost", first, replaced)
	}
}

func TestQueryDiagnosticsCorrelateAndClassifyFailure(t *testing.T) {
	s, listener := fixture(t, 0)
	changed := new(atomic.Bool)
	_, conn := mockBoard(t, s, listener, changed)
	_, _, status, problem := s.queryDevice(context.Background())
	if status != 200 || problem != "" {
		t.Fatal(status, problem)
	}
	start := waitDiagnostic(t, s, "query_started")
	end := waitDiagnostic(t, s, "query_completed")
	if start.Transaction == "" || start.Transaction != end.Transaction || start.Session != end.Session {
		t.Fatal("query correlation lost", start, end)
	}
	conn.Close()
	waitDiagnostic(t, s, "tcp_peer_closed")
	_, _, _, problem = s.queryDevice(context.Background())
	if problem != "device_offline" {
		t.Fatal(problem)
	}
	failed := waitDiagnostic(t, s, "query_failed")
	if failed.Outcome != "device_offline" {
		t.Fatal(failed)
	}
}

func TestOutgoingDiagnosticCorrelationDoesNotReplay(t *testing.T) {
	s, listener := fixture(t, 0, func(c *config.Config) { c.OutboundEnabled = true; c.LogLevel = "debug" })
	count, _ := mockBoard(t, s, listener, new(atomic.Bool))
	id := strings.Repeat("d", 32)
	result(t, action(s, "/outgoing/prepare", map[string]any{"id": id, "slot": 1, "phone": "000", "body": "SYNTHETIC\n正文"}))
	for i := 0; i < 3; i++ {
		result(t, action(s, "/outgoing/"+id+"/confirm", map[string]any{"confirmed": true, "slot": 1, "phone": "000"}))
	}
	waitDiagnostic(t, s, "sms_report_received")
	rows, _ := s.Logs.Snapshot(diagnostics.Filter{Reference: diagnostics.Reference(id)}, 1000)
	claims, writes, reports := 0, 0, 0
	for _, row := range rows {
		switch row.Code {
		case "sms_dispatch_claimed":
			claims++
		case "sms_write_completed":
			writes++
		case "sms_report_received":
			reports++
		}
	}
	if count.Load() != 1 || claims != 1 || writes != 1 || reports < 1 {
		t.Fatal("diagnostics replayed or lost correlation", count.Load(), claims, writes, reports)
	}
	p, _ := json.Marshal(rows)
	if strings.Contains(string(p), "SYNTHETIC") || strings.Contains(string(p), "正文") || strings.Contains(string(p), id) {
		t.Fatal("private outbox content logged")
	}
}
