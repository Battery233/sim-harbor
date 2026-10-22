package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/recording"
)

func TestVoicemailInsertFailureReleasesConnectionAndCleansFile(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_recording BEFORE INSERT ON voicemails BEGIN SELECT RAISE(FAIL, 'synthetic_insert_failure'); END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	audio := []byte(recording.Header + "SYNTHETIC")
	_, _, _, err := s.SaveVoicemail(ctx, dir, audio, 0, "", 0, 100)
	if err == nil || ctx.Err() != nil || s.DB.Stats().WaitCount != 0 {
		t.Fatal("failed insert waited for its own connection", err, ctx.Err(), s.DB.Stats().WaitCount)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "voicemail"))
	if err != nil || len(entries) != 0 {
		t.Fatal("failed insert left a recording", err)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER fail_recording`); err != nil {
		t.Fatal("failed transaction retained the only connection", err)
	}
	media, _, duplicate, err := s.SaveVoicemail(t.Context(), dir, audio, 0, "", 0, 101)
	if err != nil || duplicate || media == "" {
		t.Fatal("write could not recover", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "voicemail", media+".amr"))
	if err != nil || string(got) != string(audio) {
		t.Fatal("saved file does not match its index", err)
	}
}

func TestVoicemailRejectsInvalidAudioWithoutCreatingFiles(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	for _, audio := range []string{"", "#!AMR", "#!AMR-WB\nSYNTHETIC", "#!AMRX\nSYNTHETIC"} {
		if _, _, _, err := s.SaveVoicemail(t.Context(), dir, []byte(audio), 0, "", 0, 100); !errors.Is(err, recording.ErrInvalid) {
			t.Fatal("invalid format was not rejected", err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "voicemail")); !os.IsNotExist(err) {
		t.Fatal("invalid audio created a directory", err)
	}
	var rows int
	if err := s.DB.QueryRow(`SELECT count(*) FROM voicemails`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("invalid audio created an index", rows, err)
	}
}

func TestVoicemailStorageFailureDoesNotCreateIndex(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	path := filepath.Join(dir, "voicemail")
	if err := os.WriteFile(path, []byte("SYNTHETIC-BLOCKER"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SaveVoicemail(t.Context(), dir, []byte(recording.Header+"SYNTHETIC"), 0, "", 0, 100); err == nil {
		t.Fatal("storage failure reported success")
	}
	var rows int
	if err := s.DB.QueryRow(`SELECT count(*) FROM voicemails`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("storage failure created an index", rows, err)
	}
}
