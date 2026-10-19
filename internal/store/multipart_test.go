package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMultipartProgressPersistsAndRejectsChangedTotal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := sms()
	e.ICCID, e.Group, e.Max, e.Body = "SYNTHETIC-CARD", "SYNTHETIC-GROUP", 3, ""
	no := false
	e.Complete = &no
	e.Fragments = []string{"3,C", "1,A", "1,A"}
	ingest(t, s, e)
	rows, err := s.Messages(ctx, 0, "")
	if err != nil || len(rows) != 1 || rows[0].PartsTotal != 3 || rows[0].PartsReceived != 2 || !reflect.DeepEqual(rows[0].PartIndices, []int{1, 3}) || rows[0].Complete {
		t.Fatal("incorrect partial progress", err)
	}
	id := rows[0].ID
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	before := count(t, s, "events")
	e.Max = 2
	e.Fragments = []string{"2,B"}
	raw, _ := json.Marshal(e)
	if err = s.Ingest(ctx, e, raw, true); !errors.Is(err, ErrSMSTotalMismatch) {
		t.Fatal("changed total accepted", err)
	}
	m, err := s.Message(ctx, id)
	if err != nil || m.PartsTotal != 3 || m.PartsReceived != 2 || m.Complete || count(t, s, "events") != before || count(t, s, "notifications") != 0 {
		t.Fatal("rejected event changed saved state", err)
	}
	e.Max = 3
	ingest(t, s, e)
	m, err = s.Message(ctx, id)
	if err != nil || !m.Complete || m.PartsReceived != 3 || m.Body != "ABC" || count(t, s, "notifications") != 1 {
		t.Fatal("valid completion failed", err)
	}
	ingest(t, s, e)
	if count(t, s, "messages") != 1 || count(t, s, "notifications") != 1 {
		t.Fatal("replay duplicated business records")
	}
}

func TestMultipartAggregateProgressAndDeletedReplay(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := sms()
	e.ICCID, e.Group, e.Max = "SYNTHETIC-CARD", "SYNTHETIC-GROUP", 2
	yes := true
	e.Complete = &yes
	ingest(t, s, e)
	m, err := s.Message(context.Background(), 1)
	if err != nil || !m.Multipart || m.PartsTotal != 2 || m.PartsReceived != 2 || !reflect.DeepEqual(m.PartIndices, []int{1, 2}) {
		t.Fatal("aggregate lost progress", err)
	}
	if err = s.DeleteMessage(context.Background(), m.ID, 1700000100); err != nil {
		t.Fatal(err)
	}
	var total int
	var group string
	if err = s.DB.QueryRow(`SELECT sms_total,sms_group_ref FROM messages WHERE id=?`, m.ID).Scan(&total, &group); err != nil || total != 0 || group != "" {
		t.Fatal("deleted metadata retained", err)
	}
	ingest(t, s, e)
	rows, err := s.Messages(context.Background(), 0, "")
	if err != nil || len(rows) != 0 || count(t, s, "messages") != 1 {
		t.Fatal("deleted replay resurrected metadata", err)
	}
}

func TestMultipartMigrationKeepsUnknownTotalAndOriginalHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	if _, err := s.DB.Exec(`INSERT INTO messages(key,slot,phone,body,sms_ts,complete,fragments,card_name) VALUES('legacy',2,'TEST','OLD',1700000000,0,'{"1":"OLD"}','OLD NAME'); DROP INDEX messages_sms_group; ALTER TABLE messages DROP COLUMN sms_total; ALTER TABLE messages DROP COLUMN sms_group_ref; DELETE FROM schema_version; INSERT INTO schema_version VALUES(15)`); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = open(t, path)
	m, err := s.Message(context.Background(), 1)
	if err != nil || !m.Multipart || m.PartsTotal != 0 || m.PartsReceived != 1 || m.Body != "OLD" || m.CardName != "OLD NAME" || m.Complete || count(t, s, "notifications") != 0 {
		t.Fatal("migration guessed total or changed history", err)
	}
}

func TestMultipartAssemblyAllowsTimestampChanges(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := sms()
	e.ICCID, e.Group, e.Max, e.Body = "SYNTHETIC-CARD", "SYNTHETIC-GROUP", 2, ""
	no := false
	e.Complete = &no
	e.Fragments = []string{"1,A"}
	ref := SMSGroupReference(e)
	ingest(t, s, e)
	e.SMSTS += 30
	e.Fragments = []string{"2,B"}
	if SMSGroupReference(e) != ref {
		t.Fatal("time drift broke diagnostic correlation")
	}
	ingest(t, s, e)
	rows, err := s.Messages(context.Background(), 0, "")
	if err != nil || len(rows) != 1 || !rows[0].Complete || rows[0].Body != "AB" || count(t, s, "notifications") != 1 {
		t.Fatal("same-group fragments with nearby timestamps did not assemble", err)
	}
	e.ICCID = "ANOTHER-CARD"
	if SMSGroupReference(e) == ref {
		t.Fatal("different SIMs share a diagnostic group")
	}
}

func TestMultipartProgressRejectsOutOfRangeStoredMetadata(t *testing.T) {
	for _, total := range []int{-1, 256, int(^uint(0) >> 1)} {
		m := Message{PartsTotal: total, Complete: true}
		if err := m.setMultipart("{}", ""); err == nil {
			t.Fatal("unbounded stored total accepted")
		}
	}
	m := Message{PartsTotal: 2}
	if err := m.setMultipart(`{"3":"SYNTHETIC"}`, ""); err == nil {
		t.Fatal("stored index beyond total accepted")
	}
}

func TestMultipartProgressExcludesKnownSinglePartReceipts(t *testing.T) {
	for _, complete := range []bool{true, false} {
		m := Message{PartsTotal: 1, Complete: complete}
		if err := m.setMultipart(`{"1":"SYNTHETIC"}`, "SYNTHETIC-GROUP"); err != nil || m.Multipart || m.PartsReceived != 1 {
			t.Fatal("single-part receipt classified as multipart or lost metadata", err)
		}
	}
	ordinary := Message{Complete: true}
	if err := ordinary.setMultipart("{}", ""); err != nil || ordinary.Multipart {
		t.Fatal("plain SMS classified as multipart", err)
	}
	unknown := Message{Complete: false}
	if err := unknown.setMultipart(`{"1":"SYNTHETIC"}`, ""); err != nil || !unknown.Multipart {
		t.Fatal("unknown-total incomplete receipt lost its progress", err)
	}
}

func TestVersionFifteenUpgradePreservesIdentityNotificationsAndConfirmation(t *testing.T) {
	for label, confirmedAt := range map[string]int64{"blocked": 0, "confirmed": 1700000010} {
		t.Run(label, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "sim.db")
			s := open(t, path)
			setNumberStatus(t, s, "1", "SYNTHETIC-A", "Saved A", "NUMBER-A", true)
			setNumberStatus(t, s, "2", "SYNTHETIC-B", "Saved B", "NUMBER-B", true)
			if _, err := s.DB.Exec(`UPDATE cards SET notification_enabled=0 WHERE iccid='SYNTHETIC-B'`); err != nil {
				t.Fatal(err)
			}
			e := sms()
			e.ICCID, e.CardName = "SYNTHETIC-A", "Original snapshot"
			ingest(t, s, e)
			if err := s.Read(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(`UPDATE notifications SET state='sent',attempts=2,last_error='accepted',accepted_at=1700000001 WHERE message_id=1`); err != nil {
				t.Fatal(err)
			}
			no := false
			e.Group, e.Max, e.Body, e.CardName = "SYNTHETIC-GROUP", 2, "", ""
			e.Complete, e.Fragments = &no, []string{"1,A"}
			ingest(t, s, e)
			pending := sms()
			pending.ICCID, pending.SMSTS = "SYNTHETIC-A", e.SMSTS+1
			ingest(t, s, pending)
			if _, err := s.DB.Exec(`INSERT INTO recovery_guard(id,restored_at,archive_sha256,restore_id,confirmed_at,confirmation_backup_sha256,confirmation_policy) VALUES(1,1700000000,'SYNTHETIC-ARCHIVE','SYNTHETIC-RESTORE',?,'SYNTHETIC-BACKUP','complete_history_v1'); DROP INDEX messages_sms_group; ALTER TABLE messages DROP COLUMN sms_total; ALTER TABLE messages DROP COLUMN sms_group_ref; DELETE FROM schema_version; INSERT INTO schema_version VALUES(15)`, confirmedAt); err != nil {
				t.Fatal(err)
			}
			beforeCards, err := s.Cards(ctx, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			beforeGuard, err := s.MigrationState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			jobs := func() string {
				var value string
				if err := s.DB.QueryRow(`SELECT group_concat(id||':'||message_id||':'||state||':'||attempts||':'||next_at||':'||last_error||':'||accepted_at,'|') FROM (SELECT * FROM notifications ORDER BY id)`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			beforeJobs, beforeEvents := jobs(), count(t, s, "events")
			if err = s.DB.Close(); err != nil {
				t.Fatal(err)
			}
			s = open(t, path)
			afterCards, err := s.Cards(ctx, nil, false)
			if err != nil || !reflect.DeepEqual(beforeCards, afterCards) {
				t.Fatal("upgrade changed saved identities, metadata or preferences", err)
			}
			afterGuard, err := s.MigrationState(ctx)
			if err != nil || beforeGuard != afterGuard || afterGuard.Blocked != (confirmedAt == 0) {
				t.Fatal("upgrade changed the migration confirmation", err)
			}
			if jobs() != beforeJobs || count(t, s, "events") != beforeEvents {
				t.Fatal("upgrade rewrote notification history or replayed events")
			}
			m, err := s.Message(ctx, 1)
			if err != nil || !m.Read || !m.Complete || m.Body != sms().Body || m.CardName != "Original snapshot" {
				t.Fatal("upgrade rewrote the original message", err)
			}
			m, err = s.Message(ctx, 2)
			if err != nil || m.Complete || m.PartsTotal != 0 || m.PartsReceived != 1 || m.CardName != "" {
				t.Fatal("legacy partial metadata was guessed", err)
			}
			// A genuinely received follow-up may now provide the previously
			// unknown total and complete the existing row without duplicating it.
			e.Fragments = []string{"2,B"}
			ingest(t, s, e)
			m, err = s.Message(ctx, 2)
			if err != nil || !m.Complete || m.Body != "AB" || m.PartsTotal != 2 || m.PartsReceived != 2 || count(t, s, "messages") != 3 || count(t, s, "notifications") != 3 {
				t.Fatal("upgraded partial did not continue correctly", err)
			}
		})
	}
}
