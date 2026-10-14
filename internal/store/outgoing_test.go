package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/Battery233/sim-harbor/internal/outbound"
	"modernc.org/sqlite"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func preview(t *testing.T, s *Store, n int, slot int, now int64) Outgoing {
	t.Helper()
	o, e := s.PrepareOutgoing(context.Background(), "SIM-TEST", fmt.Sprintf("tid-%d", n), "SYNTHETIC-ICC-0001", outbound.Input{ID: fmt.Sprintf("%032x", n), Slot: slot, Phone: "000", Body: fmt.Sprintf("SYNTHETIC %d", n)}, 1, now)
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func limit(now int64) SendLimits { return DayLimits(time.Unix(now, 0).UTC(), 60, 5) }
func expectSafety(t *testing.T, e error, code string) {
	t.Helper()
	var se *SafetyError
	if !errors.As(e, &se) || se.Code != code {
		t.Fatalf("want %s, got %v", code, e)
	}
}
func TestOutgoingConcurrentClaimAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	now := int64(1700000000)
	o := preview(t, s, 1, 1, now)
	var claims atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, claimed, e := s.ClaimOutgoing(ctx, o.ID, o.ICCID, now, limit(now))
			if e != nil {
				t.Error(e)
			}
			if claimed {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("more than one dispatch claim", claims.Load())
	}
	if e := s.MarkOutgoingWrite(ctx, o.ID, true, now); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	s = open(t, path)
	o, e := s.Outgoing(ctx, o.ID, now+1)
	if e != nil || o.State != "unknown" {
		t.Fatal("restart must preserve uncertainty", o, e)
	}
	_, again, e := s.ClaimOutgoing(ctx, o.ID, "DIFFERENT-CARD", now+1, limit(now))
	if e != nil || again {
		t.Fatal("restart/repeated confirm reclaims", e)
	}
	var full int
	if e := s.DB.QueryRow("PRAGMA synchronous").Scan(&full); e != nil || full != 2 {
		t.Fatal("durability", full, e)
	}
}
func TestOutgoingPreviewIdentityExpiryCancelAndContent(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := int64(1700000000)
	o := preview(t, s, 1, 1, now)
	in := outbound.Input{ID: o.ID, Slot: 1, Phone: o.Phone, Body: o.Body}
	if _, e := s.PrepareOutgoing(ctx, "SIM-TEST", "ignored", "different", in, 1, now); e != nil {
		t.Fatal(e)
	}
	in.Body = "CHANGED"
	_, e := s.PrepareOutgoing(ctx, "SIM-TEST", "new", "ICC", in, 1, now)
	expectSafety(t, e, "operation_conflict")
	in.ID = fmt.Sprintf("%032x", 2)
	in.Body = o.Body
	_, e = s.PrepareOutgoing(ctx, "SIM-TEST", "new", o.ICCID, in, 1, now)
	expectSafety(t, e, "duplicate_content")
	_, _, e = s.ClaimOutgoing(ctx, o.ID, "OTHER", now, limit(now))
	expectSafety(t, e, "card_changed")
	_, _, e = s.ClaimOutgoing(ctx, o.ID, o.ICCID, now+300, limit(now))
	expectSafety(t, e, "preview_expired")
	if e = s.CancelOutgoing(ctx, o.ID, now); e != nil {
		t.Fatal(e)
	}
	_, _, e = s.ClaimOutgoing(ctx, o.ID, o.ICCID, now, limit(now))
	expectSafety(t, e, "preview_expired")
	in.ID = fmt.Sprintf("%032x", 3)
	_, e = s.PrepareOutgoing(ctx, "SIM-TEST", "new", "ICC", in, 1, now)
	if e != nil {
		t.Fatal("cancel should allow a new unclaimed preview", e)
	}
}
func TestOutgoingLimitsAndUnknownContentBlock(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := int64(1700000000)
	a := preview(t, s, 1, 1, now)
	_, _, e := s.ClaimOutgoing(ctx, a.ID, a.ICCID, now, limit(now))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.MarkOutgoingWrite(ctx, a.ID, false, now); e != nil {
		t.Fatal(e)
	}
	b := preview(t, s, 2, 1, now)
	_, _, e = s.ClaimOutgoing(ctx, b.ID, b.ICCID, now+1, limit(now))
	expectSafety(t, e, "send_interval")
	c, err := s.PrepareOutgoing(ctx, "SIM-TEST", "tid-3", "SYNTHETIC-ICC-0002", outbound.Input{ID: fmt.Sprintf("%032x", 3), Slot: 2, Phone: "000", Body: "SYNTHETIC 3"}, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	_, ok, e := s.ClaimOutgoing(ctx, c.ID, c.ICCID, now+1, limit(now))
	if e != nil || !ok {
		t.Fatal("independent card", e)
	}
	l := limit(now)
	l.Daily = 1
	_, _, e = s.ClaimOutgoing(ctx, b.ID, b.ICCID, now+61, l)
	expectSafety(t, e, "daily_limit")
	in := outbound.Input{ID: fmt.Sprintf("%032x", 4), Slot: 1, Phone: a.Phone, Body: a.Body}
	_, e = s.PrepareOutgoing(ctx, "SIM-TEST", "tid-late", a.ICCID, in, 1, now+90000)
	expectSafety(t, e, "duplicate_content")
	if e = s.CancelOutgoing(ctx, a.ID, now); e == nil {
		t.Fatal("claimed operation canceled")
	}
}
func TestOutgoingHistoryCursor(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := int64(1700000000)
	for i := 1; i <= 75; i++ {
		preview(t, s, i, 1, now)
	}
	a, e := s.OutgoingPage(context.Background(), 0, now, "")
	if e != nil || len(a) != 51 {
		t.Fatal(e)
	}
	b, e := s.OutgoingPage(context.Background(), a[49].Sequence, now, "")
	if e != nil || len(b) != 25 || b[24].Sequence != 1 {
		t.Fatal("cursor", e)
	}
}

func TestOutgoingClaimAcrossConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	a := open(t, path)
	b := open(t, path)
	now := int64(1700000000)
	o := preview(t, a, 1, 1, now)
	start := make(chan struct{})
	var claims atomic.Int32
	var wg sync.WaitGroup
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			<-start
			_, ok, e := s.ClaimOutgoing(ctx, o.ID, o.ICCID, now, limit(now))
			// SQLite may name a snapshot conflict as "database is locked (517)".
			// Check the primary result code, not the driver's display text.
			var busy *sqlite.Error
			if e != nil && (!errors.As(e, &busy) || busy.Code()&255 != 5) {
				t.Error(e)
			}
			if ok && e == nil {
				claims.Add(1)
			}
		}(s)
	}
	close(start)
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("cross-connection duplicate claim", claims.Load())
	}
	for _, s := range []*Store{a, b} {
		_, ok, e := s.ClaimOutgoing(ctx, o.ID, o.ICCID, now+1, limit(now))
		if e != nil || ok {
			t.Fatal("reclaimed", e)
		}
	}
}
