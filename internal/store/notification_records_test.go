package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestNotificationRecordsBothSourcesFilterAndStableCursor(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	for i := 0; i < 60; i++ {
		e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, Phone: "SYNTHETIC-SENDER", Body: "SYNTHETIC", SMSTS: protocol.Number(1000 + i)}
		raw, _ := json.Marshal(e)
		if err := s.Ingest(ctx, e, raw, true); err != nil {
			t.Fatal(err)
		}
		call := callEvent(603, int64(1000+i))
		call.TelEndTS = call.TelStartTS + 2
		recordCall(t, s, call, true)
	}
	// Operational status alerts do not belong to this feed.
	if _, err := s.DB.Exec(`INSERT INTO status_alert_monitors(key) VALUES('board'); INSERT INTO status_alert_jobs(key,episode,kind,label,state) VALUES('board',1,'fault','SYNTHETIC','pending')`); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	before := ""
	for {
		rows, summary, err := s.NotificationRecords(ctx, "all", "all", before)
		if err != nil || summary.Pending != 120 {
			t.Fatal(summary, err)
		}
		if len(rows) == 0 {
			break
		}
		more := len(rows) > 50
		if more {
			rows = rows[:50]
		}
		for _, row := range rows {
			if seen[row.Key] {
				t.Fatal("mixed cursor duplicated a row", row.Key)
			}
			seen[row.Key] = true
			if row.Source == "call" && (row.CallID == 0 || row.MessageID != 0) {
				t.Fatal("call link mixed")
			}
		}
		if !more {
			break
		}
		before = NotificationRecordCursor(rows[49])
	}
	if len(seen) != 120 {
		t.Fatal("mixed cursor skipped rows", len(seen))
	}
	for _, source := range []string{"sms", "call"} {
		rows, summary, err := s.NotificationRecords(ctx, "pending", source, "")
		if err != nil || len(rows) != 51 || summary.Pending != 60 {
			t.Fatal(source, len(rows), summary, err)
		}
		for _, row := range rows {
			if row.Source != source {
				t.Fatal("source filter")
			}
		}
	}
	for _, value := range []string{"bad", "eyJhdCI6MCwic291cmNlIjoiYWxsIiwiaWQiOjF9"} {
		if _, _, err := s.NotificationRecords(ctx, "all", "all", value); err == nil {
			t.Fatal("bad cursor accepted")
		}
	}
}
