package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestVoicemailControlRepeatAfterOppositeAction(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	limits := DayLimits(base, 60, 5)
	claim := func(s *Store, mode int, at time.Time) error {
		t.Helper()
		_, err := s.ClaimVoicemailControl(ctx, "SIM-TEST", 2, "13800138000", "SYNTHETIC-ICC", "mode", mode, false, at.Unix(), limits, false)
		return err
	}

	s := open(t, filepath.Join(t.TempDir(), "stop-enable-stop.db"))
	if err := claim(s, 0, base); err != nil {
		t.Fatal(err)
	}
	if err := claim(s, 2, base.Add(120*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := claim(s, 0, base.Add(240*time.Second)); err != nil {
		t.Fatal("stop after enable was blocked", err)
	}
	expectSafety(t, claim(s, 0, base.Add(360*time.Second)), "control_already_dispatched")

	s = open(t, filepath.Join(t.TempDir(), "enable-stop-enable.db"))
	if err := claim(s, 2, base); err != nil {
		t.Fatal(err)
	}
	if err := claim(s, 0, base.Add(120*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := claim(s, 2, base.Add(240*time.Second)); err != nil {
		t.Fatal("enable after stop was blocked", err)
	}

	path := filepath.Join(t.TempDir(), "same-attempt.db")
	s = open(t, path)
	id, err := s.ClaimVoicemailControl(ctx, "SIM-TEST", 2, "13800138000", "SYNTHETIC-ICC", "mode", 2, false, base.Unix(), limits, false)
	if err != nil || id == 0 {
		t.Fatal(err, id)
	}
	expectSafety(t, claim(s, 2, base.Add(120*time.Second)), "control_already_dispatched")
	if err = s.MarkVoicemailControl(ctx, id, "unknown"); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	expectSafety(t, claim(s, 2, base.Add(240*time.Second)), "control_already_dispatched")
	var rows int
	if err = s.DB.QueryRow(`SELECT count(*) FROM voicemail_controls`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("unknown attempt was sent again", rows, err)
	}
}
