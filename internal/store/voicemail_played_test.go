package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlaybackMigrationFromTwentyAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	media, _, _, err := s.SaveVoicemail(t.Context(), dir, []byte("#!AMR\nSYNTHETIC"), 0, "", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`DROP TABLE system_notifications; ALTER TABLE status_alert_jobs DROP COLUMN created_at; ALTER TABLE voicemails DROP COLUMN played_at; DELETE FROM schema_version; INSERT INTO schema_version VALUES(20)`); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version, played int64
	if err = s.DB.QueryRow(`SELECT (SELECT version FROM schema_version), played_at FROM voicemails WHERE media_id=?`, media).Scan(&version, &played); err != nil || version != SchemaVersion || played != 0 {
		t.Fatal(version, played, err)
	}
	if at, e := s.MarkVoicemailPlayed(t.Context(), media, 200); e != nil || at != 200 {
		t.Fatal(at, e)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if at, e := s.MarkVoicemailPlayed(t.Context(), media, 300); e != nil || at != 200 {
		t.Fatal("restart reset playback", at, e)
	}
}

func TestRecordingSnapshotPinsFilesAcrossLaterDeletion(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	dir := t.TempDir()
	audio := []byte("#!AMR\nSYNTHETIC-PIN")
	media, _, _, err := s.SaveVoicemail(t.Context(), dir, audio, 0, "", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := t.TempDir()
	pinned := filepath.Join(snapshot, "voicemail")
	records, err := s.SnapshotWithRecordings(t.Context(), filepath.Join(snapshot, "sim.db"), dir, pinned)
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	if err = s.DeleteVoicemail(t.Context(), dir, media); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "voicemail", media+".amr")); !os.IsNotExist(err) {
		t.Fatal("live file remains", err)
	}
	b, err := os.ReadFile(filepath.Join(pinned, media+".amr"))
	if err != nil || string(b) != string(audio) {
		t.Fatal("snapshot lost audio", err)
	}
}

func TestNotificationCountsIncludeStatusAlerts(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	if _, err := s.DB.Exec(`INSERT INTO status_alert_monitors(key) VALUES('board'); INSERT INTO status_alert_jobs(key,episode,kind,attempts) VALUES('board',1,'fault',2)`); err != nil {
		t.Fatal(err)
	}
	state, err := s.State(t.Context(), "SIM-TEST")
	if err != nil || state["notifications_pending"] != 1 || state["notifications_retrying"] != 1 {
		t.Fatal(state, err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT count(*) FROM status_alert_jobs WHERE state='pending'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("alert delivery changed", count, err)
	}
}
