package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
)

const BackupBytes int64 = 256 << 20

var ErrBackupLimit = errors.New("backup_limit_exceeded")

// Use SQLite's Backup API, not a main-file copy (which would omit WAL) nor
// VACUUM INTO (which can renumber outgoing's implicit ROWIDs).
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			os.Remove(abs)
			os.Remove(abs + "-wal")
			os.Remove(abs + "-shm")
			os.Remove(abs + "-journal")
		}
	}()
	// A dedicated read connection keeps the application's single business
	// connection available between incremental backup steps.
	var sourcePath string
	if err = s.DB.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&sourcePath); err != nil {
		return err
	}
	if sourcePath == "" {
		return errors.New("backup_requires_file_database")
	}
	sourceURI := url.URL{Scheme: "file", Path: sourcePath, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)", "synchronous(FULL)"}}.Encode()}
	sourceDB, err := sql.Open("sqlite", sourceURI.String())
	if err != nil {
		return err
	}
	defer sourceDB.Close()
	sourceDB.SetMaxOpenConns(1)
	conn, err := sourceDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var pages, pageSize int64
	if err = conn.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return err
	}
	if err = conn.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return err
	}
	if pageSize <= 0 || pages > BackupBytes/pageSize {
		return ErrBackupLimit
	}
	err = conn.Raw(func(raw any) (result error) {
		source, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("sqlite_backup_unavailable")
		}
		uri := url.URL{Scheme: "file", Path: abs, RawQuery: "_pragma=synchronous%28FULL%29"}
		b, e := source.NewBackup(uri.String())
		if e != nil {
			return e
		}
		defer func() {
			if e := b.Finish(); result == nil {
				result = e
			}
		}()
		for {
			if e = ctx.Err(); e != nil {
				return e
			}
			more, e := b.Step(256)
			if e != nil {
				return e
			}
			if int64(b.PageCount()) > BackupBytes/pageSize {
				return ErrBackupLimit
			}
			if !more {
				return nil
			}
		}
	})
	if err != nil {
		return err
	}
	f, err = os.OpenFile(abs, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = f.Sync()
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	completed = true
	return nil
}
