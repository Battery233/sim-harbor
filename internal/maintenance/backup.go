package maintenance

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Battery233/sim-harbor/internal/store"
)

const MaxArchive int64 = store.BackupBytes + (16 << 20)
const manifestLimit int64 = 16 << 10

var ErrInvalidBackup = errors.New("invalid_backup")
var ErrSchema = errors.New("unsupported_backup_schema")

type Manifest struct {
	Format      int              `json:"format_version"`
	Application string           `json:"application"`
	CreatedAt   string           `json:"created_at"`
	Schema      int              `json:"schema_version"`
	Bytes       int64            `json:"database_bytes"`
	SHA256      string           `json:"database_sha256"`
	Counts      map[string]int64 `json:"counts"`
}
type Artifact struct {
	Path     string
	Manifest Manifest
	root     string
}

func (a *Artifact) Close() { os.RemoveAll(a.root) }

func Create(ctx context.Context, s *store.Store, parent string) (*Artifact, error) {
	root, err := os.MkdirTemp(parent, ".sim-backup-")
	if err != nil {
		return nil, err
	}
	a := &Artifact{root: root, Path: filepath.Join(root, "sim-backup.zip")}
	success := false
	defer func() {
		if !success {
			a.Close()
		}
	}()
	database := filepath.Join(root, "sim.db")
	if err = s.SnapshotTo(ctx, database); err != nil {
		return nil, err
	}
	m, err := databaseInfo(ctx, database)
	if err != nil {
		return nil, err
	}
	m.Format = 1
	m.Application = "sim"
	m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	m.Bytes, m.SHA256, err = fileDigest(database)
	if err != nil {
		return nil, err
	}
	a.Manifest = m
	file, err := os.OpenFile(a.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	z := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "manifest.json", Method: zip.Store}
	header.SetMode(0600)
	w, err := z.CreateHeader(header)
	if err == nil {
		err = json.NewEncoder(w).Encode(m)
	}
	if err == nil {
		header = &zip.FileHeader{Name: "sim.db", Method: zip.Store}
		header.SetMode(0600)
		w, err = z.CreateHeader(header)
		if err == nil {
			var source *os.File
			source, err = os.Open(database)
			if err == nil {
				_, err = io.Copy(w, source)
				source.Close()
			}
		}
	}
	ze := z.Close()
	if err == nil {
		err = ze
	}
	if err == nil {
		err = file.Sync()
	}
	ce := file.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return nil, err
	}
	size, _, err := fileDigest(a.Path)
	if err != nil {
		return nil, err
	}
	if size > MaxArchive {
		return nil, store.ErrBackupLimit
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	success = true
	return a, nil
}

func fileDigest(path string) (int64, string, error) {
	f, e := os.Open(path)
	if e != nil {
		return 0, "", e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), e
}

// Read-only and immutable because only a private, closed snapshot is opened.
// No Open/migrate call is made while inspecting an original backup.
func databaseInfo(ctx context.Context, path string) (Manifest, error) {
	var m Manifest
	uri := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"ro"}, "immutable": {"1"}, "_pragma": {"trusted_schema(OFF)"}}.Encode()}
	db, e := sql.Open("sqlite", uri.String())
	if e != nil {
		return m, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var check string
	if e = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); e != nil || check != "ok" {
		return m, ErrInvalidBackup
	}
	var count int
	if e = db.QueryRowContext(ctx, `SELECT count(*),coalesce(max(version),0) FROM schema_version`).Scan(&count, &m.Schema); e != nil || count != 1 {
		return m, ErrInvalidBackup
	}
	if m.Schema < 4 || m.Schema > store.SchemaVersion {
		return m, ErrSchema
	}
	// Reject views/triggers/virtual or foreign tables. Counts use fixed names;
	// never execute arbitrary schema objects supplied by an uploaded archive.
	tables := []string{"schema_version", "events", "device", "messages", "notifications", "outgoing", "outgoing_refs", "delivery_receipts", "notification_test"}
	if m.Schema >= 17 {
		tables = append(tables, "sms_receipts")
	}
	if m.Schema >= 14 {
		tables = append(tables, "status_alert_monitors", "status_alert_jobs")
	}
	if m.Schema >= 7 {
		tables = append(tables, "cards")
	}
	if m.Schema >= 11 {
		tables = append(tables, "calls", "call_notifications")
	}
	if m.Schema >= 18 {
		tables = append(tables, "voicemails")
	}
	if m.Schema >= 19 {
		tables = append(tables, "voicemail_controls")
	}
	if m.Schema >= 5 {
		tables = append(tables, "recovery_guard")
	}
	allowed := map[string]bool{}
	for _, name := range tables {
		allowed[name] = true
	}
	rows, e := db.QueryContext(ctx, `SELECT name,type,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`)
	if e != nil {
		return m, ErrInvalidBackup
	}
	seen := map[string]bool{}
	for rows.Next() {
		var name, typ, ddl string
		if e = rows.Scan(&name, &typ, &ddl); e != nil {
			rows.Close()
			return m, ErrInvalidBackup
		}
		switch typ {
		case "table":
			if !allowed[name] || strings.Contains(strings.ToUpper(ddl), "VIRTUAL") {
				rows.Close()
				return m, ErrInvalidBackup
			}
			seen[name] = true
		case "index": // Indexes do not execute code when read.
		default:
			rows.Close()
			return m, ErrInvalidBackup
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(seen) != len(tables) {
		return m, ErrInvalidBackup
	}
	fk, e := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if e != nil {
		return m, ErrInvalidBackup
	}
	bad := fk.Next()
	e = fk.Err()
	fk.Close()
	if bad || e != nil {
		return m, ErrInvalidBackup
	}
	m.Counts = map[string]int64{}
	for _, name := range tables {
		var n int64
		if e = db.QueryRowContext(ctx, `SELECT count(*) FROM `+name).Scan(&n); e != nil {
			return m, ErrInvalidBackup
		}
		m.Counts[name] = n
	}
	// Check columns consumed by the runtime before the restore tool can migrate.
	if m.Schema >= 17 {
		r, err := db.QueryContext(ctx, `SELECT key,message_id,sms_ts FROM sms_receipts LIMIT 0`)
		if err != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 16 {
		r, err := db.QueryContext(ctx, `SELECT sms_total,sms_group_ref FROM messages LIMIT 0`)
		if err != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 18 {
		r, err := db.QueryContext(ctx, `SELECT id,media_id,call_id,slot,phone,start_ts,sha256,size,stored_name,created_at FROM voicemails LIMIT 0`)
		if err != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	for _, q := range []string{`SELECT id,slot,phone,body,sms_ts,complete,is_read,fragments,key FROM messages LIMIT 0`, `SELECT id,device_id,tid,fingerprint,slot,phone,body,segments,iccid,created_at,updated_at,expires_at,dispatched_at,deadline_at,state,rank,error_code FROM outgoing LIMIT 0`, `SELECT id,message_id,state,attempts,next_at,last_error,manual_retry_at FROM notifications LIMIT 0`, `SELECT id,last_seen,status,status_received_at FROM device LIMIT 0`, `SELECT id,requested_at,outcome FROM notification_test LIMIT 0`, `SELECT operation_id,mr FROM outgoing_refs LIMIT 0`, `SELECT hash,type,received_at FROM events LIMIT 0`, `SELECT id,event_hash,device_id,slot,phone,iccid,mr,status,scts,report_ts,matched_operation,match_state FROM delivery_receipts LIMIT 0`} {
		r, e := db.QueryContext(ctx, q)
		if e != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 11 {
		for _, q := range []string{`SELECT id,key,device_id,card_id,card_name,slot,phone,start_ts,connected_ts,end_ts,ended,connection_report,conflict,state,received_at FROM calls LIMIT 0`, `SELECT id,call_id,card_name,group_name,state,attempts,next_at,last_error,accepted_at FROM call_notifications LIMIT 0`} {
			r, err := db.QueryContext(ctx, q)
			if err != nil {
				return m, ErrInvalidBackup
			}
			r.Close()
		}
	}
	if m.Schema >= 14 {
		for _, q := range []string{`SELECT key,bad_since,good_since,active,episode FROM status_alert_monitors LIMIT 0`, `SELECT id,key,episode,kind,label,state,attempts,next_at,last_error,accepted_at FROM status_alert_jobs LIMIT 0`} {
			r, err := db.QueryContext(ctx, q)
			if err != nil {
				return m, ErrInvalidBackup
			}
			r.Close()
		}
	}
	if m.Schema >= 13 {
		r, err := db.QueryContext(ctx, `SELECT notification_enabled FROM cards LIMIT 0`)
		if err != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 12 {
		for _, q := range []string{`SELECT timeout_at FROM calls LIMIT 0`, `SELECT kind,attempted_at FROM call_notifications LIMIT 0`} {
			r, err := db.QueryContext(ctx, q)
			if err != nil {
				return m, ErrInvalidBackup
			}
			r.Close()
		}
	}
	if m.Schema >= 10 {
		r, e := db.QueryContext(ctx, `SELECT number FROM cards LIMIT 0`)
		if e != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 9 {
		r, e := db.QueryContext(ctx, `SELECT deleted_at FROM messages LIMIT 0`)
		if e != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 8 {
		r, e := db.QueryContext(ctx, `SELECT accepted_at FROM notifications LIMIT 0`)
		if e != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 7 {
		for _, q := range []string{`SELECT id,iccid,name,group_name FROM cards LIMIT 0`, `SELECT card_id,device_id FROM messages LIMIT 0`} {
			r, e := db.QueryContext(ctx, q)
			if e != nil {
				return m, ErrInvalidBackup
			}
			r.Close()
		}
	}
	if m.Schema >= 6 {
		r, err := db.QueryContext(ctx, `SELECT card_name FROM messages LIMIT 0`)
		if err != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 15 {
		r, err := db.QueryContext(ctx, `SELECT restore_id,confirmed_at,confirmation_backup_sha256,confirmation_policy FROM recovery_guard LIMIT 0`)
		if err != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	if m.Schema >= 5 {
		r, err := db.QueryContext(ctx, `SELECT id,restored_at,archive_sha256 FROM recovery_guard LIMIT 0`)
		if err != nil {
			return m, ErrInvalidBackup
		}
		r.Close()
	}
	return m, nil
}

type Preview struct {
	Manifest      Manifest `json:"manifest"`
	ArchiveSHA256 string   `json:"archive_sha256"`
}
type validated struct {
	root, database string
	preview        Preview
}

func (v *validated) close() { os.RemoveAll(v.root) }
func Inspect(ctx context.Context, path string) (Preview, error) {
	v, e := load(ctx, path)
	if e != nil {
		return Preview{}, e
	}
	defer v.close()
	return v.preview, nil
}

func load(ctx context.Context, path string) (*validated, error) {
	root, e := os.MkdirTemp("", "sim-inspect-")
	if e != nil {
		return nil, e
	}
	v := &validated{root: root, database: filepath.Join(root, "sim.db")}
	success := false
	defer func() {
		if !success {
			v.close()
		}
	}()
	source, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer source.Close()
	info, e := source.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > MaxArchive {
		return nil, ErrInvalidBackup
	}
	copyPath := filepath.Join(root, "input.zip")
	copyFile, e := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(copyFile, h), io.LimitReader(source, MaxArchive+1))
	ce := copyFile.Close()
	if e != nil {
		return nil, e
	}
	if ce != nil {
		return nil, ce
	}
	if n > MaxArchive {
		return nil, ErrInvalidBackup
	}
	v.preview.ArchiveSHA256 = hex.EncodeToString(h.Sum(nil))
	z, e := zip.OpenReader(copyPath)
	if e != nil {
		return nil, ErrInvalidBackup
	}
	defer z.Close()
	if len(z.File) != 2 {
		return nil, ErrInvalidBackup
	}
	seen := map[string]bool{}
	for _, f := range z.File {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if seen[f.Name] || !f.Mode().IsRegular() || (f.Name != "manifest.json" && f.Name != "sim.db") {
			return nil, ErrInvalidBackup
		}
		seen[f.Name] = true
		limit := store.BackupBytes
		if f.Name == "manifest.json" {
			limit = manifestLimit
		}
		if f.UncompressedSize64 > uint64(limit) {
			return nil, ErrInvalidBackup
		}
		reader, e := f.Open()
		if e != nil {
			return nil, ErrInvalidBackup
		}
		if f.Name == "manifest.json" {
			d := json.NewDecoder(io.LimitReader(reader, manifestLimit+1))
			d.DisallowUnknownFields()
			e = d.Decode(&v.preview.Manifest)
			if e == nil && d.Decode(new(any)) != io.EOF {
				e = ErrInvalidBackup
			}
			reader.Close()
			if e != nil {
				return nil, ErrInvalidBackup
			}
		} else {
			out, e := os.OpenFile(v.database, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				reader.Close()
				return nil, e
			}
			n, e = io.Copy(out, io.LimitReader(reader, limit+1))
			ce = out.Close()
			reader.Close()
			if e != nil || ce != nil || n != int64(f.UncompressedSize64) {
				return nil, ErrInvalidBackup
			}
		}
	}
	m := v.preview.Manifest
	if !seen["sim.db"] || !seen["manifest.json"] || m.Format != 1 || m.Application != "sim" || m.Bytes <= 0 || m.Bytes > store.BackupBytes || len(m.SHA256) != 64 {
		return nil, ErrInvalidBackup
	}
	if _, e = time.Parse(time.RFC3339, m.CreatedAt); e != nil {
		return nil, ErrInvalidBackup
	}
	size, digest, e := fileDigest(v.database)
	if e != nil {
		return nil, e
	}
	if size != m.Bytes || digest != m.SHA256 {
		return nil, ErrInvalidBackup
	}
	actual, e := databaseInfo(ctx, v.database)
	if e != nil {
		return nil, e
	}
	if actual.Schema != m.Schema || len(actual.Counts) != len(m.Counts) {
		return nil, ErrInvalidBackup
	}
	for table, n := range actual.Counts {
		if declared, found := m.Counts[table]; !found || declared != n {
			return nil, ErrInvalidBackup
		}
	}
	success = true
	return v, nil
}
