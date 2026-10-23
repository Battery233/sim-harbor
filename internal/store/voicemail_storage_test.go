package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVoicemailStorageCountsDeletedAndDuplicateRecords(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	ctx := t.Context()
	audio := []byte("#!AMR\nSYNTHETIC")
	media, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, "SYNTHETIC", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, duplicate, err := s.SaveVoicemail(ctx, dir, audio, 1, "SYNTHETIC", 10, 21); err != nil || !duplicate {
		t.Fatal(err)
	}
	if err = s.DeleteVoicemail(ctx, dir, media); err != nil {
		t.Fatal(err)
	}
	stats, err := s.VoicemailStorage(ctx, dir)
	if err != nil || !stats.Complete || stats.Files != 0 || stats.Bytes != 0 || stats.IndexCount != 1 || stats.DeletedCount != 1 {
		t.Fatal(stats, err)
	}
	if _, _, _, err = s.SaveVoicemail(ctx, dir, audio, 1, "SYNTHETIC", 11, 22); err != nil {
		t.Fatal(err)
	}
	stats, err = s.VoicemailStorage(ctx, dir)
	if err != nil || stats.Files != 1 || stats.IndexCount != 2 || stats.DeletedCount != 1 || stats.Bytes != int64(len(audio)) {
		t.Fatal(stats, err)
	}
}

func TestVoicemailStorageStopsAtDirectoryBoundaryAndExplainsIndexLimit(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	if _, err := s.DB.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000) INSERT INTO voicemails(media_id,sha256,size,stored_name,created_at) SELECT printf('%032x',x),printf('%064x',x),1,'',1 FROM n`); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := s.SaveVoicemail(t.Context(), dir, []byte("#!AMR\nSYNTHETIC"), 1, "", 1, 2, 16<<20)
	var limit *VoicemailCapacityError
	if !errors.As(err, &limit) || limit.Reason != "records" {
		t.Fatal("index protection lost", err)
	}
	audioDir := filepath.Join(dir, "voicemail")
	if err = os.Mkdir(audioDir, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= VoicemailDirectoryLimit; i++ {
		file, err := os.CreateTemp(audioDir, "synthetic-")
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	stats, err := s.VoicemailStorage(t.Context(), dir)
	if err != nil || stats.Complete || stats.Files != VoicemailDirectoryLimit+1 || stats.IndexCount != VoicemailIndexLimit {
		t.Fatal(stats, err)
	}
}
