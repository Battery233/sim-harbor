package server

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCompletedAndInterruptedBackupDownloadsReleaseCounters(t *testing.T) {
	s, token := restoredServer(t)
	for i := 1; i <= 3; i++ {
		f := generateTestBackup(t, s, token, fmt.Sprintf("%032x", i), "server")
		r := httptest.NewRequest("GET", "http://example.com/api/backups/"+f.ID+"/download", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		s.Handler().ServeHTTP(&brokenBackupWriter{header: make(map[string][]string)}, r)
		s.backups.mu.Lock()
		retained := len(s.backups.downloads)
		s.backups.mu.Unlock()
		if retained != 0 {
			t.Fatal("interrupted transfer retained download counters", retained)
		}
		w := backupAPI(s, "GET", "/backups/"+f.ID+"/download", "", token)
		if w.Code != 200 || int64(w.Body.Len()) != f.Bytes {
			t.Fatal("completed transfer", w.Code)
		}
		s.backups.mu.Lock()
		retained = len(s.backups.downloads)
		s.backups.mu.Unlock()
		if retained != 0 {
			t.Fatal("completed transfer retained download counters", retained)
		}
		if w = backupAPI(s, "POST", "/backups/"+f.ID+"/delete", `{"confirmed":true}`, token); w.Code != 200 {
			t.Fatal("backup deletion", w.Code)
		}
	}
}

type heldBackupWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (w *heldBackupWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started); <-w.release })
	return w.ResponseRecorder.Write(p)
}

func TestConcurrentBackupDownloadKeepsLastReaderProtected(t *testing.T) {
	s, token := restoredServer(t)
	f := generateTestBackup(t, s, token, fmt.Sprintf("%032x", 4), "browser")
	var wg sync.WaitGroup
	var release []func()
	var completed []chan struct{}
	t.Cleanup(func() {
		for _, unblock := range release {
			unblock()
		}
		wg.Wait()
	})
	for i := 0; i < 2; i++ {
		gate := make(chan struct{})
		release = append(release, sync.OnceFunc(func() { close(gate) }))
		w := &heldBackupWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: gate}
		done := make(chan struct{})
		completed = append(completed, done)
		r := httptest.NewRequest("GET", "http://example.com/api/backups/"+f.ID+"/download", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		wg.Add(1)
		go func() { defer wg.Done(); defer close(done); s.Handler().ServeHTTP(w, r) }()
		select {
		case <-w.started:
		case <-time.After(3 * time.Second):
			t.Fatal("download did not start")
		}
	}
	for i, remaining := range []int{1, 0} {
		release[i]()
		select {
		case <-completed[i]:
		case <-time.After(3 * time.Second):
			t.Fatal("download did not finish")
		}
		s.backups.mu.Lock()
		active, retained := s.backups.downloads[f.ID], len(s.backups.downloads)
		files, err := s.backups.filesLocked(time.Unix(f.ExpiresAt+1, 0))
		s.backups.mu.Unlock()
		if err != nil || active != remaining || retained != remaining || len(files) != remaining {
			t.Fatal("last reader was released early or bookkeeping survived expiry", active, retained, len(files), err)
		}
		if remaining > 0 {
			if w := backupAPI(s, "POST", "/backups/"+f.ID+"/delete", `{"confirmed":true}`, token); w.Code != 409 {
				t.Fatal("active download lost deletion protection", w.Code)
			}
		}
	}
}
