package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/Battery233/sim/internal/store"
)

var ErrConfirmation = errors.New("backup_confirmation_mismatch")

type RestoreResult struct {
	Directory       string `json:"directory"`
	OutboundBlocked bool   `json:"outbound_blocked"`
	ArchiveSHA256   string `json:"archive_sha256"`
}

func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// Link publishes the completed file atomically and refuses an existing target.
// Rename would replace a database unexpectedly created after directory checks.
func publishDatabase(pending, target string) error {
	if err := os.Link(pending, target); err != nil {
		return err
	}
	return os.Remove(pending)
}

func Restore(ctx context.Context, backup, target, expected string) (RestoreResult, error) {
	var result RestoreResult
	if len(expected) != 64 {
		return result, ErrConfirmation
	}
	v, e := load(ctx, backup)
	if e != nil {
		return result, e
	}
	defer v.close()
	if expected != v.preview.ArchiveSHA256 {
		return result, ErrConfirmation
	}
	abs, e := filepath.Abs(target)
	if e != nil || target == "" {
		return result, ErrInvalidBackup
	}
	// A target must not exist even when empty. Never replace a live/old database.
	if _, e = os.Lstat(abs); e == nil || !os.IsNotExist(e) {
		return result, errors.Join(os.ErrExist, errors.New("restore_target_exists"))
	}
	s, e := store.Open(v.database)
	if e != nil {
		return result, e
	}
	if e = s.HoldRestoredData(ctx, expected, time.Now().Unix()); e != nil {
		s.DB.Close()
		return result, e
	}
	safe := filepath.Join(v.root, "safe.db")
	e = s.SnapshotTo(ctx, safe)
	ce := s.DB.Close()
	if e != nil {
		return result, e
	}
	if ce != nil {
		return result, ce
	}
	if _, e = databaseInfo(ctx, safe); e != nil {
		return result, e
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	if e = os.MkdirAll(filepath.Dir(abs), 0700); e != nil {
		return result, e
	}
	if e = os.Mkdir(abs, 0700); e != nil {
		return result, e
	}
	lock, e := Acquire(abs)
	if e != nil {
		return result, e
	}
	defer lock.Close()
	marker := filepath.Join(abs, ".restore-incomplete")
	if e = os.WriteFile(marker, []byte("restore_incomplete\n"), 0600); e != nil {
		return result, e
	}
	if e = syncDir(abs); e != nil {
		return result, e
	}
	// Failed publication keeps the marker. A future service refuses to open it.
	source, e := os.Open(safe)
	if e != nil {
		return result, e
	}
	defer source.Close()
	pending := filepath.Join(abs, ".sim.db.pending")
	f, e := os.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, e
	}
	_, e = io.Copy(f, source)
	if e == nil {
		e = f.Sync()
	}
	ce = f.Close()
	if e != nil {
		return result, e
	}
	if ce != nil {
		return result, ce
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	if e = publishDatabase(pending, filepath.Join(abs, "sim.db")); e != nil {
		return result, e
	}
	if e = syncDir(abs); e != nil {
		return result, e
	}
	if e = os.Remove(marker); e != nil {
		return result, e
	}
	if e = syncDir(abs); e != nil {
		return result, e
	}
	return RestoreResult{Directory: abs, OutboundBlocked: true, ArchiveSHA256: expected}, nil
}

// Offline backup shares the service's lease and opens the existing source
// read-only, without applying migrations or restart state transitions.
func BackupDirectory(ctx context.Context, dir, output string) error {
	if _, e := os.Stat(filepath.Join(dir, "sim.db")); e != nil {
		return e
	}
	lock, e := Acquire(dir)
	if e != nil {
		return e
	}
	defer lock.Close()
	if _, e = os.Lstat(filepath.Join(dir, ".restore-incomplete")); e == nil || !os.IsNotExist(e) {
		return errors.New("restore_incomplete")
	}
	abs, e := filepath.Abs(filepath.Join(dir, "sim.db"))
	if e != nil {
		return e
	}
	uri := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&_pragma=synchronous%28FULL%29"}
	db, e := sql.Open("sqlite", uri.String())
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	a, e := Create(ctx, &store.Store{DB: db}, dir)
	if e != nil {
		return e
	}
	defer a.Close()
	source, e := os.Open(a.Path)
	if e != nil {
		return e
	}
	defer source.Close()
	dest, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	done := false
	defer func() {
		if !done {
			os.Remove(output)
		}
	}()
	_, e = io.Copy(dest, source)
	if e == nil {
		e = dest.Sync()
	}
	ce := dest.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = syncDir(filepath.Dir(output)); e != nil {
		return e
	}
	done = true
	return nil
}
