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
