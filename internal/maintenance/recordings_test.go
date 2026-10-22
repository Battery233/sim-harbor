package maintenance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battery233/sim-harbor/internal/store"
)

func audioFixture(t *testing.T) (*store.Store, string, string, string, []byte) {
	t.Helper()
	s := fixture(t)
	dir := t.TempDir()
	audio := []byte("#!AMR\nSYNTHETIC-RECORDING")
	if _, err := s.DB.Exec(`INSERT INTO calls(key,device_id,slot,phone,start_ts,state,received_at) VALUES('A','SIM-TEST',1,'SYNTHETIC-CALLER',1000,'ringing',1000)`); err != nil {
		t.Fatal(err)
	}
	a, _, _, err := s.SaveVoicemail(t.Context(), dir, audio, 1, "SYNTHETIC-CALLER", 1000, 1001)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _, err := s.SaveVoicemail(t.Context(), dir, []byte("#!AMR\nSYNTHETIC-UNLINKED"), 0, "", 0, 1002)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, a, b, audio
}

func TestRecordingBackupRestoresLinkedUnlinkedAndSharedPlayback(t *testing.T) {
	s, dir, a, b, audio := audioFixture(t)
	if _, err := s.MarkVoicemailPlayed(t.Context(), a, 1100); err != nil {
		t.Fatal(err)
	}
	orphan := strings.Repeat("e", 32) + ".amr"
	if err := os.WriteFile(filepath.Join(dir, "voicemail", orphan), audio, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "voicemail", "upload.partial"), []byte("INCOMPLETE"), 0600); err != nil {
		t.Fatal(err)
	}
	artifact, err := Create(t.Context(), s, t.TempDir(), BackupOptions{IncludeRecordings: true, DataDirectory: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	preview, err := Inspect(t.Context(), artifact.Path)
	if err != nil || preview.Manifest.Format != 2 || !preview.Manifest.IncludesRecordings || len(preview.Manifest.Recordings) != 3 {
		t.Fatal(preview, err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err = Restore(t.Context(), artifact.Path, target, preview.ArchiveSHA256); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(target, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	c, err := db.Call(t.Context(), 1, 1200)
	if err != nil || c.VoicemailMediaID != a || c.VoicemailPlayedAt != 1100 || c.NotificationState != "restore_held" {
		t.Fatal(c, err)
	}
	unlinked, err := db.UnlinkedVoicemails(t.Context(), 0)
	if err != nil || len(unlinked) != 1 || unlinked[0].StoredName != b+".amr" {
		t.Fatal(unlinked, err)
	}
	for _, name := range []string{a + ".amr", b + ".amr", orphan} {
		got, e := os.ReadFile(filepath.Join(target, "voicemail", name))
		if e != nil || !bytes.HasPrefix(got, []byte("#!AMR\n")) {
			t.Fatal("audio missing", name, e)
		}
		st, e := os.Stat(filepath.Join(target, "voicemail", name))
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("permissions", e)
		}
	}
	if _, err = os.Stat(filepath.Join(target, "voicemail", "upload.partial")); !os.IsNotExist(err) {
		t.Fatal("partial restored", err)
	}
	if _, err = Restore(t.Context(), artifact.Path, target, preview.ArchiveSHA256); err == nil {
		t.Fatal("existing target overwritten")
	}
}

func TestRecordingBackupMissingCorruptDeletedSymlinkAndCanceled(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "symlink", "deleted", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			s, dir, a, _, audio := audioFixture(t)
			path := filepath.Join(dir, "voicemail", a+".amr")
			ctx := t.Context()
			switch mode {
			case "missing":
				os.Remove(path)
			case "corrupt":
				os.WriteFile(path, []byte("#!AMR\nCORRUPT"), 0600)
			case "symlink":
				os.Remove(path)
				other := filepath.Join(t.TempDir(), "other.amr")
				os.WriteFile(other, audio, 0600)
				os.Symlink(other, path)
			case "deleted":
				if err := s.DeleteVoicemail(ctx, dir, a); err != nil {
					t.Fatal(err)
				}
				os.WriteFile(path, audio, 0600)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			parent := t.TempDir()
			artifact, err := Create(ctx, s, parent, BackupOptions{IncludeRecordings: true, DataDirectory: dir})
			if mode == "deleted" {
				if err != nil {
					t.Fatal(err)
				}
				defer artifact.Close()
				for _, r := range artifact.Manifest.Recordings {
					if r.Name == a+".amr" {
						t.Fatal("deleted audio resurrected")
					}
				}
			} else {
				if err == nil {
					artifact.Close()
					t.Fatal("invalid audio accepted")
				}
				entries, e := os.ReadDir(parent)
				if e != nil || len(entries) != 0 {
					t.Fatal("failed work retained", entries, e)
				}
			}
		})
	}
}

func TestRecordingArchiveRejectsTamperingMissingDuplicateAndTraversal(t *testing.T) {
	s, dir, _, _, _ := audioFixture(t)
	artifact, err := Create(t.Context(), s, t.TempDir(), BackupOptions{IncludeRecordings: true, DataDirectory: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	for _, mode := range []string{"audio", "path", "manifest", "missing", "duplicate", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			input, e := zip.OpenReader(artifact.Path)
			if e != nil {
				t.Fatal(e)
			}
			defer input.Close()
			bad := filepath.Join(t.TempDir(), "bad.zip")
			file, e := os.Create(bad)
			if e != nil {
				t.Fatal(e)
			}
			z := zip.NewWriter(file)
			altered := false
			for _, entry := range input.File {
				f, e := entry.Open()
				if e != nil {
					t.Fatal(e)
				}
				data, e := io.ReadAll(f)
				f.Close()
				if e != nil {
					t.Fatal(e)
				}
				name := entry.Name
				if strings.HasPrefix(name, "voicemail/") && !altered {
					altered = true
					switch mode {
					case "audio":
						data = append(data, '!')
					case "path":
						name = "../" + name
					case "missing":
						continue
					}
				}
				if mode == "manifest" && name == "manifest.json" {
					var m Manifest
					json.Unmarshal(data, &m)
					m.Recordings[0].SHA256 = strings.Repeat("0", 64)
					data, _ = json.Marshal(m)
				}
				h := &zip.FileHeader{Name: name, Method: zip.Store}
				h.SetMode(0600)
				if mode == "symlink" && strings.HasPrefix(name, "voicemail/") {
					h.SetMode(os.ModeSymlink | 0600)
				}
				out, e := z.CreateHeader(h)
				if e != nil {
					t.Fatal(e)
				}
				out.Write(data)
				if mode == "duplicate" && strings.HasPrefix(name, "voicemail/") {
					copyHeader := *h
					out, e = z.CreateHeader(&copyHeader)
					if e != nil {
						t.Fatal(e)
					}
					out.Write(data)
				}
			}
			z.Close()
			file.Close()
			if _, err := Inspect(t.Context(), bad); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			target := filepath.Join(t.TempDir(), "untouched")
			if _, err := Restore(t.Context(), bad, target, strings.Repeat("0", 64)); err == nil {
				t.Fatal("unsafe restore")
			}
			if _, e := os.Stat(target); !os.IsNotExist(e) {
				t.Fatal("target changed before validation", e)
			}
		})
	}
}

// Cancel as soon as restore starts its audio phase, without timing-sensitive sleeps.
type cancelAtAudioContext struct {
	context.Context
	directory string
	cancel    context.CancelFunc
}

func (c cancelAtAudioContext) Err() error {
	if st, err := os.Stat(c.directory); err == nil && st.IsDir() {
		c.cancel()
	}
	return c.Context.Err()
}

func TestRecordingRestoreInterruptedTargetIsNotStartable(t *testing.T) {
	s, dir, _, _, _ := audioFixture(t)
	artifact, err := Create(t.Context(), s, t.TempDir(), BackupOptions{IncludeRecordings: true, DataDirectory: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	target := filepath.Join(t.TempDir(), "interrupted")
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := cancelAtAudioContext{Context: base, directory: filepath.Join(target, "voicemail"), cancel: cancel}
	if _, err = Restore(ctx, artifact.Path, target, artifact.SHA256); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(target, ".restore-incomplete")); err != nil {
		t.Fatal("lost incomplete marker", err)
	}
	if _, err = os.Stat(filepath.Join(target, "sim.db")); !os.IsNotExist(err) {
		t.Fatal("published incomplete DB", err)
	}
	if db, e := store.Open(filepath.Join(target, "sim.db")); e == nil {
		db.DB.Close()
		t.Fatal("incomplete target started")
	}
	if _, err = Inspect(t.Context(), artifact.Path); err != nil {
		t.Fatal("original backup changed", err)
	}
	fresh := filepath.Join(t.TempDir(), "complete")
	if _, err = Restore(t.Context(), artifact.Path, fresh, artifact.SHA256); err != nil {
		t.Fatal("fresh recovery failed", err)
	}
}
