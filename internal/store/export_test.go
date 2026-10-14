package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportAllMatchingInboxAndNoMutation(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 80; i++ {
		if _, e = tx.Exec(`INSERT INTO messages(key,slot,phone,body,sms_ts,complete,is_read) VALUES(?,?,?,?,?,?,?)`, fmt.Sprint(i), 1+i%2, "TEST-SENDER", "中文\n\"合成\"", 1700000000+i, i%3 == 0, i%5 == 0); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	all, e := s.ExportMessages(ctx, MessageFilter{})
	if e != nil || len(all) != 80 || all[0].ID != 80 || all[79].ID != 1 {
		t.Fatal(len(all), e)
	}
	filtered, e := s.ExportMessages(ctx, MessageFilter{Slot: 1, Phone: "TEST-SENDER", From: 1700000010, Until: 1700000020})
	if e != nil || len(filtered) != 5 {
		t.Fatal(filtered, e)
	}
	for _, m := range filtered {
		if m.Slot != 1 || m.Timestamp < 1700000010 || m.Timestamp >= 1700000020 || m.Body != "中文\n\"合成\"" {
			t.Fatal(m)
		}
	}
	none, e := s.ExportMessages(ctx, MessageFilter{Phone: "OTHER"})
	if e != nil || none == nil || len(none) != 0 {
		t.Fatal(none, e)
	}
	again, e := s.ExportMessages(ctx, MessageFilter{})
	if e != nil || fmt.Sprint(again) != fmt.Sprint(all) {
		t.Fatal("export mutated data", e)
	}
	if count(t, s, "notifications") != 0 || count(t, s, "outgoing") != 0 {
		t.Fatal("export created work")
	}
}
func TestExportRefusesOversizeAndInvalidRange(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	if _, e := s.ExportMessages(ctx, MessageFilter{From: 4, Until: 3}); e == nil {
		t.Fatal("invalid range")
	}
	if _, e := s.DB.Exec(`INSERT INTO messages(key,slot,phone,body,sms_ts,complete) VALUES('big',1,'TEST',?,1,1)`, strings.Repeat("X", ExportBytes)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ExportMessages(ctx, MessageFilter{}); !errors.Is(e, ErrExportLimit) {
		t.Fatal("oversize not rejected", e)
	}
	if _, e := s.DB.Exec(`DELETE FROM messages; WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<=10000) INSERT INTO messages(key,slot,phone,body,sms_ts,complete) SELECT CAST(x AS TEXT),1,'TEST','body',x,1 FROM n`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ExportMessages(ctx, MessageFilter{}); !errors.Is(e, ErrExportLimit) {
		t.Fatal("row overflow truncated", e)
	}
}
