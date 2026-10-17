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
	if err != nil || p.Manifest.Schema != 16 {
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
	raw, _ = json.Marshal(e)
	if err = r.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	m, err = r.Message(ctx, 1)
	if err != nil || m.Body != "AB" || !m.Complete || m.PartsReceived != 2 {
		t.Fatal("restored fragments did not continue", err)
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

func TestVersionFifteenOfflineBackupStaysOldAndRestoresToSixteen(t *testing.T) {
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
	if _, err := s.DB.Exec(`DROP INDEX messages_sms_group; ALTER TABLE messages DROP COLUMN sms_total; ALTER TABLE messages DROP COLUMN sms_group_ref; DELETE FROM schema_version; INSERT INTO schema_version VALUES(15)`); err != nil {
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
	if err = r.DB.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&schema); err != nil || schema != 16 {
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
