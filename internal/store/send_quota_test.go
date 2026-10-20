package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestSharedSendQuotaBothDirections(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	limits := DayLimits(now, 60, 5)
	const device, iccid = "SIM-TEST", "SYNTHETIC-ICC-0001"

	t.Run("ordinary then control", func(t *testing.T) {
		s := open(t, filepath.Join(t.TempDir(), "sim.db"))
		o := preview(t, s, 1, 1, now.Unix())
		if _, ok, err := s.ClaimOutgoing(ctx, o.ID, o.ICCID, now.Unix(), limits); err != nil || !ok {
			t.Fatal(err, ok)
		}
		_, err := s.ClaimVoicemailControl(ctx, device, 1, "13800138000", iccid, "mode", 2, false, now.Add(59*time.Second).Unix(), limits, false)
		expectSafety(t, err, "send_interval")
		if _, err = s.ClaimVoicemailControl(ctx, device, 1, "13800138000", iccid, "mode", 2, false, now.Add(60*time.Second).Unix(), limits, false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("control then ordinary", func(t *testing.T) {
		s := open(t, filepath.Join(t.TempDir(), "sim.db"))
		if _, err := s.ClaimVoicemailControl(ctx, device, 1, "13800138000", iccid, "mode", 0, false, now.Unix(), limits, false); err != nil {
			t.Fatal(err)
		}
		early := preview(t, s, 1, 1, now.Unix())
		if _, _, err := s.ClaimOutgoing(ctx, early.ID, early.ICCID, now.Add(59*time.Second).Unix(), limits); err == nil {
			t.Fatal("ordinary sms ignored a control inside the interval")
		} else {
			expectSafety(t, err, "send_interval")
		}
		later := preview(t, s, 2, 1, now.Unix())
		if _, ok, err := s.ClaimOutgoing(ctx, later.ID, later.ICCID, now.Add(60*time.Second).Unix(), limits); err != nil || !ok {
			t.Fatal(err, ok)
		}
	})

	t.Run("daily limit", func(t *testing.T) {
		full := DayLimits(now, 60, 1)
		s := open(t, filepath.Join(t.TempDir(), "sim.db"))
		if _, err := s.ClaimVoicemailControl(ctx, device, 1, "13800138000", iccid, "mode", 2, false, now.Unix(), full, false); err != nil {
			t.Fatal(err)
		}
		o := preview(t, s, 1, 1, now.Unix())
		_, _, err := s.ClaimOutgoing(ctx, o.ID, o.ICCID, now.Add(60*time.Second).Unix(), full)
		expectSafety(t, err, "daily_limit")

		s = open(t, filepath.Join(t.TempDir(), "other.db"))
		first := preview(t, s, 1, 1, now.Unix())
		if _, ok, err := s.ClaimOutgoing(ctx, first.ID, first.ICCID, now.Unix(), full); err != nil || !ok {
			t.Fatal(err, ok)
		}
		_, err = s.ClaimVoicemailControl(ctx, device, 1, "13800138000", iccid, "mode", 0, false, now.Add(60*time.Second).Unix(), full, false)
		expectSafety(t, err, "daily_limit")
	})

	t.Run("same confirmation still shares the daily slot", func(t *testing.T) {
		one := DayLimits(now, 60, 1)
		s := open(t, filepath.Join(t.TempDir(), "sim.db"))
		if _, err := s.ClaimVoicemailControl(ctx, device, 2, "13800138000", iccid, "mode", 0, false, now.Unix(), one, false); err != nil {
			t.Fatal(err)
		}
		_, err := s.ClaimVoicemailControl(ctx, device, 2, "13800138000", iccid, "tts", 0, true, now.Add(time.Second).Unix(), one, true)
		expectSafety(t, err, "daily_limit")

		room := DayLimits(now, 60, 2)
		s = open(t, filepath.Join(t.TempDir(), "room.db"))
		if _, err = s.ClaimVoicemailControl(ctx, device, 2, "13800138000", iccid, "mode", 0, false, now.Unix(), room, false); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimVoicemailControl(ctx, device, 2, "13800138000", iccid, "tts", 0, true, now.Add(time.Second).Unix(), room, true); err != nil {
			t.Fatal("same confirmation was blocked by the interval", err)
		}
		_, err = s.ClaimVoicemailControl(ctx, device, 2, "13800138000", iccid, "tts", 0, false, now.Add(2*time.Second).Unix(), room, true)
		expectSafety(t, err, "daily_limit")
	})
}

func TestSharedSendQuotaConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	a := open(t, path)
	b := open(t, path)
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	limits := DayLimits(now, 60, 1)
	o := preview(t, a, 1, 1, now.Unix())
	start := make(chan struct{})
	var wins atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := a.ClaimVoicemailControl(ctx, "SIM-TEST", 1, "13800138000", o.ICCID, "mode", 2, false, now.Unix(), limits, false)
		if err == nil {
			wins.Add(1)
			return
		}
		if !quotaRaceLoss(err) {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		_, ok, err := b.ClaimOutgoing(ctx, o.ID, o.ICCID, now.Unix(), limits)
		if err == nil && ok {
			wins.Add(1)
			return
		}
		if err != nil && !quotaRaceLoss(err) {
			t.Error(err)
		}
	}()
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("shared quota allowed both sends", wins.Load())
	}
}

func quotaRaceLoss(err error) bool {
	var safety *SafetyError
	if errors.As(err, &safety) {
		return safety.Code == "send_interval" || safety.Code == "daily_limit"
	}
	var busy *sqlite.Error
	return errors.As(err, &busy) && busy.Code()&255 == 5
}
