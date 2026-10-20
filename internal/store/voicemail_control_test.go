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

func TestClearTTSRepeatsOnlyAfterLaterSlotCommand(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	limits := DayLimits(base, 60, 20)
	const device, phone, iccid = "SIM-TEST", "13800138000", "SYNTHETIC-ICC"
	claim := func(s *Store, kind string, mode int, clear bool, at time.Time, relax bool) (int64, error) {
		t.Helper()
		return s.ClaimVoicemailControl(ctx, device, 2, phone, iccid, kind, mode, clear, at.Unix(), limits, relax)
	}

	path := filepath.Join(t.TempDir(), "restore-group.db")
	s := open(t, path)
	if _, err := claim(s, "mode", 0, false, base, false); err != nil {
		t.Fatal(err)
	}
	ttsID, err := claim(s, "tts", 0, true, base.Add(time.Second), true)
	if err != nil || ttsID == 0 {
		t.Fatal(err, ttsID)
	}
	_, err = claim(s, "mode", 0, false, base.Add(120*time.Second), false)
	expectSafety(t, err, "control_already_dispatched")
	_, err = claim(s, "tts", 0, true, base.Add(121*time.Second), true)
	expectSafety(t, err, "control_already_dispatched")
	var rows int
	if err = s.DB.QueryRow(`SELECT count(*) FROM voicemail_controls`).Scan(&rows); err != nil || rows != 2 {
		t.Fatal("same restore was claimed again", rows, err)
	}

	rejected, err := claim(s, "mode", 2, false, base.Add(240*time.Second), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkVoicemailControl(ctx, rejected, "rejected"); err != nil {
		t.Fatal(err)
	}
	_, err = claim(s, "tts", 0, true, base.Add(241*time.Second), true)
	expectSafety(t, err, "control_already_dispatched")

	if err = s.MarkVoicemailControl(ctx, ttsID, "unknown"); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	_, err = claim(s, "tts", 0, true, base.Add(360*time.Second), false)
	expectSafety(t, err, "control_already_dispatched")
	_, err = s.ClaimVoicemailControl(ctx, device, 2, phone, "SYNTHETIC-ICC-NEXT", "tts", 0, true, base.Add(361*time.Second).Unix(), limits, true)
	expectSafety(t, err, "card_changed")
	if err = s.DB.QueryRow(`SELECT count(*) FROM voicemail_controls`).Scan(&rows); err != nil || rows != 3 {
		t.Fatal("restart or card change inserted a clear-tts row", rows, err)
	}

	if _, err = claim(s, "mode", 2, false, base.Add(480*time.Second), false); err != nil {
		t.Fatal(err)
	}
	if _, err = claim(s, "mode", 0, false, base.Add(600*time.Second), false); err != nil {
		t.Fatal("second restore mode", err)
	}
	if _, err = claim(s, "tts", 0, true, base.Add(601*time.Second), true); err != nil {
		t.Fatal("second restore clear-tts", err)
	}
	_, err = claim(s, "tts", 0, true, base.Add(602*time.Second), true)
	expectSafety(t, err, "control_already_dispatched")
	if _, err = claim(s, "mode", 2, false, base.Add(720*time.Second), false); err != nil {
		t.Fatal(err)
	}
	if _, err = claim(s, "mode", 0, false, base.Add(840*time.Second), false); err != nil {
		t.Fatal("third restore mode", err)
	}
	if _, err = claim(s, "tts", 0, true, base.Add(841*time.Second), true); err != nil {
		t.Fatal("third restore clear-tts", err)
	}
	if err = s.DB.QueryRow(`SELECT count(*) FROM voicemail_controls WHERE kind='tts' AND clear_tts=1 AND state!='rejected'`).Scan(&rows); err != nil || rows != 3 {
		t.Fatal("clear-tts claims", rows, err)
	}
}
