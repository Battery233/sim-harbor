package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Battery233/sim-harbor/internal/store"
	"path/filepath"
	"testing"
)

func TestStatusAlertBackupRestoreHoldsPendingAndKeepsAccepted(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	bad := false
	at := int64(1700000000)
	for _, o := range []store.AlertObservation{{Key: "board", Healthy: &bad}, {Key: "sim1", Healthy: &bad}} {
		s.ObserveAlerts(ctx, []store.AlertObservation{o}, at)
		s.ObserveAlerts(ctx, []store.AlertObservation{o}, at+1800)
	}
	n, err := s.NextNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.NotificationResultCodeAt(ctx, n, true, "accepted", at+1800)
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restore")
	if _, err = Restore(ctx, a.Path, target, p.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	r, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.DB.Close()
	if _, err = r.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("restored alert runnable")
	}
	var sent, held, atAccepted int64
	r.DB.QueryRow(`SELECT count(*) FROM status_alert_jobs WHERE state='sent'`).Scan(&sent)
	r.DB.QueryRow(`SELECT count(*) FROM status_alert_jobs WHERE state='restore_held'`).Scan(&held)
	r.DB.QueryRow(`SELECT accepted_at FROM status_alert_jobs WHERE state='sent'`).Scan(&atAccepted)
	if sent != 1 || held != 1 || atAccepted != at+1800 {
		t.Fatal("alert evidence lost")
	}
}
