package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestorePublicationCannotReplaceAnUnexpectedDatabase(t *testing.T) {
	dir := t.TempDir()
	pending, target := filepath.Join(dir, ".sim.db.pending"), filepath.Join(dir, "sim.db")
	if err := os.WriteFile(pending, []byte("new synthetic snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("preserve unexpected database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishDatabase(pending, target); !errors.Is(err, os.ErrExist) {
		t.Fatal("publication replaced existing file", err)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "preserve unexpected database" {
		t.Fatal("original changed", err)
	}
	if _, err := os.Stat(pending); err != nil {
		t.Fatal("failed publication discarded pending snapshot", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := publishDatabase(pending, target); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(target)
	if err != nil || string(b) != "new synthetic snapshot" {
		t.Fatal("completed snapshot not published", err)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("pending file left after publication", err)
	}
}
