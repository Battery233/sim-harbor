package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestRoutineReportsUpdateStateWithoutGrowingLegacyHistory(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	if _, err := s.DB.Exec(`INSERT INTO events VALUES('SYNTHETIC-LEGACY',998,1)`); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, sms())
	if _, err := s.DB.Exec(`UPDATE device SET last_seen=0 WHERE id='SIM-TEST'`); err != nil {
		t.Fatal(err)
	}
	for i := protocol.Number(1); i <= 3; i++ {
		ingest(t, s, protocol.Event{DeviceID: "SIM-TEST", Type: 998, MessageTS: i})
	}
	var heartbeatAt int64
	if err := s.DB.QueryRow(`SELECT last_seen FROM device WHERE id='SIM-TEST'`).Scan(&heartbeatAt); err != nil || heartbeatAt <= 0 {
		t.Fatal("heartbeat did not update device liveness", err)
	}
	for i := protocol.Number(1); i <= 3; i++ {
		ingest(t, s, protocol.Event{
			DeviceID: "SIM-TEST", Type: 402, SubType: 46, Code: number(0), MessageTS: i,
			SlotInfo: map[string]any{"slot1_sta": "OK", "sim1_iccId": "SYNTHETIC-CARD", "sim1_scName": "SYNTHETIC-CURRENT"},
		})
	}
	var lastSeen, statusAt, legacy int64
	var status, cardName string
	if err := s.DB.QueryRow(`SELECT last_seen,status_received_at,status FROM device WHERE id='SIM-TEST'`).Scan(&lastSeen, &statusAt, &status); err != nil || lastSeen <= 0 || statusAt <= 0 || !strings.Contains(status, "SYNTHETIC-CURRENT") {
		t.Fatal("routine reports did not update device state", err)
	}
	if err := s.DB.QueryRow(`SELECT name FROM cards WHERE iccid='SYNTHETIC-CARD'`).Scan(&cardName); err != nil || cardName != "SYNTHETIC-CURRENT" {
		t.Fatal("status did not update card information", cardName, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM events WHERE hash='SYNTHETIC-LEGACY'`).Scan(&legacy); err != nil || legacy != 1 {
		t.Fatal("legacy history changed", err)
	}
	ingest(t, s, sms())
	if count(t, s, "events") != 2 || count(t, s, "messages") != 1 || count(t, s, "notifications") != 1 {
		t.Fatal("routine history grew or business deduplication changed")
	}
}
