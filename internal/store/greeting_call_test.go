package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAnsweredCallStillCurrent(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "calls.db"))
	ctx := context.Background()
	insert := func(key string, start, connected int64, ended int, state string) {
		t.Helper()
		if _, err := s.DB.Exec(`INSERT INTO calls(key,device_id,card_name,slot,phone,start_ts,connected_ts,ended,state,received_at) VALUES(?,?, '',2,'SYNTHETIC',?,?,?,?,1)`, key, "SIM-TEST", start, connected, ended, state); err != nil {
			t.Fatal(err)
		}
	}
	if reason, err := s.AnsweredCallStillCurrent(ctx, "SIM-TEST", 2, 0, 10); err != nil || reason != "call_identity_insufficient" {
		t.Fatal(reason, err)
	}
	insert("one", 100, 103, 0, "connected")
	if reason, err := s.AnsweredCallStillCurrent(ctx, "SIM-TEST", 2, 100, 103); err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	if _, err := s.DB.Exec(`UPDATE calls SET ended=1, state='answered' WHERE key='one'`); err != nil {
		t.Fatal(err)
	}
	if reason, err := s.AnsweredCallStillCurrent(ctx, "SIM-TEST", 2, 100, 103); err != nil || reason != "call_ended" {
		t.Fatal(reason, err)
	}
	insert("two", 200, 201, 0, "connected")
	insert("three", 300, 301, 0, "connected")
	if reason, err := s.AnsweredCallStillCurrent(ctx, "SIM-TEST", 2, 200, 201); err != nil || reason != "call_replaced" {
		t.Fatal(reason, err)
	}
	if reason, err := s.AnsweredCallStillCurrent(ctx, "SIM-TEST", 2, 300, 301); err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	if _, err := s.DB.Exec(`UPDATE calls SET state='unknown' WHERE key='three'`); err != nil {
		t.Fatal(err)
	}
	if reason, err := s.AnsweredCallStillCurrent(ctx, "SIM-TEST", 2, 300, 301); err != nil || reason != "" {
		t.Fatal("open call", reason, err)
	}
	if reason, err := s.AnsweredCallStillCurrent(ctx, "SIM-TEST", 1, 200, 201); err != nil || reason != "call_identity_insufficient" {
		t.Fatal(reason, err)
	}
}
