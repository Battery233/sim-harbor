package maintenance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestMultipartMetadataBackupRestoreAndContinuation(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	no := false
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, ICCID: "SYNTHETIC-CARD", Phone: "TEST", SMSTS: 1700000000, Group: "SYNTHETIC-GROUP", Max: 2, Complete: &no, Fragments: []string{"1,A"}}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil || p.Manifest.Schema != store.SchemaVersion || p.Manifest.Counts["sms_receipts"] != 1 {
		t.Fatal("metadata backup invalid", err)
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
	m, err := r.Message(ctx, 1)
	if err != nil || m.PartsTotal != 2 || m.PartsReceived != 1 || m.Complete {
		t.Fatal("restore lost metadata", err)
	}
	e.Fragments = []string{"2,B"}
	e.SMSTS += 6
	raw, _ = json.Marshal(e)
	if err = r.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	m, err = r.Message(ctx, 1)
	if err != nil || m.Body != "AB" || !m.Complete || m.PartsReceived != 2 {
		t.Fatal("restored fragments did not continue", err)
	}
	for i := 0; i < 2; i++ {
		if err = r.Ingest(ctx, e, raw, true); err != nil {
			t.Fatal(err)
		}
	}
	var messages, receipts int
	if err = r.DB.QueryRow(`SELECT count(*) FROM messages`).Scan(&messages); err != nil || messages != 1 {
		t.Fatal("replay duplicated restored SMS", err)
	}
	if err = r.DB.QueryRow(`SELECT count(*) FROM sms_receipts`).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatal("accepted timestamps did not survive backup/restore", err)
	}
	blocked, err := r.RestoreBlocked(ctx)
	if err != nil || !blocked {
		t.Fatal("metadata continuation released outbound guard", err)
	}
}

func TestMultipartBackupRejectsMissingMetadataColumn(t *testing.T) {
	s := fixture(t)
	if _, err := s.DB.Exec(`DROP INDEX messages_sms_group; ALTER TABLE messages DROP COLUMN sms_total`); err != nil {
		t.Fatal(err)
	}
	if a, err := Create(context.Background(), s, t.TempDir()); err == nil {
		a.Close()
		t.Fatal("incomplete schema 16 accepted")
	}
}

func TestMultipartBackupRejectsMissingReceiptTableOrColumn(t *testing.T) {
	for _, statement := range []string{
		`DROP TABLE sms_receipts`,
		`ALTER TABLE sms_receipts DROP COLUMN sms_ts`,
	} {
		t.Run(statement, func(t *testing.T) {
			s := fixture(t)
			if _, err := s.DB.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if a, err := Create(t.Context(), s, t.TempDir()); err == nil {
				a.Close()
				t.Fatal("incomplete schema-17 receipt data accepted")
			}
		})
	}
}

func TestVersionSixteenBackupAndReceiptLinksAfterRestore(t *testing.T) {
	ctx := t.Context()
	s := fixture(t)
	no := false
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, ICCID: "SYNTHETIC-CARD", Phone: "TEST", SMSTS: 1700000000, Group: "SYNTHETIC-GROUP", Max: 2, Complete: &no, Fragments: []string{"1,A"}}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE IF EXISTS voicemail_controls; DROP TABLE IF EXISTS voicemails; DROP TABLE sms_receipts; DELETE FROM schema_version; INSERT INTO schema_version VALUES(16)`); err != nil {
		t.Fatal(err)
	}
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil || p.Manifest.Schema != 16 {
		t.Fatal("old backup was migrated while inspecting", err)
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
	e.SMSTS += 6
	e.Fragments = []string{"2,B"}
	raw, _ = json.Marshal(e)
	if err = r.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	newBackup, err := Create(ctx, r, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer newBackup.Close()
	preview, err := Inspect(ctx, newBackup.Path)
	if err != nil || preview.Manifest.Schema != store.SchemaVersion || preview.Manifest.Counts["sms_receipts"] != 1 {
		t.Fatal("upgraded receipt linkage missing from backup", err)
	}
	secondTarget := filepath.Join(t.TempDir(), "restored-again")
	if _, err = Restore(ctx, newBackup.Path, secondTarget, preview.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(filepath.Join(secondTarget, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.DB.Close()
	if err = second.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = second.DB.QueryRow(`SELECT count(*) FROM messages`).Scan(&count); err != nil || count != 1 {
		t.Fatal("restored alias replay duplicated a message", err)
	}
	if err = second.DeleteMessage(ctx, 1, 1700001000); err != nil {
		t.Fatal(err)
	}
	if err = second.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	messages, err := second.Messages(ctx, 0, "")
	if err != nil || len(messages) != 0 {
		t.Fatal("restored alias resurrected deleted history", err)
	}
}

func TestVersionFifteenOfflineBackupStaysOldAndRestoresToCurrent(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	no := false
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, ICCID: "SYNTHETIC-CARD", Phone: "TEST", SMSTS: 1700000000, Group: "SYNTHETIC-GROUP", Max: 2, Complete: &no, Fragments: []string{"1,A"}}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	var database string
	if err := s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE IF EXISTS voicemail_controls; DROP TABLE IF EXISTS voicemails; DROP TABLE sms_receipts; DROP INDEX messages_sms_group; ALTER TABLE messages DROP COLUMN sms_total; ALTER TABLE messages DROP COLUMN sms_group_ref; DELETE FROM schema_version; INSERT INTO schema_version VALUES(15)`); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "old.zip")
	if err := BackupDirectory(ctx, filepath.Dir(database), backup); err != nil {
		t.Fatal(err)
	}
	p, err := Inspect(ctx, backup)
	if err != nil || p.Manifest.Schema != 15 {
		t.Fatal("old backup migrated during creation or inspection", err)
	}
	source, err := databaseInfo(ctx, database)
	if err != nil || source.Schema != 15 {
		t.Fatal("read-only backup upgraded the original database", err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(ctx, backup, target, p.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	r, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.DB.Close()
	var schema int
	if err = r.DB.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&schema); err != nil || schema != store.SchemaVersion {
		t.Fatal("restored database did not upgrade", err)
	}
	m, err := r.Message(ctx, 1)
	if err != nil || m.Complete || m.PartsTotal != 0 || m.PartsReceived != 1 {
		t.Fatal("restore guessed the historical total", err)
	}
	blocked, err := r.RestoreBlocked(ctx)
	if err != nil || !blocked {
		t.Fatal("restore omitted its outbound guard", err)
	}
}
