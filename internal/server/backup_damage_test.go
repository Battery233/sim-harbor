package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDamagedBackupDoesNotDisableRepositoryAfterRestart(t *testing.T) {
	for _, damage := range []string{"truncated", "metadata", "metadata_digest"} {
		t.Run(damage, func(t *testing.T) {
			s, token := restoredServer(t)
			bad := generateTestBackup(t, s, token, strings.Repeat("a", 32), "server")
			good := generateTestBackup(t, s, token, strings.Repeat("b", 32), "server")
			dir := filepath.Join(s.Config.DataDir, "backups", bad.ID)
			var err error
			switch damage {
			case "truncated":
				err = os.Truncate(filepath.Join(dir, bad.Name), 1)
			case "metadata":
				err = os.WriteFile(filepath.Join(dir, "metadata.json"), []byte("{}"), 0600)
			case "metadata_digest":
				bad.SHA256 = strings.Repeat("z", 64)
				data, marshalErr := json.Marshal(bad)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				err = os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			s.CloseBackups()
			next := New(s.Config, s.Store)
			defer next.CloseBackups()
			files := backupList(t, next, token)
			var damaged backupFile
			for _, item := range files {
				if item.ID == bad.ID {
					damaged = item
				}
			}
			if len(files) != 2 || !damaged.Unavailable || damaged.SizeUnknown || damaged.Bytes <= 0 {
				t.Fatal("damaged item not isolated or counted", files)
			}
			if w := backupAPI(next, "GET", "/backups/"+good.ID+"/download", "", token); w.Code != 200 {
				t.Fatal("healthy download blocked", w.Code)
			}
			generateTestBackup(t, next, token, strings.Repeat("c", 32), "server")
			if w := backupAPI(next, "POST", "/backups/"+bad.ID+"/delete", `{"confirmed":false}`, token); w.Code != 400 {
				t.Fatal("unconfirmed damaged deletion", w.Code)
			}
			if _, err = os.Stat(dir); err != nil {
				t.Fatal("damaged item removed automatically", err)
			}
			if w := backupAPI(next, "POST", "/backups/"+bad.ID+"/delete", `{"confirmed":true}`, token); w.Code != 200 {
				t.Fatal("damaged deletion", w.Code)
			}
			if len(backupList(t, next, token)) != 2 {
				t.Fatal("other backups changed")
			}
		})
	}
}

func TestUnknownDamagedBackupSpaceBlocksCreationWithoutFollowingSymlinks(t *testing.T) {
	s, token := restoredServer(t)
	good := generateTestBackup(t, s, token, strings.Repeat("a", 32), "server")
	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("SYNTHETIC"), 0600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 32)
	if err := os.Symlink(outside, filepath.Join(s.Config.DataDir, "backups", id)); err != nil {
		t.Fatal(err)
	}
	files := backupList(t, s, token)
	var unknown bool
	for _, f := range files {
		if f.ID == id {
			unknown = f.Unavailable && f.SizeUnknown
		}
	}
	if !unknown {
		t.Fatal("unknown size was counted as empty")
	}
	if w := backupAPI(s, "POST", "/backups", `{"id":"cccccccccccccccccccccccccccccccc","confirmed":true,"destination":"server"}`, token); w.Code != 503 {
		t.Fatal("unknown quota allowed creation", w.Code)
	}
	if w := backupAPI(s, "GET", "/backups/"+good.ID+"/download", "", token); w.Code != 200 {
		t.Fatal("healthy download blocked", w.Code)
	}
	if w := backupAPI(s, "POST", "/backups/"+id+"/delete", `{"confirmed":true}`, token); w.Code != 200 {
		t.Fatal("confined deletion", w.Code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("followed a symlink while deleting", err)
	}
	generateTestBackup(t, s, token, strings.Repeat("c", 32), "server")
}

func TestUnreadableBackupDownloadIsIsolated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses owner read permissions")
	}
	s, token := restoredServer(t)
	bad := generateTestBackup(t, s, token, strings.Repeat("a", 32), "server")
	good := generateTestBackup(t, s, token, strings.Repeat("b", 32), "server")
	path := filepath.Join(s.Config.DataDir, "backups", bad.ID, bad.Name)
	if err := os.Chmod(path, 0200); err != nil {
		t.Fatal(err)
	}
	if w := backupAPI(s, "GET", "/backups/"+bad.ID+"/download", "", token); w.Code != 503 {
		t.Fatal("unreadable download", w.Code)
	}
	for _, file := range backupList(t, s, token) {
		if file.ID == bad.ID && !file.Unavailable {
			t.Fatal("failed read still shown as ready")
		}
	}
	if w := backupAPI(s, "GET", "/backups/"+good.ID+"/download", "", token); w.Code != 200 {
		t.Fatal("healthy download blocked", w.Code)
	}
	generateTestBackup(t, s, token, strings.Repeat("c", 32), "server")
}

func TestDetectedBackupCorruptionInvalidatesExistingMigrationPreview(t *testing.T) {
	for _, damage := range []string{"truncated", "digest"} {
		t.Run(damage, func(t *testing.T) {
			s, token := restoredServer(t)
			f := generateTestBackup(t, s, token, strings.Repeat("d", 32), "server")
			w := migrationCall(s, "/migration/preview", "{"+migrationAcknowledgements+"}", token, "")
			if w.Code != 200 {
				t.Fatal("preview", w.Code)
			}
			var preview struct {
				Token string `json:"token"`
			}
			json.Unmarshal(w.Body.Bytes(), &preview)
			path := filepath.Join(s.Config.DataDir, "backups", f.ID, f.Name)
			if damage == "truncated" {
				if err := os.Truncate(path, 1); err != nil {
					t.Fatal(err)
				}
				if w = backupAPI(s, "GET", "/backups/"+f.ID+"/download", "", token); w.Code != 404 {
					t.Fatal("truncated archive served", w.Code)
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data[0] ^= 0xff
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				if w = backupAPI(s, "GET", "/backups/"+f.ID+"/download", "", token); w.Code != 503 {
					t.Fatal("bad digest served", w.Code)
				}
			}
			body := `{"token":"` + preview.Token + `","confirmed":true,` + migrationAcknowledgements + `}`
			if w = migrationCall(s, "/migration/confirm", body, token, ""); w.Code != 409 {
				t.Fatal("invalid backup authorized migration", w.Code)
			}
			files := backupList(t, s, token)
			if len(files) != 1 || !files[0].Unavailable {
				t.Fatal("damaged item still ready", files)
			}
			state, err := s.Store.MigrationState(t.Context())
			if err != nil || !state.Blocked {
				t.Fatal("recovery guard changed", err)
			}
		})
	}
}
