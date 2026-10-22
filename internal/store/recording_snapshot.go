package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Battery233/sim-harbor/internal/recording"
)

const RecordingBackupBytes int64 = 65536 << 20
const RecordingFileBytes int64 = recording.MaxBytes
const RecordingBackupFiles = 10000

var ErrRecordingBackup = errors.New("recording_backup_incomplete")

type RecordingSnapshot struct {
	Name   string
	Size   int64
	SHA256 string
}

// Pin immutable completed AMRs alongside a matching SQLite snapshot. Holding
// the same mutex as upload/deletion prevents an index/file race. Hard links
// pin old content even when a later user deletion unlinks the live file.
// Cross-filesystem/non-linking filesystems use a bounded, cancelable copy.
func (s *Store) SnapshotWithRecordings(ctx context.Context, database, dataDir, target string) ([]RecordingSnapshot, error) {
	s.voicemailMu.Lock()
	defer s.voicemailMu.Unlock()
	if err := s.SnapshotTo(ctx, database); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT media_id,stored_name,size,sha256 FROM voicemails`)
	if err != nil {
		return nil, err
	}
	expected := map[string]RecordingSnapshot{}
	deleted := map[string]bool{}
	for rows.Next() {
		var id, name, digest string
		var size int64
		if err = rows.Scan(&id, &name, &size, &digest); err != nil {
			break
		}
		if !recording.ValidMediaID(id) {
			err = ErrRecordingBackup
			break
		}
		if name == "" {
			deleted[id+".amr"] = true
			continue
		}
		if name != id+".amr" || size <= 0 || size > RecordingFileBytes {
			err = ErrRecordingBackup
			break
		}
		expected[name] = RecordingSnapshot{Name: name, Size: size, SHA256: digest}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(dataDir, "voicemail")
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) && len(expected) == 0 {
		return []RecordingSnapshot{}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrRecordingBackup
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	folder, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	if err = os.Mkdir(target, 0700); err != nil {
		return nil, err
	}
	result := []RecordingSnapshot{}
	var total int64
	count := 0
	for {
		entries, readErr := folder.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			count++
			if count > RecordingBackupFiles {
				return nil, ErrBackupLimit
			}
			name := entry.Name()
			if filepath.Ext(name) != ".amr" || deleted[name] {
				continue
			}
			if !recording.ValidMediaID(name[:len(name)-4]) {
				return nil, ErrRecordingBackup
			}
			f, err := root.Open(name)
			if err != nil {
				return nil, ErrRecordingBackup
			}
			st, statErr := root.Lstat(name)
			actual, openErr := f.Stat()
			if statErr != nil || openErr != nil || !st.Mode().IsRegular() || !os.SameFile(st, actual) || st.Size() <= 0 || st.Size() > RecordingFileBytes {
				f.Close()
				return nil, ErrRecordingBackup
			}
			record := RecordingSnapshot{Name: name, Size: st.Size()}
			if indexed, ok := expected[name]; ok {
				if indexed.Size != st.Size() {
					f.Close()
					return nil, ErrRecordingBackup
				}
				record.SHA256 = indexed.SHA256
				delete(expected, name)
			}
			total += st.Size()
			if total > RecordingBackupBytes {
				f.Close()
				return nil, ErrBackupLimit
			}
			dest := filepath.Join(target, name)
			err = os.Link(filepath.Join(dir, name), dest)
			if err == nil {
				pinned, e := os.Lstat(dest)
				if e != nil || !pinned.Mode().IsRegular() || !os.SameFile(pinned, actual) {
					f.Close()
					return nil, ErrRecordingBackup
				}
			} else {
				out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					f.Close()
					return nil, e
				}
				var n int64
				n, e = io.Copy(out, &snapshotReader{ctx: ctx, reader: io.LimitReader(f, RecordingFileBytes+1)})
				if e == nil && n != record.Size {
					e = ErrRecordingBackup
				}
				if e == nil {
					e = out.Sync()
				}
				ce := out.Close()
				if e != nil {
					f.Close()
					return nil, e
				}
				if ce != nil {
					f.Close()
					return nil, ce
				}
			}
			f.Close()
			result = append(result, record)
		}
		if readErr == io.EOF {
			break
		}
	}
	if len(expected) > 0 {
		return nil, ErrRecordingBackup
	}
	return result, nil
}

type snapshotReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *snapshotReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
