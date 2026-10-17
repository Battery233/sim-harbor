package diagnostics

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLevelAndPrivateFields(t *testing.T) {
	var output bytes.Buffer
	r := New("info", 100, &output)
	secret := "synthetic-private-body-key-ICCID"
	r.Record("event_received", Fields{})
	r.Record(secret, Fields{})
	r.Record("query_failed", Fields{Session: secret, Operation: secret, Transaction: secret, Outcome: secret, Type: -1, SubType: 999999, Slot: 3, DurationMS: -1})
	rows, _ := r.Snapshot(Filter{}, 50)
	if len(rows) != 1 || rows[0].Fields != (Fields{}) || strings.Contains(output.String(), secret) {
		t.Fatal("untrusted fields escaped", output.String())
	}
	ref := Reference(secret)
	r.Record("query_started", Fields{Transaction: ref})
	rows, _ = r.Snapshot(Filter{Reference: ref}, 50)
	if len(rows) != 1 || rows[0].Transaction != ref {
		t.Fatal("reference missing", rows)
	}
	if !ValidFilter(Filter{Level: "warn", Category: "tcp"}) || ValidFilter(Filter{Reference: secret}) {
		t.Fatal("filter validation")
	}
}

func TestConcurrentBoundedWindowAndPaging(t *testing.T) {
	r := New("debug", 100, nil)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Record("event_received", Fields{Type: 998})
			}
		}()
	}
	wg.Wait()
	rows, status := r.Snapshot(Filter{}, 50)
	if len(rows) != 50 || status.Retained != 100 || rows[0].ID != 400 {
		t.Fatal(status, rows)
	}
	older, _ := r.Snapshot(Filter{Before: rows[49].ID}, 51)
	if len(older) != 50 || older[0].ID != rows[49].ID-1 {
		t.Fatal("paging overlap", older)
	}
}

func TestMultipartMetadataIsBoundedAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	var output bytes.Buffer
	r, err := Open(dir, "info", 100, 65536, 1, &output)
	if err != nil {
		t.Fatal(err)
	}
	secret := "PRIVATE-SENDER-SIM-BODY"
	complete := false
	ref := Reference("SYNTHETIC-GROUP")
	r.Record("sms_fragment_received", Fields{SMSGroup: ref, SMSTotal: 3, SMSParts: "3,1,1", SMSComplete: &complete, SMSStoredComplete: &complete, SMSTime: 1700000000})
	complete = true
	rows, _ := r.Snapshot(Filter{Reference: ref}, 10)
	if len(rows) != 1 || rows[0].SMSParts != "1,3" || rows[0].SMSComplete == nil || *rows[0].SMSComplete {
		t.Fatal("metadata mutated or lost")
	}
	*rows[0].SMSComplete = true
	*rows[0].SMSStoredComplete = true
	rows, _ = r.Snapshot(Filter{Reference: ref}, 10)
	if *rows[0].SMSComplete || *rows[0].SMSStoredComplete {
		t.Fatal("caller mutated retained metadata")
	}
	r.Record("sms_fragment_rejected", Fields{SMSGroup: secret, SMSParts: secret, SMSTotal: 999, SMSTime: -1, SMSInvalidParts: -1, Outcome: secret})
	rows, _ = r.Snapshot(Filter{}, 10)
	if rows[0].Fields != (Fields{}) || strings.Contains(output.String(), secret) {
		t.Fatal("unsafe fields escaped")
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(dir, "info", 100, 65536, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rows, _ = r.Snapshot(Filter{Reference: ref}, 10)
	if len(rows) != 1 || rows[0].SMSTotal != 3 || rows[0].SMSParts != "1,3" || rows[0].SMSComplete == nil || *rows[0].SMSComplete {
		t.Fatal("restart lost metadata")
	}
}

func TestRotationPermissionsAndRestart(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, "debug", 1000, 65536, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Isolate mechanical size rotation; time-protected capacity is tested separately.
	r.file.protectRecent = false
	for i := 0; i < 1500; i++ {
		r.Record("event_received", Fields{Type: 998, Transaction: Reference("synthetic-transaction")})
	}
	before, _ := r.Snapshot(Filter{}, 1)
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "logs", "service.jsonl*"))
	if len(files) != 3 {
		t.Fatal("unbounded or missing rotation", files)
	}
	for _, path := range files {
		stat, err := os.Stat(path)
		if err != nil || stat.Size() > 65536 || stat.Mode().Perm() != 0600 {
			t.Fatal(stat, err)
		}
	}
	r, err = Open(dir, "info", 1000, 65536, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.Record("service_started", Fields{})
	after, _ := r.Snapshot(Filter{}, 1)
	if after[0].ID != before[0].ID+1 {
		t.Fatal("restart sequence lost", before, after)
	}
}

func TestDamagedHistoryAndPartialTailRemainSafe(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, "info", 100, 65536, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Record("service_started", Fields{})
	r.Close()
	path := filepath.Join(dir, "logs", "service.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{\"body\":\"synthetic-private-text\"}\n{incomplete")
	f.Close()
	var output bytes.Buffer
	r, err = Open(dir, "info", 100, 65536, 1, &output)
	if err != nil {
		t.Fatal(err)
	}
	r.Record("service_stopped", Fields{})
	r.Close()
	r, err = Open(dir, "info", 100, 65536, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rows, _ := r.Snapshot(Filter{}, 100)
	p, _ := json.Marshal(rows)
	if strings.Contains(string(p), "synthetic-private-text") || strings.Contains(output.String(), "synthetic-private-text") {
		t.Fatal("damaged data exposed")
	}
	found := false
	for _, row := range rows {
		if row.Code == "service_stopped" {
			found = true
		}
	}
	if !found {
		t.Fatal("partial line swallowed next record", rows)
	}
}

func TestFileFailureDoesNotDiscardMemoryEvidence(t *testing.T) {
	r, err := Open(t.TempDir(), "info", 100, 65536, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.file.file.Close() // Simulate disk sink failure; no operation must be replayed.
	r.Record("sms_write_unknown", Fields{Operation: Reference("synthetic-operation")})
	rows, status := r.Snapshot(Filter{}, 50)
	if len(rows) != 2 || status.FileOK || rows[0].Code != "logging_write_failed" || rows[1].Code != "sms_write_unknown" {
		t.Fatal(rows, status)
	}
	r.Record("service_started", Fields{})
	rows, _ = r.Snapshot(Filter{}, 50)
	if len(rows) != 3 {
		t.Fatal("repeated file failure flood", rows)
	}
}

func TestSymlinkHistoryRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private.txt")
	if err := os.WriteFile(private, []byte("synthetic-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(dir, "logs", "service.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, "info", 100, 65536, 1, nil); err == nil {
		t.Fatal("symlink accepted")
	}
}
