package diagnostics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

var errLogCapacity = errors.New("log retention capacity reached")

// RunRetention also cleans an idle service; cleanup never depends on a new event.
func (r *Recorder) RunRetention(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.Prune(now)
		}
	}
}

// Prune operates only on the fixed application log files, under the writer lock.
func (r *Recorder) Prune(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.retention <= 0 || r.file == nil {
		return
	}
	cutoff := now.Add(-r.retention).UnixMilli()
	kept := r.events[:0]
	for _, e := range r.events {
		if e.Time >= cutoff {
			kept = append(kept, e)
		}
	}
	r.events = kept
	// Do not revive an IO-failed sink. Capacity pauses may recover after expiry.
	if !r.fileOK && !r.diskFull {
		return
	}
	if err := r.file.prune(cutoff); err != nil {
		r.fileOK, r.diskFull = false, false
		r.sequence++
		e := Event{ID: r.sequence, Time: now.UnixMilli(), Code: "logging_write_failed", Definition: catalog["logging_write_failed"]}
		r.append(e)
		if r.output != nil {
			p, _ := json.Marshal(e)
			_, _ = r.output.Write(append(p, '\n'))
		}
		return
	}
	if r.diskFull {
		info, err := os.Stat(r.file.name(r.file.backups))
		if os.IsNotExist(err) || err == nil && info.Size() == 0 || r.file.size+8192 <= r.file.max {
			r.diskFull, r.fileOK = false, true
		}
	}
}

func (f *rotatingFile) prune(cutoff int64) error {
	// The application holds its data-directory lease; no other writer owns these
	// staging files. A killed atomic rewrite must not accumulate extra disk usage.
	staging, err := filepath.Glob(filepath.Join(filepath.Dir(f.path), ".log-retention-*"))
	if err != nil {
		return err
	}
	for _, path := range staging {
		if err = regular(path); err != nil {
			return err
		}
	}
	// Preflight every path before modifying any file, including dormant backups.
	for i := 0; i <= f.backups; i++ {
		if err := regular(f.name(i)); err != nil {
			return err
		}
	}
	for _, path := range staging {
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for i := f.backups; i >= 0; i-- {
		changed, err := pruneFile(f.name(i), f.max, cutoff, func() error {
			if i == 0 && f.file != nil {
				return f.file.Close()
			}
			return nil
		})
		if changed && i == 0 && f.file != nil {
			if openErr := f.open(); err == nil {
				err = openErr
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Rewrite mixed-age files rather than deleting young records by file age.
// The temporary file is private, synced, and atomically replaces its source.
func pruneFile(path string, maxBytes, cutoff int64, beforeReplace func() error) (bool, error) {
	source, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() > maxBytes {
		return false, errors.New("log file exceeds configured limit")
	}
	newScanner := func() *bufio.Scanner {
		s := bufio.NewScanner(io.LimitReader(source, maxBytes+1))
		s.Buffer(make([]byte, 1024), 8192)
		return s
	}
	expired := func(line []byte) bool {
		var event struct {
			Time int64 `json:"time_ms"`
		}
		return json.Unmarshal(line, &event) == nil && event.Time > 0 && event.Time < cutoff
	}
	// Most hourly checks contain only young records: avoid copying them to disk.
	scanner := newScanner()
	changed := false
	for scanner.Scan() {
		if expired(scanner.Bytes()) {
			changed = true
			break
		}
	}
	if err = scanner.Err(); err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".log-retention-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	scanner = newScanner()
	for scanner.Scan() {
		line := scanner.Bytes()
		if expired(line) {
			continue
		}
		if _, err = temp.Write(append(line, '\n')); err != nil {
			return false, err
		}
	}
	if err = scanner.Err(); err != nil {
		return false, err
	}
	if err = temp.Sync(); err != nil {
		return false, err
	}
	if err = temp.Close(); err != nil {
		return false, err
	}
	if err = beforeReplace(); err != nil {
		return false, err
	}
	err = os.Rename(temp.Name(), path)
	return true, err
}
