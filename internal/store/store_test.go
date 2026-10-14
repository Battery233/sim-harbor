package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim/internal/protocol"
)

func sms() protocol.Event {
	return protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, SMSTS: 1700000000, Phone: "TEST-SENDER", Body: "合成短信"}
}
func ingest(t *testing.T, s *Store, e protocol.Event) {
	t.Helper()
	raw, _ := json.Marshal(e)
	if err := s.Ingest(context.Background(), e, raw, true); err != nil {
		t.Fatal(err)
	}
}
func open(t *testing.T, path string) *Store {
	t.Helper()
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestIncompleteWithoutGroupNeverNotifies(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := sms()
	complete := false
	e.Complete = &complete
	e.Max = 1
	e.Fragments = []string{"1,合成残片"}
	ingest(t, s, e)
	ms, err := s.Messages(context.Background(), 0, "")
	if err != nil || len(ms) != 1 || ms[0].Complete || count(t, s, "notifications") != 0 {
		t.Fatal("missing grouping identity falsely completed", ms, err)
	}
}

func TestSQLiteConnectionReplacementRetainsSafetyPragmas(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "space ? #.db"))
	s.DB.SetMaxIdleConns(0)
	for name, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "synchronous": 2} {
		var got int
		if err := s.DB.QueryRow("PRAGMA " + name).Scan(&got); err != nil || got != want {
			t.Errorf("%s = %d, want %d: %v", name, got, want, err)
		}
	}
}

func TestDedupSlotsAndLegitimateMessages(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := sms()
	ingest(t, s, e)
	e.MessageTS = 123
	ingest(t, s, e)
	if count(t, s, "messages") != 1 || count(t, s, "notifications") != 1 {
		t.Fatal("duplicate business record")
	}
	e.Slot = 2
	ingest(t, s, e)
	e.SMSTS++
	ingest(t, s, e)
	if count(t, s, "messages") != 3 {
		t.Fatal("distinct card/time collapsed")
	}
	ms, err := s.Messages(context.Background(), 2, "合成")
	if err != nil || len(ms) != 2 {
		t.Fatal("filter failed")
	}
	if err = s.Read(context.Background(), ms[0].ID); err != nil {
		t.Fatal(err)
	}
	m, err := s.Message(context.Background(), ms[0].ID)
	if err != nil || !m.Read {
		t.Fatal("read state")
	}
	if !errors.Is(s.Read(context.Background(), 999), sql.ErrNoRows) {
		t.Fatal("missing ID not reported")
	}
}

func TestMultipartRestartAndNoRegression(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := sms()
	no := false
	e.Complete = &no
	e.Group = "SYNTHETIC-GROUP"
	e.Max = 3
	e.Body = ""
	e.Fragments = []string{"2,中间,"}
	ingest(t, s, e)
	if count(t, s, "notifications") != 0 {
		t.Fatal("incomplete SMS notified")
	}
	s.DB.Close()
	s = open(t, path)
	e.Fragments = []string{"3,结尾", "1,开头"}
	ingest(t, s, e)
	ms, err := s.Messages(ctx, 0, "")
	if err != nil || len(ms) != 1 || ms[0].Body != "开头中间,结尾" || !ms[0].Complete {
		t.Fatalf("reassembly: %#v %v", ms, err)
	}
	e.Fragments = []string{"2,中间,"}
	ingest(t, s, e)
	ms, _ = s.Messages(ctx, 0, "")
	if !ms[0].Complete || ms[0].Body != "开头中间,结尾" || count(t, s, "notifications") != 1 {
		t.Fatal("late partial regressed complete record")
	}
	n, err := s.NextNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.NotificationResult(ctx, n, false); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	state, err := s.State(ctx, "SIM-TEST")
	if err != nil || state["notifications_retrying"] != 1 {
		t.Fatal("retry state not durable")
	}
	if _, err = s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("retry ignores backoff")
	}
	if err = s.NotificationResult(ctx, n, true); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, e)
	if count(t, s, "notifications") != 1 {
		t.Fatal("duplicate notified after success")
	}
}

func TestFragmentConflictRollsBackWholeEvent(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := sms()
	no := false
	e.Complete = &no
	e.Group = "TEST"
	e.Max = 2
	e.Fragments = []string{"1,original"}
	ingest(t, s, e)
	before := count(t, s, "events")
	e.Fragments = []string{"1,conflict"}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(context.Background(), e, raw, true); err == nil {
		t.Fatal("conflict accepted")
	}
	if count(t, s, "events") != before || count(t, s, "messages") != 1 || count(t, s, "notifications") != 0 {
		t.Fatal("failed ingest partially committed")
	}
}

func TestCompleteBoardAggregateWins(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := sms()
	no := false
	e.Complete = &no
	e.Group = "TEST"
	e.Max = 2
	e.Fragments = []string{"1,合成"}
	ingest(t, s, e)
	yes := true
	e.Complete = &yes
	e.Body = "厂家合成完整正文"
	e.Fragments = nil
	ingest(t, s, e)
	ms, _ := s.Messages(context.Background(), 0, "")
	if len(ms) != 1 || !ms[0].Complete || ms[0].Body != e.Body || count(t, s, "notifications") != 1 {
		t.Fatal("aggregate not promoted")
	}
}

func TestMigrateV1AndRejectFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE schema_version(version INTEGER PRIMARY KEY); INSERT INTO schema_version VALUES(1); CREATE TABLE device(id TEXT PRIMARY KEY,last_seen INTEGER NOT NULL,status TEXT NOT NULL DEFAULT '{}'); INSERT INTO device(id,last_seen) VALUES('SIM-TEST',123);`)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	s := open(t, path)
	state, e := s.State(context.Background(), "SIM-TEST")
	if e != nil || state["last_seen"] != int64(123) || state["status_received_at"] != int64(0) {
		t.Fatalf("v1 upgrade: %v %v", state, e)
	}
	if _, e = s.DB.Exec(`DELETE FROM schema_version; INSERT INTO schema_version VALUES(99)`); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	if s, e = Open(path); e == nil {
		s.DB.Close()
		t.Fatal("future database schema accepted")
	}
}

func TestHistoryCursorDoesNotSkipOrRepeat(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := sms()
	for i := 0; i < 75; i++ {
		e.SMSTS = protocol.Number(1700000000 + i)
		ingest(t, s, e)
	}
	a, err := s.MessagePage(context.Background(), 0, "", 0, 50)
	if err != nil || len(a) != 50 {
		t.Fatal("first history page")
	}
	b, err := s.MessagePage(context.Background(), 0, "", a[49].ID, 50)
	if err != nil || len(b) != 25 || b[0].ID >= a[49].ID {
		t.Fatal("history cursor")
	}
	m, err := s.Message(context.Background(), b[24].ID)
	if err != nil || m.ID != 1 {
		t.Fatal("deep link older message")
	}
}
