package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestSingleSMSDoesNotCreateMultipartDiagnostics(t *testing.T) {
	yes, no := true, false
	s := &Server{Logs: diagnostics.New("info", 100, nil)}
	sess := &session{}
	for _, e := range []protocol.Event{
		{Type: 501},
		{Type: 501, Complete: &yes},
		{Type: 501, Complete: &yes, Max: 1},
	} {
		s.recordSMSIngest(t.Context(), sess, e, "sms_fragment_received", nil)
	}
	rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 10)
	if len(rows) != 0 {
		t.Fatal("plain receipts were classified as multipart")
	}
	// Incomplete reports still need diagnostics when the board omits a group.
	s.recordSMSIngest(t.Context(), sess, protocol.Event{Type: 501, Complete: &no}, "sms_fragment_received", nil)
	rows, _ = s.Logs.Snapshot(diagnostics.Filter{}, 10)
	if len(rows) != 1 || rows[0].SMSComplete == nil || *rows[0].SMSComplete {
		t.Fatal("ungrouped incomplete report lost its diagnostic")
	}
}

func TestMultipartDiagnosticsAtInfoAndRejectedTransaction(t *testing.T) {
	s, l := fixture(t, 1, func(c *config.Config) { c.LogLevel = "info" })
	conn := connect(t, l)
	event := map[string]any{"devId": "SIM-TEST", "type": 501, "slot": 2, "iccId": "PRIVATE-SIM-IDENTITY", "phNum": "PRIVATE-SENDER", "scName": "PRIVATE-NAME", "smsTs": 1700000000, "smsGroupId": "PRIVATE-GROUP", "smsMaxNo": 3, "comp": false, "smsBd": "PRIVATE-BODY", "smsBdFrag": []string{"1,PRIVATE-BODY"}}
	send(t, conn, s.codec(), event)
	stored := waitDiagnostic(t, s, "sms_fragment_stored")
	if stored.Level != "info" || stored.Outcome != "stored" || stored.MessageID < 1 || len(stored.SMSGroup) != 24 || stored.SMSTotal != 3 || stored.SMSParts != "1" || stored.SMSComplete == nil || *stored.SMSComplete {
		t.Fatal("missing persistent metadata", stored.Code)
	}
	w := request(s, "GET", "http://example.com/api/messages/1", "", "")
	var m store.Message
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil || m.PartsTotal != 3 || m.PartsReceived != 1 || m.Complete {
		t.Fatal("API progress unavailable")
	}
	event["smsMaxNo"] = 2
	event["smsBdFrag"] = []string{"2,PRIVATE-SECOND"}
	send(t, conn, s.codec(), event)
	rejected := waitDiagnostic(t, s, "sms_fragment_rejected")
	if rejected.Outcome != "total_mismatch" || rejected.SMSGroup != stored.SMSGroup || rejected.SMSParts != "2" {
		t.Fatal("failure lost correlation")
	}
	rows, _ := s.Logs.Snapshot(diagnostics.Filter{Reference: stored.SMSGroup}, 20)
	if len(rows) != 4 {
		t.Fatal("missing receive/result pairs", len(rows))
	}
	exported := request(s, "GET", "http://example.com/api/diagnostics/export", "", "")
	for _, secret := range []string{"PRIVATE-SIM-IDENTITY", "PRIVATE-SENDER", "PRIVATE-GROUP", "PRIVATE-BODY", "PRIVATE-SECOND", "PRIVATE-NAME", s.Config.AESKey, s.Config.DeviceID} {
		if strings.Contains(exported.Body.String(), secret) {
			t.Fatal("private content escaped")
		}
	}
	w = request(s, "GET", "http://example.com/api/messages/1", "", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil || m.PartsTotal != 3 || m.PartsReceived != 1 || m.Complete {
		t.Fatal("rejection damaged saved progress")
	}
	var outgoing, notifications int
	s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&outgoing)
	s.Store.DB.QueryRow(`SELECT count(*) FROM notifications`).Scan(&notifications)
	if outgoing != 0 || notifications != 0 {
		t.Fatal("partial or rejected input created external actions")
	}
	// A valid report after reconnect can finish local assembly even while the
	// board's own comp flag remains false. Diagnostics must distinguish both.
	conn = connect(t, l)
	event["smsMaxNo"] = 3
	event["smsBdFrag"] = []string{"2,PRIVATE-SECOND", "3,PRIVATE-THIRD"}
	send(t, conn, s.codec(), event)
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, _ = s.Logs.Snapshot(diagnostics.Filter{Reference: stored.SMSGroup}, 20)
		found := false
		for _, row := range rows {
			if row.Code == "sms_fragment_stored" && row.SMSStoredComplete != nil && *row.SMSStoredComplete {
				if row.SMSComplete == nil || *row.SMSComplete {
					t.Fatal("board and stored flags conflated")
				}
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion result was not recorded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	w = request(s, "GET", "http://example.com/api/messages/1", "", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil || !m.Complete || m.PartsReceived != 3 {
		t.Fatal("completed API progress unavailable")
	}
}

func TestMultipartDiagnosticsClassifiesInvalidAndConflictingParts(t *testing.T) {
	for _, tc := range []struct{ name, part, outcome string }{
		{"invalid", "0,PRIVATE", "invalid_fragment"},
		{"conflict", "1,CHANGED", "conflicting_fragment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, l := fixture(t, 0, func(c *config.Config) { c.LogLevel = "info" })
			conn := connect(t, l)
			e := map[string]any{"devId": "SIM-TEST", "type": 501, "slot": 1, "phNum": "PRIVATE-SENDER", "smsTs": 1700000000, "smsGroupId": "PRIVATE-GROUP", "smsMaxNo": 2, "comp": false, "smsBdFrag": []string{"1,ORIGINAL"}}
			send(t, conn, s.codec(), e)
			waitDiagnostic(t, s, "sms_fragment_stored")
			e["smsBdFrag"] = []string{tc.part}
			send(t, conn, s.codec(), e)
			r := waitDiagnostic(t, s, "sms_fragment_rejected")
			if r.Outcome != tc.outcome {
				t.Fatal("unclassified failure", r.Outcome)
			}
			if tc.name == "invalid" && (r.SMSInvalidParts != 1 || r.SMSParts != "") {
				t.Fatal("invalid part not bounded")
			}
		})
	}
}
