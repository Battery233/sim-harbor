package diagnostics

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func history(t *testing.T, path string, events ...Event) {
	t.Helper()
	var b bytes.Buffer
	for _, e := range events {
		p, _ := json.Marshal(e)
		b.Write(p)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestRetentionStartupMixedFilesAndBoundary(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	os.Mkdir(logs, 0700)
	now := time.Now()
	cutoff := now.Add(-7 * 24 * time.Hour)
	history(t, filepath.Join(logs, "service.jsonl.1"), Event{ID: 1, Time: cutoff.Add(-time.Hour).UnixMilli(), Code: "service_started"}, Event{ID: 2, Time: now.Add(-time.Hour).UnixMilli(), Code: "service_stopped"})
	history(t, filepath.Join(logs, "service.jsonl"), Event{ID: 3, Time: now.UnixMilli(), Code: "service_started"})
	r, err := Open(dir, "info", 5000, 65536, 1, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rows, _ := r.Snapshot(Filter{}, 5000)
	if len(rows) != 2 || rows[0].ID != 3 || rows[1].ID != 2 {
		t.Fatal(rows)
	}
	p, _ := os.ReadFile(filepath.Join(logs, "service.jsonl.1"))
	if strings.Contains(string(p), `"id":1,`) {
		t.Fatal("expired record retained")
	}
	r.events = append(r.events, Event{ID: 4, Time: cutoff.UnixMilli(), Code: "service_started"})
	r.Prune(now)
	rows, _ = r.Snapshot(Filter{}, 5000)
	if len(rows) != 3 {
		t.Fatal("exact seven-day boundary discarded", rows)
	}
}
func TestRetentionCapacityProtectsYoungRecordsAndRecovers(t *testing.T) {
	dir := t.TempDir()
	var output bytes.Buffer
	r, err := Open(dir, "info", 5000, 65536, 1, &output, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 1800; i++ {
		r.Record("query_started", Fields{Transaction: Reference("synthetic")})
	}
	rows, status := r.Snapshot(Filter{}, 5000)
	if status.FileOK || !r.diskFull {
		t.Fatal("capacity must pause sink", status)
	}
	count := 0
	for _, e := range rows {
		if e.Code == "logging_capacity_reached" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("capacity warnings flooded", count)
	}
	oldest, _ := os.ReadFile(filepath.Join(dir, "logs", "service.jsonl.1"))
	if !bytes.Contains(oldest, []byte(`"id":1,`)) {
		t.Fatal("youngest retention overwritten")
	}
	for _, name := range []string{"service.jsonl", "service.jsonl.1"} {
		info, _ := os.Stat(filepath.Join(dir, "logs", name))
		if info.Size() > 65536 || info.Mode().Perm() != 0600 {
			t.Fatal(info)
		}
	}
	r.Prune(time.Now().Add(8 * 24 * time.Hour))
	_, status = r.Snapshot(Filter{}, 5000)
	if !status.FileOK || r.diskFull {
		t.Fatal("capacity did not recover", status)
	}
	r.Record("service_started", Fields{})
	p, _ := os.ReadFile(filepath.Join(dir, "logs", "service.jsonl"))
	if !bytes.Contains(p, []byte("service_started")) {
		t.Fatal("did not resume writing")
	}
}
func TestRetentionIdleCurrentFileRestartAndConcurrency(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, "info", 5000, 65536, 1, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	r.Record("service_started", Fields{})
	r.Close()
	path := filepath.Join(dir, "logs", "service.jsonl")
	history(t, path, Event{ID: 1, Time: time.Now().Add(-8 * 24 * time.Hour).UnixMilli(), Code: "service_started"})
	r, err = Open(dir, "info", 5000, 65536, 1, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if p, _ := os.ReadFile(path); len(p) != 0 {
		t.Fatal("idle history not cleaned")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			r.Record("query_started", Fields{})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 4; i++ {
			r.Prune(time.Now())
		}
	}()
	wg.Wait()
	rows, status := r.Snapshot(Filter{}, 5000)
	if len(rows) != 100 || !status.FileOK {
		t.Fatal(status, len(rows))
	}
}
func TestRetentionRejectsSymlinkDuringCleanup(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, "info", 5000, 65536, 1, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	private := filepath.Join(dir, "private")
	os.WriteFile(private, []byte("synthetic-private"), 0600)
	os.Symlink(private, filepath.Join(dir, "logs", "service.jsonl.1"))
	r.Prune(time.Now())
	_, status := r.Snapshot(Filter{}, 1)
	if status.FileOK {
		t.Fatal("symlink cleanup accepted")
	}
	p, _ := os.ReadFile(private)
	if string(p) != "synthetic-private" {
		t.Fatal("other file touched")
	}
}
func TestRetentionDoesNotReviveIOFailure(t *testing.T) {
	r, err := Open(t.TempDir(), "info", 5000, 65536, 1, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.file.file.Close()
	r.Record("service_started", Fields{})
	r.Prune(time.Now())
	_, status := r.Snapshot(Filter{}, 1)
	if status.FileOK || r.diskFull {
		t.Fatal("IO failure hidden", status)
	}
}
func TestRetentionInvalidDaysAndDefaultWindow(t *testing.T) {
	for _, days := range []int{0, -1, 31} {
		if _, err := Open(t.TempDir(), "info", 5000, 65536, 1, nil, days); err == nil {
			t.Fatal(days)
		}
	}
	_, status := New("info", 0, nil).Snapshot(Filter{}, 1)
	if status.Limit != 5000 {
		t.Fatal(status)
	}
}

func TestRetentionRecoversAbandonedRewriteWithoutOtherFiles(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	os.Mkdir(logs, 0700)
	stale := filepath.Join(logs, ".log-retention-12345")
	other := filepath.Join(logs, "operator-notes")
	os.WriteFile(stale, []byte("synthetic-interrupted-rewrite"), 0600)
	os.WriteFile(other, []byte("synthetic-notes"), 0600)
	r, err := Open(dir, "info", 5000, 65536, 1, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("staging artifact not cleaned", err)
	}
	if p, _ := os.ReadFile(other); string(p) != "synthetic-notes" {
		t.Fatal("unrelated file changed")
	}
}
