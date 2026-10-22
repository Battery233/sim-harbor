package maintenance

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/store"
)

func TestVoicemailBackupRestoresIndexNotAudio(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().Unix()
	audio := []byte("#!AMR\nSYNTHETIC")
	if _, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, "15151515", 11000, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO calls(key,device_id,card_name,slot,phone,start_ts,state,received_at) VALUES('backup-call','SIM-TEST','',1,'15151515',11000,'ringing',?)`, now); err != nil {
		t.Fatal(err)
	}
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	archive, err := zip.OpenReader(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if filepath.Ext(file.Name) == ".amr" {
			t.Fatal("zip included an AMR file", file.Name)
		}
	}
	preview, err := Inspect(ctx, a.Path)
	if err != nil || preview.Manifest.Schema != store.SchemaVersion || preview.Manifest.Counts["voicemails"] != 1 {
		t.Fatal(err, preview.Manifest)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(ctx, a.Path, target, preview.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.DB.Close()
	var callID int64
	var name string
	if err = restored.DB.QueryRow(`SELECT call_id, stored_name FROM voicemails`).Scan(&callID, &name); err != nil || callID == 0 || name == "" {
		t.Fatal("restore did not link the index", callID, name, err)
	}
	var state string
	var notes int
	if err = restored.DB.QueryRow(`SELECT count(*), coalesce(max(state),'') FROM call_notifications WHERE call_id=?`, callID).Scan(&notes, &state); err != nil || notes != 1 || state != "restore_held" {
		t.Fatal("restore notification", notes, state, err)
	}
	if _, err = os.Stat(filepath.Join(target, "voicemail", name)); !os.IsNotExist(err) {
		t.Fatal("zip restore recreated the recording", err)
	}
	if err = os.MkdirAll(filepath.Join(target, "voicemail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(target, "voicemail", name), audio, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := restored.VoicemailFile(ctx, callID)
	if err != nil || file.Name != name {
		t.Fatal(file, err)
	}
	got, err := os.ReadFile(filepath.Join(target, "voicemail", file.Name))
	if err != nil || string(got) != string(audio) {
		t.Fatal("separate audio restore", err)
	}
}

func TestVoicemailDeletionTombstoneSurvivesBackupRestore(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	dir := t.TempDir()
	audio := []byte("#!AMR\nSYNTHETIC-DELETED")
	media, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, "SYNTHETIC-CALLER", 11000, 12000)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteVoicemail(ctx, dir, media); err != nil {
		t.Fatal(err)
	}
	archive, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	preview, err := Inspect(ctx, archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(ctx, archive.Path, target, preview.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.DB.Close()
	got, _, duplicate, err := restored.SaveVoicemail(ctx, target, audio, 1, "SYNTHETIC-CALLER", 11000, 13000)
	if err != nil || !duplicate || got != media {
		t.Fatal("restored deletion dedup failed", err)
	}
	items, err := restored.UnlinkedVoicemails(ctx, 0)
	if err != nil || len(items) != 0 {
		t.Fatal("restored tombstone visible", err)
	}
	if _, err = os.Stat(filepath.Join(target, "voicemail", media+".amr")); !os.IsNotExist(err) {
		t.Fatal("restored deleted audio", err)
	}
}

func TestDelayedVoicemailNotificationsRestoreHeldWithoutReplay(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	now := time.Now().Unix()
	if _, err := s.DB.Exec(`INSERT INTO calls(key,device_id,slot,phone,start_ts,connected_ts,end_ts,ended,state,received_at,voicemail_due_at) VALUES('SYNTHETIC-DELAY','SIM-TEST',1,'anonymous',?,?,?,?, 'answered',?,?);
 INSERT INTO call_notifications(call_id,card_name,group_name,state,kind,accepted_at,attempted_at,created_at) VALUES(last_insert_rowid(),'SYNTHETIC','SYNTHETIC','sent','voicemail_missing',?,?,?);`, now-200, now-199, now-190, 1, now, now-10, now-5, now-5, now-10); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SaveVoicemail(ctx, t.TempDir(), []byte("#!AMR\nSYNTHETIC"), 1, "anonymous", now-200, now); err != nil {
		t.Fatal(err)
	}
	archive, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	preview, err := Inspect(ctx, archive.Path)
	if err != nil || preview.Manifest.Schema != store.SchemaVersion || preview.Manifest.Counts["call_notifications"] != 2 {
		t.Fatal("delayed ledger not backed up", preview.Manifest, err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(ctx, archive.Path, target, preview.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.DB.Close()
	var first, second string
	var due int64
	restored.DB.QueryRow(`SELECT state FROM call_notifications WHERE phase=0`).Scan(&first)
	restored.DB.QueryRow(`SELECT state FROM call_notifications WHERE phase=1`).Scan(&second)
	restored.DB.QueryRow(`SELECT voicemail_due_at FROM calls WHERE key='SYNTHETIC-DELAY'`).Scan(&due)
	if first != "sent" || second != "restore_held" || due != -1 {
		t.Fatal("restored old notifications rearmed", first, second, due)
	}
}
