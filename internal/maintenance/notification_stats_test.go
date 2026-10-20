package maintenance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestNotificationSuccessBackupRestoreAndOldVersionDates(t *testing.T) {
	for _, version := range []int{7, 8, 9, 10, 11, 12, 13} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			s := fixture(t)
			ctx := context.Background()
			now := time.Now()
			e := protocol.Event{Type: 501, Slot: 1, DeviceID: "SIM-TEST", Phone: "TEST", Body: "SYNTHETIC", SMSTS: 1700000000}
			raw, _ := json.Marshal(e)
			if err := s.Ingest(ctx, e, raw, true); err != nil {
				t.Fatal(err)
			}
			n, err := s.NextNotification(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.NotificationResultCodeAt(ctx, n, true, "accepted", now.Unix()); err != nil {
				t.Fatal(err)
			}
			e.SMSTS++
			raw, _ = json.Marshal(e)
			s.Ingest(ctx, e, raw, true)
			if _, err := s.DB.Exec(`DROP TABLE IF EXISTS voicemail_controls; DROP TABLE IF EXISTS voicemails`); err != nil {
				t.Fatal(err)
			}
			if version < 14 {
				if _, err := s.DB.Exec(`DROP TABLE status_alert_jobs;DROP TABLE status_alert_monitors`); err != nil {
					t.Fatal(err)
				}
			}
			if version < 13 {
				if _, err := s.DB.Exec(`ALTER TABLE cards DROP COLUMN notification_enabled`); err != nil {
					t.Fatal(err)
				}
			}
			if version == 11 {
				if _, err := s.DB.Exec(`ALTER TABLE calls DROP COLUMN timeout_at; ALTER TABLE call_notifications DROP COLUMN attempted_at;ALTER TABLE call_notifications DROP COLUMN kind`); err != nil {
					t.Fatal(err)
				}
			}
			if version < 11 {
				if _, err := s.DB.Exec(`DROP TABLE call_notifications;DROP TABLE calls`); err != nil {
					t.Fatal(err)
				}
			}
			if version == 9 {
				if _, err := s.DB.Exec(`ALTER TABLE cards DROP COLUMN number;DELETE FROM schema_version;INSERT INTO schema_version VALUES(9)`); err != nil {
					t.Fatal(err)
				}
			}
			if version == 8 {
				if _, err := s.DB.Exec(`ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DELETE FROM schema_version;INSERT INTO schema_version VALUES(8)`); err != nil {
					t.Fatal(err)
				}
			}
			if version == 7 {
				if _, err = s.DB.Exec(`ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DROP INDEX notifications_success_time;ALTER TABLE notifications DROP COLUMN accepted_at;DELETE FROM schema_version;INSERT INTO schema_version VALUES(7)`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.DB.Exec(`DROP TABLE sms_receipts;DELETE FROM schema_version;INSERT INTO schema_version VALUES(?)`, version); err != nil {
				t.Fatal(err)
			}
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
			stats, err := r.NotificationSuccess(ctx, now, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			if version >= 8 && (stats.Today != 1 || stats.Last30Days != 1 || stats.Undated != 0) {
				t.Fatal("acceptance lost", stats)
			}
			if version == 7 && (stats.Today != 0 || stats.Last30Days != 0 || stats.Undated != 1) {
				t.Fatal("legacy date invented", stats)
			}
			_, sum, err := r.NotificationPage(ctx, "all", 0)
			blocked, _ := r.RestoreBlocked(ctx)
			if err != nil || !blocked || sum.Held != 1 || sum.Accepted != 1 {
				t.Fatal("restore protections changed", sum, err)
			}
		})
	}
}
func TestVersionEightBackupRequiresSuccessTimestamp(t *testing.T) {
	s := fixture(t)
	if _, err := s.DB.Exec(`ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DROP INDEX notifications_success_time;ALTER TABLE notifications DROP COLUMN accepted_at`); err != nil {
		t.Fatal(err)
	}
	if a, err := Create(context.Background(), s, t.TempDir()); err == nil {
		a.Close()
		t.Fatal("malformed v8 accepted")
	}
}
