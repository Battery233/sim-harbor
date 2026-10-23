package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/maintenance"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestManagedBackupDownloadRestoreAndSeparateAudio(t *testing.T) {
	for _, destination := range []string{"browser", "server"} {
		t.Run(destination, func(t *testing.T) {
			s, _ := fixture(t, 0, func(c *config.Config) {
				c.AdminToken = "SYNTHETIC-BACKUP-RESTORE-TOKEN"
				// Include a pending reminder to exercise restore protection.
				c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC-BARK", "https://bark.example.invalid", "https://example.invalid"
			})
			ctx := t.Context()
			connected := protocol.Number(1002)
			call := protocol.Event{DeviceID: s.Config.DeviceID, Type: 603, Slot: 1, Phone: "SYNTHETIC-CALLER", ICCID: "SYNTHETIC-CARD", TelStartTS: 1000, TelConnectedTS: &connected, TelEndTS: 1010}
			raw, _ := json.Marshal(call)
			if err := s.Store.Ingest(ctx, call, raw, false); err != nil {
				t.Fatal(err)
			}
			audio := []byte("#!AMR\nSYNTHETIC-SEPARATE-AUDIO")
			media, _, _, err := s.Store.SaveVoicemail(ctx, s.Config.DataDir, audio, 1, call.Phone, 1000, 1011)
			if err != nil {
				t.Fatal(err)
			}
			file := generateTestBackup(t, s, s.Config.AdminToken, strings.Repeat("d", 32), destination)
			w := backupAPI(s, "GET", "/backups/"+file.ID+"/download", "", s.Config.AdminToken)
			if w.Code != 200 || w.Body.Len() != int(file.Bytes) {
				t.Fatal("managed download failed", w.Code)
			}
			archive := filepath.Join(t.TempDir(), file.Name)
			if err := os.WriteFile(archive, w.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			preview, err := maintenance.Inspect(ctx, archive)
			if err != nil || preview.ArchiveSHA256 != file.SHA256 || preview.Manifest.Schema != store.SchemaVersion {
				t.Fatal("downloaded backup inspection", err)
			}
			target := filepath.Join(t.TempDir(), "restored")
			if _, err = maintenance.Restore(ctx, archive, target, preview.ArchiveSHA256); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(filepath.Join(target, "sim.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.DB.Close()
			cfg := s.Config
			cfg.DataDir = target
			restored := New(cfg, db)
			defer restored.CloseBackups()
			c, err := db.Call(ctx, 1, 1012)
			if err != nil || c.VoicemailMediaID != media || c.NotificationState != "restore_held" || c.VoicemailDeleted {
				t.Fatal("restored index or notification protection", c, err)
			}
			// The ZIP has indexes, not AMR. Restoring it alone must report missing audio.
			if w = backupAPI(restored, "GET", "/calls/1/voicemail", "", cfg.AdminToken); w.Code != 404 {
				t.Fatal("database ZIP unexpectedly restored AMR", w.Code)
			}
			if _, err = os.Stat(filepath.Join(target, "voicemail", media+".amr")); !os.IsNotExist(err) {
				t.Fatal("audio unexpectedly exists", err)
			}
			// Simulate separately restoring the original private voicemail directory.
			if err = os.MkdirAll(filepath.Join(target, "voicemail"), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(target, "voicemail", media+".amr"), audio, 0600); err != nil {
				t.Fatal(err)
			}
			w = backupAPI(restored, "GET", "/calls/1/voicemail", "", cfg.AdminToken)
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), audio) {
				t.Fatal("separate audio restore was not readable", w.Code)
			}
		})
	}
}

func backupAPI(s *Server, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.com/api"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func backupList(t *testing.T, s *Server, token string) []backupFile {
	t.Helper()
	w := backupAPI(s, "GET", "/backups", "", token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var result struct {
		Items []backupFile `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Items
}
func generateTestBackup(t *testing.T, s *Server, token, id, destination string) backupFile {
	t.Helper()
	w := backupAPI(s, "POST", "/backups", fmt.Sprintf(`{"id":%q,"destination":%q,"confirmed":true}`, id, destination), token)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	s.backups.wg.Wait()
	s.backups.mu.Lock()
	job := *s.backups.job
	s.backups.mu.Unlock()
	if job.State != "ready" {
		t.Fatal(job)
	}
	for _, f := range backupList(t, s, token) {
		if f.ID == id {
			return f
		}
	}
	t.Fatal("completed archive absent")
	return backupFile{}
}
func TestSavedBackupSurvivesCanceledRequestDownloadFailureAndRestart(t *testing.T) {
	s, token := restoredServer(t)
	id := strings.Repeat("a", 32)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "http://example.com/api/backups", strings.NewReader(fmt.Sprintf(`{"id":%q,"destination":"server","confirmed":true}`, id))).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	cancel() // Closing the page must not cancel generation.
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	s.backups.wg.Wait()
	files := backupList(t, s, token)
	if len(files) != 1 || files[0].ExpiresAt != 0 || !strings.Contains(files[0].Name, "T") || !strings.HasSuffix(files[0].Name, id+".zip") {
		t.Fatal(files)
	}
	f := files[0]
	path := filepath.Join(s.Config.DataDir, "backups", id, f.Name)
	if _, err := maintenance.Inspect(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 2 {
		t.Fatal("raw snapshot retained with archive", entries, err)
	}
	if s.migrationBackup == nil {
		t.Fatal("durably saved backup not recorded")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("archive permissions", info.Mode())
	}
	// A retry with the same creation ID must not generate another file.
	w = backupAPI(s, "POST", "/backups", fmt.Sprintf(`{"id":%q,"destination":"server","confirmed":true}`, id), token)
	if w.Code != 200 || len(backupList(t, s, token)) != 1 {
		t.Fatal("creation replay", w.Code)
	}
	r = httptest.NewRequest("GET", "http://example.com/api/backups/"+id+"/download", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	s.Handler().ServeHTTP(&brokenBackupWriter{header: http.Header{}}, r)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("interrupted download removed backup", err)
	}
	if len(backupList(t, s, token)) != 1 {
		t.Fatal("failed transfer lost backup")
	}
	w = backupAPI(s, "GET", "/backups/"+id+"/download", "", token)
	if w.Code != 200 || w.Body.Len() != int(f.Bytes) || !strings.Contains(w.Header().Get("Content-Disposition"), f.Name) {
		t.Fatal(w.Code, w.Header())
	}
	s.CloseBackups()
	next := New(s.Config, s.Store)
	t.Cleanup(next.CloseBackups)
	if len(backupList(t, next, token)) != 1 {
		t.Fatal("restart lost completed file")
	}
	if next.migrationBackup != nil {
		t.Fatal("restart reused migration evidence")
	}
}
func TestBackupCrashCleanupOnlyRemovesOwnedWorkAndExpiredTemporaryFiles(t *testing.T) {
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = strings.Repeat("t", 24) })
	token := s.Config.AdminToken
	persistent := generateTestBackup(t, s, token, strings.Repeat("a", 32), "server")
	temporary := generateTestBackup(t, s, token, strings.Repeat("b", 32), "browser")
	b := s.backups
	// Cleanup must skip a temporary file currently being downloaded.
	b.mu.Lock()
	b.downloads[temporary.ID] = 1
	files, err := b.filesLocked(time.Unix(temporary.ExpiresAt+1, 0))
	b.mu.Unlock()
	if err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
	b.mu.Lock()
	b.downloads[temporary.ID] = 0
	files, err = b.filesLocked(time.Unix(temporary.ExpiresAt+1, 0))
	b.mu.Unlock()
	if err != nil || len(files) != 1 || files[0].ID != persistent.ID {
		t.Fatal(files, err)
	}
	// Simulate an interrupted worker, incomplete status write, and old HTTP temp directory.
	pending := filepath.Join(s.Config.DataDir, "backups", ".pending-"+strings.Repeat("c", 32))
	if err = os.Mkdir(pending, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(pending, "incomplete.zip"), []byte("SYNTHETIC"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(s.Config.DataDir, ".sim-backup-12345678")
	if err = os.Mkdir(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(s.Config.DataDir, ".sim-backup-keep")
	if err = os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.job = &backupJob{ID: strings.Repeat("c", 32), Destination: "browser", State: "running"}
	err = b.saveJobLocked()
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(b.root.Name(), ".job.tmp"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	s.CloseBackups()
	next := New(s.Config, s.Store)
	t.Cleanup(next.CloseBackups)
	if len(backupList(t, next, token)) != 1 {
		t.Fatal("completed persistent backup lost")
	}
	if next.backups.job.State != "failed" || next.backups.job.Error != "backup_interrupted" {
		t.Fatal(next.backups.job)
	}
	for _, path := range []string{pending, legacy, filepath.Join(b.root.Name(), ".job.tmp")} {
		if _, err = os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("temporary file survived", path, err)
		}
	}
	if _, err = os.Stat(unrelated); err != nil {
		t.Fatal("unrelated file removed", err)
	}
}
func TestBackupConsentCapacityPathAndDeleteProtection(t *testing.T) {
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = strings.Repeat("t", 24) })
	token := s.Config.AdminToken
	if w := backupAPI(s, "GET", "/backups", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{}`, `{"confirmed":true,"id":"../sim.db","destination":"server"}`, `{"confirmed":true,"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","destination":"server","path":"/tmp"}`} {
		if w := backupAPI(s, "POST", "/backups", body, token); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	f := generateTestBackup(t, s, token, strings.Repeat("d", 32), "server")
	b := s.backups
	b.mu.Lock()
	b.limit = f.Bytes
	b.mu.Unlock()
	w := backupAPI(s, "POST", "/backups", `{"confirmed":true,"id":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","destination":"server"}`, token)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "backup_storage_full") {
		t.Fatal(w.Code, w.Body)
	}
	if len(backupList(t, s, token)) != 1 {
		t.Fatal("capacity refusal removed old file")
	}
	if w = backupAPI(s, "POST", "/backups/"+f.ID+"/delete", `{}`, token); w.Code != 400 {
		t.Fatal(w.Code)
	}
	b.mu.Lock()
	b.downloads[f.ID] = 1
	b.mu.Unlock()
	if w = backupAPI(s, "POST", "/backups/"+f.ID+"/delete", `{"confirmed":true}`, token); w.Code != 409 {
		t.Fatal(w.Code)
	}
	b.mu.Lock()
	b.downloads[f.ID] = 0
	b.mu.Unlock()
	if w = backupAPI(s, "POST", "/backups/"+f.ID+"/delete", `{"confirmed":true}`, token); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if len(backupList(t, s, token)) != 0 {
		t.Fatal("delete did not remove file")
	}
	if w = backupAPI(s, "GET", "/backups/"+f.ID+"/download", "", token); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if _, err := os.Stat(filepath.Join(s.Config.DataDir, "sim.db")); err != nil {
		t.Fatal("delete touched database", err)
	}
}
func TestCanceledBackupJobCleansPendingFilesAndCannotAuthorizeMigration(t *testing.T) {
	s, token := restoredServer(t)
	b, err := s.initBackups()
	if err != nil {
		t.Fatal(err)
	}
	b.cancel() // Deterministically interrupt generation, independently of HTTP cancellation.
	w := backupAPI(s, "POST", "/backups", `{"confirmed":true,"id":"ffffffffffffffffffffffffffffffff","destination":"browser"}`, token)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	b.wg.Wait()
	if b.job.State != "failed" || b.job.Error != "backup_interrupted" || s.migrationBackup != nil {
		t.Fatal(b.job, s.migrationBackup)
	}
	entries, err := os.ReadDir(b.root.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			t.Fatal("work leaked", entry.Name())
		}
	}
	if len(backupList(t, s, token)) != 0 {
		t.Fatal("partial backup exposed")
	}
}
func TestBrowserBackupInterruptedDownloadDoesNotAuthorizeMigration(t *testing.T) {
	s, token := restoredServer(t)
	f := generateTestBackup(t, s, token, strings.Repeat("e", 32), "browser")
	if s.migrationBackup != nil {
		t.Fatal("undownloaded browser backup authorized migration")
	}
	r := httptest.NewRequest("GET", "http://example.com/api/backups/"+f.ID+"/download", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	s.Handler().ServeHTTP(&brokenBackupWriter{header: http.Header{}}, r)
	if s.migrationBackup != nil {
		t.Fatal("incomplete download authorized migration")
	}
	if w := backupAPI(s, "GET", "/backups/"+f.ID+"/download", "", token); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if s.migrationBackup == nil {
		t.Fatal("completed current backup not recorded")
	}
	if w := backupAPI(s, "POST", "/backups/"+f.ID+"/delete", `{"confirmed":true}`, token); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if s.migrationBackup != nil {
		t.Fatal("deleted backup kept evidence")
	}
}
func TestBackupRepositoryRejectsSymlinksWithoutFollowingOrCleaningTargets(t *testing.T) {
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = strings.Repeat("t", 24) })
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("SYNTHETIC"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Config.DataDir, "backups")); err != nil {
		t.Fatal(err)
	}
	w := backupAPI(s, "GET", "/backups", "", s.Config.AdminToken)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("followed backup directory symlink", err)
	}
}

func TestBackupPublicationCrashRecoversReadyFileAndDeletionTombstone(t *testing.T) {
	s, token := restoredServer(t)
	f := generateTestBackup(t, s, token, strings.Repeat("a", 32), "server")
	b := s.backups
	b.mu.Lock()
	b.job.State = "running" // ZIP was published but completion status was not written.
	err := b.saveJobLocked()
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	tombstone := ".deleting-" + strings.Repeat("b", 32)
	if err = b.root.Mkdir(tombstone, 0700); err != nil {
		t.Fatal(err)
	}
	if err = b.root.WriteFile(filepath.Join(tombstone, "partial"), []byte("SYNTHETIC"), 0600); err != nil {
		t.Fatal(err)
	}
	s.CloseBackups()
	next := New(s.Config, s.Store)
	t.Cleanup(next.CloseBackups)
	files := backupList(t, next, token)
	if len(files) != 1 || files[0].ID != f.ID || next.backups.job.State != "ready" {
		t.Fatal(files, next.backups.job)
	}
	if _, err = next.backups.root.Stat(tombstone); !os.IsNotExist(err) {
		t.Fatal("interrupted deletion not cleaned", err)
	}
	if next.migrationBackup != nil {
		t.Fatal("restart reused persisted migration authorization")
	}
}

func TestBackupRejectsCrossOriginAndSymlinkArchive(t *testing.T) {
	s, token := restoredServer(t)
	r := httptest.NewRequest("GET", "http://example.com/api/backups", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "https://other.invalid")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	f := generateTestBackup(t, s, token, strings.Repeat("c", 32), "server")
	path := filepath.Join(s.Config.DataDir, "backups", f.ID, f.Name)
	target := filepath.Join(t.TempDir(), "private.zip")
	if err := os.WriteFile(target, make([]byte, f.Bytes), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	w = backupAPI(s, "GET", "/backups/"+f.ID+"/download", "", token)
	if w.Code != 404 {
		t.Fatal("symlink archive served", w.Code)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("target removed", err)
	}
}

func TestSavedBackupHEADOrCorruptionCannotAuthorizeMigration(t *testing.T) {
	s, token := restoredServer(t)
	f := generateTestBackup(t, s, token, strings.Repeat("d", 32), "browser")
	path := "/backups/" + f.ID + "/download"
	if w := backupAPI(s, "HEAD", path, "", token); w.Code != 405 {
		t.Fatal("HEAD treated as a download", w.Code)
	}
	if s.migrationBackup != nil {
		t.Fatal("HEAD authorized migration")
	}
	archive := filepath.Join(s.Config.DataDir, "backups", f.ID, f.Name)
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 0xff
	if err = os.WriteFile(archive, data, 0600); err != nil {
		t.Fatal(err)
	}
	if w := backupAPI(s, "GET", path, "", token); w.Code != 503 {
		t.Fatal("corrupted archive served", w.Code)
	}
	if s.migrationBackup != nil {
		t.Fatal("corrupted transfer authorized migration")
	}
	if _, err = os.Stat(archive); err != nil {
		t.Fatal("corrupted file removed without consent", err)
	}
}

func TestConcurrentBackupCreationWithSameIDProducesOneArchive(t *testing.T) {
	s, token := restoredServer(t)
	start := make(chan struct{})
	results := make(chan int, 12)
	for range 12 {
		go func() {
			<-start
			w := backupAPI(s, "POST", "/backups", `{"confirmed":true,"id":"abababababababababababababababab","destination":"server"}`, token)
			results <- w.Code
		}()
	}
	close(start)
	for range 12 {
		if code := <-results; code != 200 && code != 202 {
			t.Fatal(code)
		}
	}
	s.backups.wg.Wait()
	if len(backupList(t, s, token)) != 1 {
		t.Fatal("duplicate archive generated")
	}
	s.backupGate.Lock()
	w := backupAPI(s, "POST", "/backups", `{"confirmed":true,"id":"cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd","destination":"server"}`, token)
	s.backupGate.Unlock()
	if w.Code != 409 {
		t.Fatal("concurrent generation allowed", w.Code)
	}
}

func TestBackupStatusWriteFailureRetainsExistingArchive(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires non-root filesystem permissions")
	}
	s, token := restoredServer(t)
	f := generateTestBackup(t, s, token, strings.Repeat("e", 32), "server")
	directory := s.backups.root.Name()
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(directory, 0700)
	w := backupAPI(s, "POST", "/backups", `{"confirmed":true,"id":"ffffffffffffffffffffffffffffffff","destination":"server"}`, token)
	if w.Code != 503 {
		t.Fatal("write error reported success", w.Code)
	}
	if files := backupList(t, s, token); len(files) != 1 || files[0].ID != f.ID {
		t.Fatal("existing archive lost", files)
	}
}

func TestManagedRecordingBackupBothDestinationsAndRestore(t *testing.T) {
	for _, destination := range []string{"browser", "server"} {
		t.Run(destination, func(t *testing.T) {
			s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = "SYNTHETIC-MANAGED-AUDIO-TOKEN" })
			audio := []byte("#!AMR\nSYNTHETIC-MANAGED-AUDIO")
			media, _, _, err := s.Store.SaveVoicemail(t.Context(), s.Config.DataDir, audio, 0, "", 0, 1000)
			if err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("c", 32)
			body := fmt.Sprintf(`{"id":%q,"destination":%q,"confirmed":true,"include_recordings":true}`, id, destination)
			w := backupAPI(s, "POST", "/backups", body, s.Config.AdminToken)
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body)
			}
			s.backups.wg.Wait()
			files := backupList(t, s, s.Config.AdminToken)
			if len(files) != 1 || !files[0].IncludeRecordings || files[0].RecordingFiles != 1 {
				t.Fatal(files)
			}
			w = backupAPI(s, "POST", "/backups", fmt.Sprintf(`{"id":%q,"destination":%q,"confirmed":true,"include_recordings":false}`, id, destination), s.Config.AdminToken)
			if w.Code != 409 {
				t.Fatal("changed replay options accepted", w.Code)
			}
			w = backupAPI(s, "GET", "/backups/"+id+"/download", "", s.Config.AdminToken)
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			path := filepath.Join(t.TempDir(), "saved.zip")
			os.WriteFile(path, w.Body.Bytes(), 0600)
			preview, err := maintenance.Inspect(t.Context(), path)
			if err != nil || preview.Manifest.Format != 2 {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "restored")
			if _, err = maintenance.Restore(t.Context(), path, target, preview.ArchiveSHA256); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(target, "voicemail", media+".amr"))
			if err != nil || !bytes.Equal(got, audio) {
				t.Fatal("managed audio roundtrip", err)
			}
		})
	}
}
