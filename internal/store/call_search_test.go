package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCallSearchDisplayLabelsAndLiteralNumberText(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := time.Now().Unix()
	phones := []string{"anonymous", " ANONYMOUS ", "", "SYNTHETIC-2468", "TEST%_CALLER"}
	for i, phone := range phones {
		e := callEvent(603, now+int64(i)*30)
		e.Phone = phone
		e.TelEndTS = e.TelStartTS + 5
		recordCall(t, s, e, false)
	}
	for _, tt := range []struct {
		q    string
		want int
	}{{"匿名", 2}, {"匿名来电", 2}, {" AnOnYmOuS ", 2}, {"号码未提供", 1}, {"2468", 1}, {"synthetic", 1}, {"%_", 1}, {"' OR 1=1 --", 0}, {" ", 5}} {
		calls, err := s.Calls(ctx, "", tt.q, 0, now+1000)
		if err != nil || len(calls) != tt.want {
			t.Fatalf("calls search %q: want %d got %d err %v", tt.q, tt.want, len(calls), err)
		}
		groups, err := s.CallGroups(ctx, "", tt.q, 0, now+1000)
		if err != nil || len(groups) != tt.want {
			t.Fatalf("groups search %q: want %d got %d err %v", tt.q, tt.want, len(groups), err)
		}
	}
	groups, _ := s.CallGroups(ctx, "", "匿名", 0, now+1000)
	if groups[0].Count != 1 || groups[1].Count != 1 {
		t.Fatal("alias search merged distinct anonymous callers")
	}
}

func TestAnonymousSearchFiltersBeforeGroupPaginationAndCardIsolation(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := time.Now().Unix()
	for i := 0; i < 53; i++ {
		e := callEvent(603, now+int64(i)*30)
		e.Phone = "anonymous"
		e.TelEndTS = e.TelStartTS + 5
		recordCall(t, s, e, false)
	}
	a, _ := s.Call(ctx, 1, now)
	other := callEvent(603, now+2000)
	other.ICCID = "SECOND-SYNTHETIC-CARD"
	other.Phone = "anonymous"
	other.TelEndTS = other.TelStartTS + 5
	recordCall(t, s, other, false)
	page, err := s.CallGroups(ctx, a.CardID, "匿名", 0, now+3000)
	if err != nil || len(page) != 51 || page[0].Latest.ID != 53 {
		t.Fatal("card filter lost on alias search", err)
	}
	older, err := s.CallGroups(ctx, a.CardID, "匿名", page[49].Latest.ID, now+3000)
	if err != nil || len(older) != 3 || older[0].Latest.ID != 3 {
		t.Fatal("anonymous pagination skipped calls", err)
	}
	rows, err := s.Calls(ctx, a.CardID, "匿名", 4, now+3000)
	if err != nil || len(rows) != 3 {
		t.Fatal("detail and group filters differ", err)
	}
}
