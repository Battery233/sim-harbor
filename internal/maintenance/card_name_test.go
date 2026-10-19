package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestCardNameBackupRestoreAndLegacyMigration(t *testing.T) {
	for _, version := range []int{4, 5, 6, 7, 8, 9, 10, 11, 12, 13} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			ctx := context.Background()
			s := fixture(t)
			e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 2, Phone: "TEST", Body: "SYNTHETIC", SMSTS: 1700000000, CardName: "SYNTHETIC HK"}
			raw, _ := json.Marshal(e)
			if err := s.Ingest(ctx, e, raw, true); err != nil {
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
				if _, err := s.DB.Exec(`ALTER TABLE cards DROP COLUMN number`); err != nil {
					t.Fatal(err)
				}
			}
			if version == 8 {
				if _, err := s.DB.Exec(`ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at`); err != nil {
					t.Fatal(err)
				}
			}
			if version < 8 {
				if _, err := s.DB.Exec(`ALTER TABLE cards DROP COLUMN number;ALTER TABLE messages DROP COLUMN deleted_at;DROP INDEX notifications_success_time;ALTER TABLE notifications DROP COLUMN accepted_at`); err != nil {
					t.Fatal(err)
				}
			}
			if version < 7 {
				if _, err := s.DB.Exec(`DROP INDEX outgoing_card_limits;DROP INDEX messages_card;ALTER TABLE messages DROP COLUMN card_id;ALTER TABLE messages DROP COLUMN device_id;DROP TABLE cards`); err != nil {
					t.Fatal(err)
				}
			}
			if version < 6 {
				if _, err := s.DB.Exec(`ALTER TABLE messages DROP COLUMN card_name`); err != nil {
					t.Fatal(err)
				}
			}
			if version == 4 {
				if _, err := s.DB.Exec(`DROP TABLE recovery_guard`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.DB.Exec(`DROP TABLE sms_receipts; DELETE FROM schema_version; INSERT INTO schema_version VALUES(?)`, version); err != nil {
				t.Fatal(err)
			}
			a, err := Create(ctx, s, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			p, err := Inspect(ctx, a.Path)
			if err != nil || p.Manifest.Schema != version {
				t.Fatal("backup schema mismatch", err)
			}
			target := filepath.Join(t.TempDir(), "restored")
			if _, err = Restore(ctx, a.Path, target, p.ArchiveSHA256); err != nil {
				t.Fatal(err)
			}
			r, err := store.Open(filepath.Join(target, "sim.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer r.DB.Close()
			var name, body, state string
			if err = r.DB.QueryRow(`SELECT m.card_name,m.body,n.state FROM messages m JOIN notifications n ON n.message_id=m.id`).Scan(&name, &body, &state); err != nil {
				t.Fatal(err)
			}
			want := ""
			if version >= 6 {
				want = e.CardName
			}
			blocked, err := r.RestoreBlocked(ctx)
			if err != nil || name != want || body != e.Body || state != "restore_held" || !blocked {
				t.Fatal("name, original message or restore guard changed", err)
			}
		})
	}
}

func TestVersionSixBackupRequiresNameColumn(t *testing.T) {
	s := fixture(t)
	if _, err := s.DB.Exec(`ALTER TABLE messages DROP COLUMN card_name`); err != nil {
		t.Fatal(err)
	}
	if a, err := Create(context.Background(), s, t.TempDir()); err == nil {
		a.Close()
		t.Fatal("malformed version six archive accepted")
	}
}
