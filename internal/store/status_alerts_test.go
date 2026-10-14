package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestStatusAlertThresholdFlapsOnceRecoveryAndRestart(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	bad, good := false, true
	at := int64(1700000000)
	observe := func(healthy *bool, now int64) {
		t.Helper()
		if err := s.ObserveAlerts(ctx, []AlertObservation{{Key: "board", Healthy: healthy, Label: "板子"}}, now); err != nil {
			t.Fatal(err)
		}
	}
	observe(&bad, at)
	observe(&bad, at+899)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("early fault")
	}
	observe(&bad, at+900)
	n, err := s.NextNotification(ctx)
	if err != nil || n.AlertID == 0 || n.AlertKind != "fault" {
		t.Fatal("fault absent", err)
	}
	s.NotificationResultCodeAt(ctx, n, false, "http_rejected", at+900)
	s.DB.Exec(`UPDATE status_alert_jobs SET next_at=0`)
	retry, _ := s.NextNotification(ctx)
	if retry.AlertID != n.AlertID {
		t.Fatal("new job for retry")
	}
	s.NotificationResultCodeAt(ctx, retry, true, "accepted", at+901)
	s.ResetAlertObservation(ctx)
	observe(&bad, at+1000)
	observe(&bad, at+10000)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restart repeated accepted fault")
	}
	observe(&good, at+10001)
	observe(&good, at+10299)
	observe(&bad, at+10300)
	observe(&good, at+10301)
	observe(&good, at+10600)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("flap or early recovery")
	}
	observe(&good, at+10601)
	recovery, err := s.NextNotification(ctx)
	if err != nil || recovery.AlertKind != "recovery" {
		t.Fatal("recovery absent")
	}
	s.NotificationResultCodeAt(ctx, recovery, true, "accepted", at+10601)
	observe(&good, at+11000)
	var jobs int
	s.DB.QueryRow(`SELECT count(*) FROM status_alert_jobs`).Scan(&jobs)
	if jobs != 2 {
		t.Fatal("repeated recovery")
	}
	observe(&bad, at+12000)
	observe(&bad, at+12900)
	if n, err := s.NextNotification(ctx); err != nil || n.AlertID == recovery.AlertID {
		t.Fatal("new incident absent")
	}
}
func TestSIMAlertUnknownAndUnacceptedRestartNeverBackfill(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	bad, good := false, true
	at := int64(1700000000)
	observe := func(healthy *bool, now int64) {
		t.Helper()
		if err := s.ObserveAlerts(ctx, []AlertObservation{{Key: "sim1", Healthy: healthy, Label: "Test card"}}, now); err != nil {
			t.Fatal(err)
		}
	}
	observe(&bad, at)
	observe(&bad, at+1799)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("early sim")
	}
	observe(nil, at+1800)
	observe(&bad, at+1801)
	observe(&bad, at+3601)
	if _, err := s.NextNotification(ctx); err != nil {
		t.Fatal("sim absent")
	}
	s.ResetAlertObservation(ctx)
	observe(&bad, at+10000)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restart inferred downtime")
	}
	observe(&bad, at+11800)
	observe(&good, at+11801)
	observe(&good, at+12101)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("recovered before acceptance still notified")
	}
}
