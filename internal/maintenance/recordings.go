package maintenance

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/Battery233/sim-harbor/internal/recording"
	"github.com/Battery233/sim-harbor/internal/store"
)

func recordingArchiveName(name string) bool {
	return len(name) > 10 && name[:10] == "voicemail/" && recording.ValidFileName(name[10:])
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}

func recordingDigest(ctx context.Context, path string) (int64, string, error) {
	f, e := os.Open(path)
	if e != nil {
		return 0, "", e
	}
	defer f.Close()
	prefix := make([]byte, len(recording.Header))
	if _, e = io.ReadFull(f, prefix); e != nil || !recording.ValidAudio(prefix) {
		return 0, "", ErrInvalidBackup
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return 0, "", e
	}
	h := sha256.New()
	n, e := io.Copy(h, &contextReader{ctx: ctx, reader: io.LimitReader(f, store.RecordingFileBytes+1)})
	if e != nil {
		return 0, "", e
	}
	if n > store.RecordingFileBytes {
		return 0, "", ErrInvalidBackup
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func writeArchiveFile(ctx context.Context, z *zip.Writer, name, path string, size int64, digest string) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(0600)
	out, e := z.CreateHeader(header)
	if e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, h), &contextReader{ctx: ctx, reader: io.LimitReader(f, size+1)})
	if e != nil {
		return e
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return ErrInvalidBackup
	}
	return nil
}

func validateRecordingArchive(ctx context.Context, v *validated, files []*zip.File) error {
	m := v.preview.Manifest
	if m.Format == 1 {
		if m.IncludesRecordings || len(m.Recordings) > 0 || len(files) != 2 {
			return ErrInvalidBackup
		}
		info, e := os.Stat(filepath.Join(v.root, "input.zip"))
		if e != nil || info.Size() > DatabaseArchiveLimit {
			return ErrInvalidBackup
		}
		return nil
	}
	if !m.IncludesRecordings || len(m.Recordings) > store.RecordingBackupFiles || len(files) != len(m.Recordings)+2 {
		return ErrInvalidBackup
	}
	declared := map[string]RecordingManifest{}
	var total int64
	for _, r := range m.Recordings {
		if !recording.ValidFileName(r.Name) || r.Bytes <= 0 || r.Bytes > store.RecordingFileBytes || len(r.SHA256) != 64 {
			return ErrInvalidBackup
		}
		if _, ok := declared[r.Name]; ok {
			return ErrInvalidBackup
		}
		total += r.Bytes
		if total > store.RecordingBackupBytes {
			return ErrInvalidBackup
		}
		declared[r.Name] = r
		n, d, e := recordingDigest(ctx, filepath.Join(v.root, "voicemail", r.Name))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil || n != r.Bytes || d != r.SHA256 {
			return ErrInvalidBackup
		}
	}
	for _, f := range files {
		if recordingArchiveName(f.Name) {
			if _, ok := declared[f.Name[10:]]; !ok {
				return ErrInvalidBackup
			}
		}
	}
	// Verify the manifest covers every active index and never resurrects a deleted recording.
	uri := url.URL{Scheme: "file", Path: v.database, RawQuery: "mode=ro&immutable=1&_pragma=trusted_schema%28OFF%29"}
	db, e := sql.Open("sqlite", uri.String())
	if e != nil {
		return e
	}
	defer db.Close()
	var exists int
	if e = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='voicemails'`).Scan(&exists); e != nil {
		return ErrInvalidBackup
	}
	if exists == 0 {
		if len(declared) > 0 {
			return ErrInvalidBackup
		}
		return nil
	}
	rows, e := db.QueryContext(ctx, `SELECT media_id,stored_name,size,sha256 FROM voicemails`)
	if e != nil {
		return ErrInvalidBackup
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, digest string
		var size int64
		if e = rows.Scan(&id, &name, &size, &digest); e != nil {
			return ErrInvalidBackup
		}
		if name == "" {
			if _, ok := declared[id+".amr"]; ok {
				return ErrInvalidBackup
			}
			continue
		}
		r, ok := declared[name]
		if !ok || name != id+".amr" || r.Bytes != size || r.SHA256 != digest {
			return ErrInvalidBackup
		}
	}
	return rows.Err()
}

func restoreRecordings(ctx context.Context, v *validated, target string) error {
	if !v.preview.Manifest.IncludesRecordings {
		return nil
	}
	dir := filepath.Join(target, "voicemail")
	if e := os.Mkdir(dir, 0700); e != nil {
		return e
	}
	for _, r := range v.preview.Manifest.Recordings {
		if e := ctx.Err(); e != nil {
			return e
		}
		f, e := os.Open(filepath.Join(v.root, "voicemail", r.Name))
		if e != nil {
			return e
		}
		out, e := os.OpenFile(filepath.Join(dir, r.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			f.Close()
			return e
		}
		n, e := io.Copy(out, &contextReader{ctx: ctx, reader: io.LimitReader(f, r.Bytes+1)})
		f.Close()
		if e == nil && n != r.Bytes {
			e = ErrInvalidBackup
		}
		if e == nil {
			e = out.Sync()
		}
		ce := out.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	return syncDir(dir)
}
