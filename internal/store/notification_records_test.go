package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestSystemPushRecordsFiltersPaginationCountersAndRestore(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := t.Context()
	for i := 0; i < 60; i++ {
		if _, err := s.DB.Exec(`INSERT OR IGNORE INTO status_alert_monitors(key) VALUES('board');INSERT INTO status_alert_jobs(key,episode,kind,state,attempts,created_at) VALUES('board',?,'fault','pending',1,1000)`, i); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`INSERT INTO system_notifications(kind,state,created_at,used_bytes,limit_bytes,target_bytes,accepted_at) VALUES('voicemail_capacity','sent',1000,256,512,256,1001)`); err != nil {
			t.Fatal(err)
		}
	}
	// Four independently numbered streams can share both ID and time.
	e := sms()
	e.SMSTS = 1000
	ingest(t, s, e)
	call := callEvent(603, 1000)
	call.TelEndTS = 1002
	recordCall(t, s, call, true)
	seen := map[string]bool{}
	before := ""
	for {
		rows, summary, err := s.NotificationRecords(ctx, "all", "everything", before)
		if err != nil || summary.Pending != 62 || summary.Retrying != 60 || summary.Accepted != 60 {
			t.Fatal(summary, err)
		}
		more := len(rows) > 50
		if more {
			rows = rows[:50]
		}
		for _, r := range rows {
			if seen[r.Key] {
				t.Fatal("repeated mixed page", r.Key)
			}
			seen[r.Key] = true
		}
		if !more {
			break
		}
		before = NotificationRecordCursor(rows[49])
	}
	if len(seen) != 122 {
		t.Fatal("missing stream", len(seen))
	}
	rows, sum, err := s.NotificationRecords(ctx, "all", "system", "")
	if err != nil || len(rows) != 51 || sum.Pending != 60 || sum.Accepted != 60 {
		t.Fatal(sum, err)
	}
	for _, r := range rows {
		if r.Source != "system" || r.CallID != 0 || r.MessageID != 0 {
			t.Fatal(r)
		}
	}
	rows, sum, err = s.NotificationRecords(ctx, "all", "all", "")
	if err != nil || len(rows) != 2 || sum.Pending != 2 {
		t.Fatal("legacy business filter changed", sum, err)
	}
	state, err := s.State(ctx, "SIM-TEST")
	if err != nil || state["notifications_pending"] != 62 || state["notifications_retrying"] != 60 {
		t.Fatal(state, err)
	}
	stats, err := s.NotificationSuccess(ctx, time.Unix(1002, 0), time.UTC)
	if err != nil || stats.Today != 60 {
		t.Fatal(stats, err)
	}
	if err = s.HoldRestoredData(ctx, strings.Repeat("a", 64), 2000); err != nil {
		t.Fatal(err)
	}
	_, sum, err = s.NotificationRecords(ctx, "restore_held", "system", "")
	if err != nil || sum.Held != 60 || sum.Pending != 0 {
		t.Fatal("system restore hold", sum, err)
	}
}

func TestCapacityTaskCoalescesAndAcceptedCheckpointRecovery(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := t.Context()
	id, accepted, err := s.PrepareCapacityNotification(ctx, 0, 256, 512, 270, 0, 1000)
	if err != nil || accepted {
		t.Fatal(id, accepted, err)
	}
	s.ReserveCapacityNotification(ctx, id, 1030)
	s.CapacityNotificationResult(ctx, id, false, "http_rejected", 1000)
	again, _, err := s.PrepareCapacityNotification(ctx, id, 288, 512, 290, 1030, 1020)
	if err != nil || again != id {
		t.Fatal("retry duplicated task", again, err)
	}
	rows, sum, err := s.NotificationRecords(ctx, "pending", "system", "")
	if err != nil || len(rows) != 1 || rows[0].RecordedAt != 1000 || rows[0].UsedBytes != 290 || sum.Retrying != 1 {
		t.Fatal(rows, sum, err)
	}
	s.CapacityNotificationResult(ctx, id, true, "accepted", 1030)
	recovered, accepted, err := s.PrepareCapacityNotification(ctx, id, 288, 512, 290, 0, 1040)
	if err != nil || !accepted || recovered != id {
		t.Fatal("accepted task replayed", accepted, err)
	}
	next, accepted, err := s.PrepareCapacityNotification(ctx, id, 320, 512, 320, 0, 1050)
	if err != nil || accepted || next == id {
		t.Fatal("new band not recorded", next, accepted, err)
	}
	s.HoldRestoredData(ctx, strings.Repeat("b", 64), 1060)
	_, sum, err = s.NotificationRecords(ctx, "restore_held", "system", "")
	if err != nil || sum.Held != 1 || sum.Pending != 0 {
		t.Fatal(sum, err)
	}
}

func TestLegacyStatusUnknownRecordTimeStaysStableAfterAcceptance(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := t.Context()
	if _, err := s.DB.Exec(`INSERT INTO status_alert_monitors(key) VALUES('board');INSERT INTO status_alert_jobs(key,episode,kind) VALUES('board',1,'fault')`); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.NotificationRecords(ctx, "all", "system", "")
	if err != nil || len(rows) != 1 || rows[0].RecordedAt != 0 {
		t.Fatal(rows, err)
	}
	before := NotificationRecordCursor(rows[0])
	if err = s.statusAlertResult(ctx, Notification{AlertID: rows[0].ID}, true, "accepted", 2000); err != nil {
		t.Fatal(err)
	}
	rows, _, err = s.NotificationRecords(ctx, "all", "system", "")
	if err != nil || rows[0].RecordedAt != 0 || rows[0].AcceptedAt != 2000 || NotificationRecordCursor(rows[0]) != before {
		t.Fatal("unstable record cursor", rows, err)
	}
}
