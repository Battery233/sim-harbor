package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestVoicemailCapacityPreservesDuplicatesAndFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	audio := []byte("#!AMR\nSYNTHETIC")
	id, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, "", 10, 20, int64(len(audio)))
	if err != nil {
		t.Fatal(err)
	}
	again, _, duplicate, err := s.SaveVoicemail(ctx, dir, audio, 1, "", 10, 21, 1)
	if err != nil || !duplicate || again != id {
		t.Fatal("duplicate rejected at capacity", err)
	}
	if _, _, _, err = s.SaveVoicemail(ctx, dir, audio, 1, "", 11, 22, int64(len(audio))); !errors.Is(err, ErrVoicemailCapacity) {
		t.Fatal("quota bypass", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "voicemail"))
	if len(entries) != 1 {
		t.Fatal("capacity rejection changed files")
	}
	if err = os.WriteFile(filepath.Join(dir, "voicemail", "orphan.partial"), audio, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.SaveVoicemail(ctx, dir, audio, 1, "", 12, 22, int64(len(audio)*2)); !errors.Is(err, ErrVoicemailCapacity) {
		t.Fatal("unindexed bytes ignored", err)
	}
}

func TestVoicemailCapacitySerializesConcurrentUploads(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	audio := []byte("#!AMR\nSYNTHETIC")
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := range 8 {
		wg.Go(func() {
			_, _, _, err := s.SaveVoicemail(context.Background(), dir, audio, 1, "", int64(i+1), 20, int64(len(audio)*2))
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrVoicemailCapacity) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 2 {
		t.Fatal("concurrent capacity not enforced", accepted.Load())
	}
}
