package store

import (
	"context"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"path/filepath"
	"testing"
	"time"
)

func TestNotificationSuccessUsesFirstAcceptanceAndIgnoresLateFailure(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	ingest(t, s, sms())
	n, err := s.NextNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	if err = s.NotificationResultCodeAt(ctx, n, false, "transport_failed", now.Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	stats, err := s.NotificationSuccess(ctx, now, time.UTC)
	if err != nil || stats.Today != 0 || stats.Undated != 0 {
		t.Fatal("failed attempt counted", stats, err)
	}
	if err = s.NotificationResultCodeAt(ctx, n, true, "accepted", now.Unix()); err != nil {
		t.Fatal(err)
	}
	if err = s.NotificationResultCodeAt(ctx, n, true, "accepted", now.AddDate(0, 0, 1).Unix()); err != nil {
		t.Fatal(err)
	}
	if err = s.NotificationResultCodeAt(ctx, n, false, "transport_failed", now.Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	var at, attempts int64
	var state, last string
	s.DB.QueryRow(`SELECT accepted_at,attempts,state,last_error FROM notifications WHERE id=?`, n.ID).Scan(&at, &attempts, &state, &last)
	if at != now.Unix() || attempts != 1 || state != "sent" || last != "" {
		t.Fatal("acceptance moved or regressed", at, attempts, state, last)
	}
	if err = s.ReserveNotificationTest(ctx, now.Unix()); err != nil {
		t.Fatal(err)
	}
	s.CompleteNotificationTest(ctx, now.Unix(), "accepted")
	stats, err = s.NotificationSuccess(ctx, now, time.UTC)
	if err != nil || stats.Today != 1 || stats.Last30Days != 1 {
		t.Fatal("test push or repeats counted", stats, err)
	}
	later, _ := s.NotificationSuccess(ctx, now.AddDate(0, 0, 1), time.UTC)
	if later.Today != 0 || later.Last30Days != 1 {
		t.Fatal("day rollover", later)
	}
}

func TestNotificationSuccessCalendarBoundsTimezoneAndDST(t *testing.T) {
	for _, zone := range []string{"Asia/Shanghai", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatal(err)
			}
			// This 30-day interval crosses the New York DST transition.
			now := time.Date(2026, 3, 20, 1, 0, 0, 0, loc)
			day := time.Date(2026, 3, 20, 0, 0, 0, 0, loc)
			start := day.AddDate(0, 0, -29)
			s := open(t, filepath.Join(t.TempDir(), "sim.db"))
			ctx := context.Background()
			cases := []struct {
				at    int64
				state string
			}{{day.Unix(), "sent"}, {now.Unix(), "sent"}, {day.Unix() - 1, "sent"}, {start.Unix(), "sent"}, {start.Unix() - 1, "sent"}, {now.Unix() + 1, "sent"}, {0, "sent"}, {now.Unix(), "pending"}, {now.Unix(), "restore_held"}}
			for i, c := range cases {
				e := sms()
				e.SMSTS += 1000 + protocol.Number(i)
				ingest(t, s, e)
				if _, err = s.DB.Exec(`UPDATE notifications SET state=?,accepted_at=? WHERE message_id=?`, c.state, c.at, i+1); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.NotificationSuccess(ctx, now, loc)
			if err != nil || got.Today != 2 || got.Last30Days != 4 || got.Undated != 1 || got.Timezone != zone {
				t.Fatal("calendar bounds", got, err)
			}
			// UTC deliberately disagrees with configured day around local midnight.
			if zone == "Asia/Shanghai" {
				utc, _ := s.NotificationSuccess(ctx, now, time.UTC)
				if utc.Today == got.Today {
					t.Fatal("configured timezone ignored")
				}
			}
		})
	}
}

func TestNotificationSuccessLegacyMigrationLeavesDatesUnknownAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ctx := context.Background()
	ingest(t, s, sms())
	if _, err := s.DB.Exec(`DROP TABLE call_notifications;DROP TABLE calls;UPDATE notifications SET state='sent';DROP TABLE status_alert_jobs;DROP TABLE status_alert_monitors;ALTER TABLE cards DROP COLUMN notification_enabled;ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DROP INDEX notifications_success_time;ALTER TABLE notifications DROP COLUMN accepted_at;DELETE FROM schema_version;INSERT INTO schema_version VALUES(7)`); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	stats, err := s.NotificationSuccess(ctx, time.Now(), time.UTC)
	if err != nil || stats.Today != 0 || stats.Last30Days != 0 || stats.Undated != 1 {
		t.Fatal("legacy dates guessed", stats, err)
	}
	if err = s.NotificationResultCodeAt(ctx, Notification{ID: 1}, true, "accepted", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	stats, _ = s.NotificationSuccess(ctx, time.Now(), time.UTC)
	if stats.Undated != 1 {
		t.Fatal("legacy duplicate invented date")
	}
	e := sms()
	e.SMSTS++
	ingest(t, s, e)
	n, err := s.NextNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Unix()
	if err = s.NotificationResultCodeAt(ctx, n, true, "accepted", at); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	stats, err = s.NotificationSuccess(ctx, time.Unix(at, 0), time.UTC)
	if err != nil || stats.Today != 1 || stats.Last30Days != 1 || stats.Undated != 1 {
		t.Fatal("restart lost acceptance", stats, err)
	}
}
