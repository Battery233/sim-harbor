package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseAndWALArePrivateBeforeFirstWriteAndAfterReopen(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sim.db")
	s := open(t, path)
	ingest(t, s, sms())
	for pass := 0; pass < 2; pass++ {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			info, err := os.Stat(path + suffix)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("database or sidecar not private", suffix, info, err)
			}
			if err := os.Chmod(path+suffix, 0644); err != nil {
				t.Fatal(err)
			}
		}
		if pass == 0 {
			s.DB.Close()
			s = open(t, path)
			ingest(t, s, sms())
		}
	}
}

func TestPendingRestoreAndDatabaseSymlinkRefuseStartup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sim.db")
	if err := os.WriteFile(filepath.Join(dir, ".sim.db.pending"), []byte("synthetic incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("pending restoration opened")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("incomplete restoration created database", err)
	}
	dir = t.TempDir()
	original := filepath.Join(dir, "original")
	if err := os.WriteFile(original, []byte("preserve synthetic file"), 0600); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "sim.db")
	if err := os.Symlink(original, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("symlink database accepted")
	}
	b, err := os.ReadFile(original)
	if err != nil || string(b) != "preserve synthetic file" {
		t.Fatal("symlink target changed", err)
	}
}
