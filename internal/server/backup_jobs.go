package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Battery233/sim-harbor/internal/maintenance"
	"github.com/Battery233/sim-harbor/internal/store"
	"golang.org/x/sys/unix"
)

const backupLifetime = time.Hour
const backupJobTimeout = 10 * time.Minute
const backupFileLimit = 20

var backupID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var backupDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var legacyBackupTemp = regexp.MustCompile(`^\.sim-backup-[0-9]+$`)
var errBackupStorage = errors.New("backup_storage_full")
var errBackupSpace = errors.New("backup_disk_space_low")
var errBackupFiles = errors.New("backup_files_unavailable")
var errBackupCleanup = errors.New("backup_cleanup_failed")

type backupFile struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Bytes             int64  `json:"bytes"`
	CreatedAt         int64  `json:"created_at"`
	Destination       string `json:"destination"`
	ExpiresAt         int64  `json:"expires_at,omitempty"`
	SHA256            string `json:"sha256"`
	IncludeRecordings bool   `json:"include_recordings,omitempty"`
	RecordingFiles    int    `json:"recording_files,omitempty"`
	Unavailable       bool   `json:"unavailable,omitempty"`
	SizeUnknown       bool   `json:"size_unknown,omitempty"`
}
type backupJob struct {
	ID                string `json:"id"`
	Destination       string `json:"destination"`
	State             string `json:"state"`
	Error             string `json:"error,omitempty"`
	IncludeRecordings bool   `json:"include_recordings,omitempty"`
}
type backupRepository struct {
	mu        sync.Mutex
	root      *os.Root
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closed    bool
	job       *backupJob
	limit     int64
	downloads map[string]int
	evidence  map[string]*migrationBackup
	damaged   map[string]bool
}

// All repository mutation is under mu. The process data-directory lease excludes
// another instance. Pending names are private work, never downloadable archives.
func (s *Server) initBackups() (*backupRepository, error) {
	s.backupsOnce.Do(func() {
		dir := filepath.Join(s.Config.DataDir, "backups")
		if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			s.backupsErr = err
			return
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			s.backupsErr = errBackupFiles
			return
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			s.backupsErr = err
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		limit := s.Config.BackupMaxStorageMiB
		if limit == 0 {
			limit = 2048
		}
		b := &backupRepository{root: root, ctx: ctx, cancel: cancel, limit: limit << 20, downloads: map[string]int{}, evidence: map[string]*migrationBackup{}, damaged: map[string]bool{}}
		// A crash cannot turn a half-written archive into a successful backup.
		entries, err := fs.ReadDir(root.FS(), ".")
		if err == nil {
			for _, entry := range entries {
				if backupWorkName(entry.Name()) || entry.Name() == ".job.tmp" {
					if err = root.RemoveAll(entry.Name()); err != nil {
						break
					}
				}
			}
		}
		if err == nil {
			if _, e := root.Lstat(".job.private.json"); e == nil {
				var job backupJob
				err = readBackupJSON(root, ".job.private.json", &job)
				if err == nil && (!backupID.MatchString(job.ID) || job.Destination != "browser" && job.Destination != "server") {
					err = errBackupFiles
				}
				if err == nil {
					b.job = &job
					if job.State == "running" {
						if _, e := b.fileLocked(job.ID); e == nil {
							job.State, job.Error = "ready", ""
						} else {
							job.State, job.Error = "failed", "backup_interrupted"
						}
						err = b.saveJobLocked()
					}
				}
			} else if !os.IsNotExist(e) {
				err = e
			}
		}
		// Legacy synchronous downloads used this exact temporary-directory pattern.
		// Never touch backups, restore work, or other project files.
		if err == nil {
			var old []os.DirEntry
			old, err = os.ReadDir(s.Config.DataDir)
			for _, entry := range old {
				if err == nil && legacyBackupTemp.MatchString(entry.Name()) && entry.IsDir() {
					err = os.RemoveAll(filepath.Join(s.Config.DataDir, entry.Name()))
				}
			}
		}
		if err == nil {
			_, err = b.filesLocked(time.Now())
		}
		if err != nil {
			cancel()
			root.Close()
			s.backupsErr = err
			return
		}
		s.backups = b
	})
	return s.backups, s.backupsErr
}

func (s *Server) RunBackups(ctx context.Context) {
	b, err := s.initBackups()
	if err != nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.CloseBackups()
			return
		case <-b.ctx.Done():
			return
		case now := <-ticker.C:
			b.mu.Lock()
			if !b.closed {
				_, _ = b.filesLocked(now)
			}
			b.mu.Unlock()
		}
	}
}
func (s *Server) CloseBackups() {
	// Initialization here also performs crash cleanup when shutdown races the first request.
	b, err := s.initBackups()
	if err != nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		b.wg.Wait()
		return
	}
	b.closed = true
	b.cancel()
	b.mu.Unlock()
	b.wg.Wait()
	b.root.Close()
}
func readBackupJSON(root *os.Root, path string, value any) error {
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return errBackupFiles
	}
	f, err := root.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 4097))
	dec.DisallowUnknownFields()
	if err = dec.Decode(value); err != nil {
		return errBackupFiles
	}
	if dec.Decode(new(any)) != io.EOF {
		return errBackupFiles
	}
	return nil
}

func backupWorkName(name string) bool {
	for _, prefix := range []string{".pending-", ".deleting-"} {
		if strings.HasPrefix(name, prefix) && backupID.MatchString(strings.TrimPrefix(name, prefix)) {
			return true
		}
	}
	return false
}

// A durable tombstone keeps a failed deletion from exposing a half-removed ZIP.
// Only explicitly deleted files and expired browser files enter this state.
func (b *backupRepository) removeFileLocked(id string) error {
	target := ".deleting-" + id
	if err := b.root.Rename(id, target); err != nil {
		return err
	}
	if err := b.syncLocked(); err != nil {
		return err
	}
	if err := b.root.RemoveAll(target); err != nil {
		return err
	}
	if err := b.syncLocked(); err != nil {
		return err
	}
	delete(b.downloads, id)
	return nil
}
func writeBackupJSON(root *os.Root, path string, value any) error {
	p, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(p)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	return err
}
func (b *backupRepository) syncLocked() error {
	f, err := b.root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (b *backupRepository) saveJobLocked() error {
	if err := b.root.Remove(".job.tmp"); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := writeBackupJSON(b.root, ".job.tmp", b.job); err != nil {
		return err
	}
	if err := b.root.Rename(".job.tmp", ".job.private.json"); err != nil {
		return err
	}
	return b.syncLocked()
}
func (b *backupRepository) fileLocked(id string) (backupFile, error) {
	var f backupFile
	if !backupID.MatchString(id) {
		return f, os.ErrNotExist
	}
	info, err := b.root.Lstat(id)
	if err != nil {
		return f, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return f, errBackupFiles
	}
	if err = readBackupJSON(b.root, filepath.Join(id, "metadata.json"), &f); err != nil {
		return f, err
	}
	if f.Unavailable || f.SizeUnknown || f.RecordingFiles < 0 || f.RecordingFiles > store.RecordingBackupFiles || (!f.IncludeRecordings && f.Bytes > maintenance.DatabaseArchiveLimit) || f.ID != id || f.CreatedAt <= 0 || f.Bytes <= 0 || f.Bytes > maintenance.MaxArchive || !backupDigest.MatchString(f.SHA256) ||
		f.Name != "sim-backup-"+time.Unix(f.CreatedAt, 0).UTC().Format("20060102T150405Z")+"-"+id+".zip" ||
		f.Destination != "server" && f.Destination != "browser" ||
		f.Destination == "server" && f.ExpiresAt != 0 || f.Destination == "browser" && f.ExpiresAt != f.CreatedAt+int64(backupLifetime/time.Second) {
		return f, errBackupFiles
	}
	info, err = b.root.Lstat(filepath.Join(id, f.Name))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != f.Bytes {
		return f, errBackupFiles
	}
	return f, nil
}

// Damaged items remain visible and count toward the quota. Never trust their
// metadata for paths, expiry or capacity, and never follow a symlink to count it.
func (b *backupRepository) damagedFileLocked(id string) backupFile {
	f := backupFile{ID: id, Unavailable: true}
	entries := 0
	err := fs.WalkDir(b.root.FS(), id, func(path string, entry fs.DirEntry, err error) error {
		entries++
		if err != nil || entries > 10002 {
			return errBackupFiles
		}
		info, err := entry.Info()
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errBackupFiles
		}
		if info.Mode().IsRegular() {
			if info.Size() < 0 || info.Size() > int64(^uint64(0)>>1)-f.Bytes {
				return errBackupFiles
			}
			f.Bytes += info.Size()
		}
		return nil
	})
	f.SizeUnknown = err != nil
	if evidence := b.evidence[id]; evidence != nil {
		evidence.invalid.Store(true)
	}
	return f
}

func (b *backupRepository) filesLocked(now time.Time) ([]backupFile, error) {
	files := []backupFile{}
	entries, err := fs.ReadDir(b.root.FS(), ".")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".deleting-") && backupWorkName(entry.Name()) {
			if err := b.root.RemoveAll(entry.Name()); err != nil {
				return nil, err
			}
			continue
		}
		if !backupID.MatchString(entry.Name()) {
			continue
		}
		f, err := b.fileLocked(entry.Name())
		if err != nil || b.damaged[entry.Name()] {
			files = append(files, b.damagedFileLocked(entry.Name()))
			continue
		}
		if f.ExpiresAt != 0 && now.Unix() >= f.ExpiresAt && b.downloads[f.ID] == 0 {
			if err = b.removeFileLocked(f.ID); err != nil {
				return nil, err
			}
			delete(b.evidence, f.ID)
			continue
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].CreatedAt == files[j].CreatedAt {
			return files[i].ID > files[j].ID
		}
		return files[i].CreatedAt > files[j].CreatedAt
	})
	return files, nil
}
func backupFailure(err error) string {
	switch {
	case errors.Is(err, errBackupCleanup):
		return "backup_cleanup_failed"
	case errors.Is(err, store.ErrRecordingBackup):
		return "recording_backup_incomplete"
	case errors.Is(err, store.ErrBackupLimit):
		return "backup_limit_exceeded"
	case errors.Is(err, errBackupStorage):
		return "backup_storage_full"
	case errors.Is(err, errBackupSpace), errors.Is(err, unix.ENOSPC):
		return "backup_disk_space_low"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "backup_interrupted"
	default:
		return "backup_failed"
	}
}
func (s *Server) backupReady(w http.ResponseWriter) *backupRepository {
	b, err := s.initBackups()
	if err != nil {
		reply(w, 503, map[string]string{"error": "backup_files_unavailable"})
		return nil
	}
	return b
}
func (s *Server) backupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/backups", s.listBackups)
	mux.HandleFunc("POST /api/backups", s.startBackup)
	mux.HandleFunc("GET /api/backups/{id}/download", s.downloadSavedBackup)
	mux.HandleFunc("POST /api/backups/{id}/delete", s.deleteBackup)
}
func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	b := s.backupReady(w)
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		reply(w, 503, map[string]string{"error": "backup_files_unavailable"})
		return
	}
	files, err := b.filesLocked(time.Now())
	if err != nil {
		reply(w, 503, map[string]string{"error": "backup_files_unavailable"})
		return
	}
	var used int64
	unknown := false
	for _, f := range files {
		if f.Bytes > int64(^uint64(0)>>1)-used {
			unknown = true
		} else {
			used += f.Bytes
		}
		unknown = unknown || f.SizeUnknown
	}
	reply(w, 200, map[string]any{"items": files, "job": b.job, "storage_bytes": used, "storage_unknown": unknown, "max_storage_bytes": b.limit, "max_files": backupFileLimit, "directory": filepath.Join(s.Config.DataDir, "backups")})
}
func (s *Server) startBackup(w http.ResponseWriter, r *http.Request) {
	var action struct {
		Confirmed         bool   `json:"confirmed"`
		ID                string `json:"id"`
		Destination       string `json:"destination"`
		IncludeRecordings bool   `json:"include_recordings"`
	}
	if !decodeAction(w, r, &action) {
		return
	}
	if !action.Confirmed || !backupID.MatchString(action.ID) || action.Destination != "browser" && action.Destination != "server" {
		reply(w, 400, map[string]string{"error": "backup_confirmation_required"})
		return
	}
	b := s.backupReady(w)
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || s.restarting.Load() {
		reply(w, 503, map[string]string{"error": "backup_interrupted"})
		return
	}
	if b.job != nil && b.job.ID == action.ID {
		if b.job.Destination != action.Destination || b.job.IncludeRecordings != action.IncludeRecordings {
			reply(w, 409, map[string]string{"error": "backup_request_conflict"})
			return
		}
		reply(w, 200, b.job)
		return
	}
	if f, err := b.fileLocked(action.ID); err == nil {
		if f.Destination != action.Destination || f.IncludeRecordings != action.IncludeRecordings {
			reply(w, 409, map[string]string{"error": "backup_request_conflict"})
			return
		}
		reply(w, 200, backupJob{ID: f.ID, Destination: f.Destination, State: "ready", IncludeRecordings: f.IncludeRecordings})
		return
	} else if !os.IsNotExist(err) {
		reply(w, 503, map[string]string{"error": "backup_files_unavailable"})
		return
	}
	if !s.backupGate.TryLock() {
		reply(w, 409, map[string]string{"error": "backup_in_progress"})
		return
	}
	if !s.migrationGate.TryLock() {
		s.backupGate.Unlock()
		reply(w, 409, map[string]string{"error": "backup_in_progress"})
		return
	}
	started := false
	defer func() {
		if !started {
			s.migrationGate.Unlock()
			s.backupGate.Unlock()
		}
	}()
	files, err := b.filesLocked(time.Now())
	if err == nil {
		err = s.backupSpaceLocked(r.Context(), b, files, action.IncludeRecordings)
	}
	if err != nil {
		reply(w, 503, map[string]string{"error": backupFailure(err)})
		return
	}
	s.migrationBackup, s.migrationPreview = nil, nil
	b.job = &backupJob{ID: action.ID, Destination: action.Destination, State: "running", IncludeRecordings: action.IncludeRecordings}
	if err = b.saveJobLocked(); err != nil {
		b.job.State, b.job.Error = "failed", "backup_failed"
		reply(w, 503, map[string]string{"error": "backup_failed"})
		return
	}
	b.wg.Add(1)
	started = true
	go s.generateBackup(b, *b.job)
	reply(w, 202, b.job)
}
func (s *Server) backupSpaceLocked(ctx context.Context, b *backupRepository, files []backupFile, withAudio bool) error {
	var pages, size int64
	if err := s.Store.DB.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return err
	}
	if err := s.Store.DB.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return err
	}
	if size <= 0 || pages <= 0 || pages > store.BackupBytes/size {
		return store.ErrBackupLimit
	}
	estimate := pages * size
	if withAudio {
		audio, _, err := s.Store.VoicemailDirectoryStats(ctx, s.Config.DataDir)
		if err != nil {
			return err
		}
		if audio > store.RecordingBackupBytes {
			return store.ErrBackupLimit
		}
		estimate += audio
	}
	var used int64
	for _, f := range files {
		if f.SizeUnknown {
			return errBackupFiles
		}
		if f.Bytes > b.limit-used {
			return errBackupStorage
		}
		used += f.Bytes
	}
	if len(files) >= backupFileLimit || used+estimate+(32<<20) > b.limit {
		return errBackupStorage
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(b.root.Name(), &stat); err != nil {
		return err
	}
	// Snapshot and ZIP coexist. Leave a margin for growth and the running service.
	if uint64(stat.Bavail)*uint64(stat.Bsize) < uint64(2*estimate+(96<<20)) {
		return errBackupSpace
	}
	return nil
}
func (s *Server) generateBackup(b *backupRepository, job backupJob) {
	defer b.wg.Done()
	defer s.backupGate.Unlock()
	defer s.migrationGate.Unlock()
	ctx, cancel := context.WithTimeout(b.ctx, backupJobTimeout)
	defer cancel()
	f, before, err := s.createSavedBackup(ctx, b, job)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		after, e := s.Store.MigrationState(ctx)
		if e == nil && before.Blocked && before.Revision == after.Revision && before.LedgerRevision == after.LedgerRevision {
			evidence := &migrationBackup{revision: before.Revision, ledger: before.LedgerRevision, digest: f.SHA256, created: time.Now()}
			b.evidence[f.ID] = evidence
			if job.Destination == "server" {
				s.migrationBackup = evidence
			}
		}
		b.job.State, b.job.Error = "ready", ""
	} else {
		b.job.State, b.job.Error = "failed", backupFailure(err)
	}
	if e := b.saveJobLocked(); e != nil {
		b.job.State, b.job.Error = "failed", "backup_status_failed"
	}
}

func (s *Server) createSavedBackup(ctx context.Context, b *backupRepository, job backupJob) (f backupFile, before store.MigrationState, err error) {
	pending := ".pending-" + job.ID
	if err = b.root.Mkdir(pending, 0700); err != nil {
		return
	}
	defer func() {
		if e := b.root.RemoveAll(pending); e != nil {
			err = errBackupCleanup
		}
	}()
	before, err = s.Store.MigrationState(ctx)
	if err != nil {
		return
	}
	a, err := maintenance.Create(ctx, s.Store, filepath.Join(b.root.Name(), pending), maintenance.BackupOptions{IncludeRecordings: job.IncludeRecordings, DataDirectory: s.Config.DataDir})
	if err != nil {
		return f, before, err
	}
	defer a.Close()
	f = backupFile{ID: job.ID, CreatedAt: time.Now().Unix(), Destination: job.Destination, SHA256: a.SHA256, IncludeRecordings: job.IncludeRecordings, RecordingFiles: len(a.Manifest.Recordings)}
	if job.Destination == "browser" {
		f.ExpiresAt = f.CreatedAt + int64(backupLifetime/time.Second)
	}
	f.Name = "sim-backup-" + time.Unix(f.CreatedAt, 0).UTC().Format("20060102T150405Z") + "-" + f.ID + ".zip"
	info, err := os.Stat(a.Path)
	if err != nil {
		return f, before, err
	}
	f.Bytes = info.Size()
	if err = os.Rename(a.Path, filepath.Join(b.root.Name(), pending, f.Name)); err != nil {
		return
	}
	// Remove the raw snapshot before publishing. Never retain a second database
	// alongside the ZIP, including when disk permissions prevent cleanup.
	if err = a.Close(); err != nil {
		return f, before, errBackupCleanup
	}
	if err = writeBackupJSON(b.root, filepath.Join(pending, "metadata.json"), f); err != nil {
		return
	}
	dir, err := b.root.Open(pending)
	if err != nil {
		return f, before, err
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return
	}
	files, err := b.filesLocked(time.Now())
	if err != nil {
		return f, before, err
	}
	var used int64
	for _, existing := range files {
		if existing.SizeUnknown {
			return f, before, errBackupFiles
		}
		if existing.Bytes > b.limit-used {
			return f, before, errBackupStorage
		}
		used += existing.Bytes
	}
	if len(files) >= backupFileLimit || used+f.Bytes > b.limit {
		return f, before, errBackupStorage
	}
	if err = b.root.Rename(pending, job.ID); err != nil {
		return
	}
	err = b.syncLocked()
	return
}
func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	var action struct {
		Confirmed bool `json:"confirmed"`
	}
	if !decodeAction(w, r, &action) {
		return
	}
	if !action.Confirmed {
		reply(w, 400, map[string]string{"error": "backup_delete_confirmation_required"})
		return
	}
	b := s.backupReady(w)
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := r.PathValue("id")
	if !backupID.MatchString(id) {
		reply(w, 404, map[string]string{"error": "backup_not_found"})
		return
	}
	if b.downloads[id] > 0 {
		reply(w, 409, map[string]string{"error": "backup_download_in_progress"})
		return
	}
	if !s.migrationGate.TryLock() {
		reply(w, 409, map[string]string{"error": "backup_in_progress"})
		return
	}
	defer s.migrationGate.Unlock()
	// Deletion needs an explicitly confirmed, confined ID, not readable metadata
	// or an intact ZIP. RemoveAll unlinks symlinks rather than following them.
	if _, err := b.root.Lstat(id); err != nil {
		reply(w, 404, map[string]string{"error": "backup_not_found"})
		return
	}
	if err := b.removeFileLocked(id); err != nil {
		reply(w, 503, map[string]string{"error": "backup_cleanup_failed"})
		return
	}
	if b.evidence[id] != nil && s.migrationBackup == b.evidence[id] {
		s.migrationBackup, s.migrationPreview = nil, nil
	}
	delete(b.evidence, id)
	delete(b.damaged, id)
	reply(w, 200, map[string]bool{"deleted": true})
}
func (s *Server) downloadSavedBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		reply(w, http.StatusMethodNotAllowed, map[string]string{"error": "backup_download_requires_get"})
		return
	}
	b := s.backupReady(w)
	if b == nil {
		return
	}
	id := r.PathValue("id")
	b.mu.Lock()
	f, err := b.fileLocked(id)
	if err != nil || f.ExpiresAt != 0 && time.Now().Unix() >= f.ExpiresAt {
		if evidence := b.evidence[id]; evidence != nil {
			evidence.invalid.Store(true)
		}
		b.mu.Unlock()
		reply(w, 404, map[string]string{"error": "backup_not_found"})
		return
	}
	if b.damaged[id] {
		b.mu.Unlock()
		reply(w, 503, map[string]string{"error": "backup_file_unavailable"})
		return
	}
	total := 0
	for _, n := range b.downloads {
		total += n
	}
	if total >= 2 {
		b.mu.Unlock()
		reply(w, 409, map[string]string{"error": "backup_download_in_progress"})
		return
	}
	file, err := b.root.Open(filepath.Join(id, f.Name))
	if err != nil {
		b.damaged[id] = true
		if evidence := b.evidence[id]; evidence != nil {
			evidence.invalid.Store(true)
		}
		b.mu.Unlock()
		reply(w, 503, map[string]string{"error": "backup_file_unavailable"})
		return
	}
	b.downloads[id]++
	evidence := b.evidence[id]
	b.mu.Unlock()
	defer file.Close()
	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.downloads[id] > 1 {
			b.downloads[id]--
		} else {
			delete(b.downloads, id)
		}
	}()
	// Keep the old migration rule: a browser transfer must finish in full before
	// it can serve as evidence. Disk-only completion is recorded by the worker.
	locked := s.migrationGate.TryLock()
	if locked {
		defer s.migrationGate.Unlock()
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	// Verify the stored archive before sending a successful file response. This
	// uses bounded memory and leaves a damaged file in place for investigation.
	check := sha256.New()
	if _, err := io.Copy(check, &backupReader{ctx: r.Context(), reader: file}); err != nil || hex.EncodeToString(check.Sum(nil)) != f.SHA256 {
		// A canceled transfer is not evidence of file corruption.
		if r.Context().Err() == nil {
			b.mu.Lock()
			b.damaged[id] = true
			if evidence != nil {
				evidence.invalid.Store(true)
			}
			b.mu.Unlock()
		}
		reply(w, 503, map[string]string{"error": "backup_files_unavailable"})
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		reply(w, 503, map[string]string{"error": "backup_files_unavailable"})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("Content-Disposition", `attachment; filename="`+f.Name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(f.Bytes, 10))
	digest := sha256.New()
	n, copyErr := io.Copy(w, io.TeeReader(&backupReader{ctx: r.Context(), reader: file}, digest))
	if locked && copyErr == nil && r.Context().Err() == nil && n == f.Bytes && hex.EncodeToString(digest.Sum(nil)) == f.SHA256 && evidence != nil && time.Since(evidence.created) < migrationWindow {
		after, e := s.Store.MigrationState(r.Context())
		if e == nil && after.Blocked && evidence.revision == after.Revision && evidence.ledger == after.LedgerRevision {
			s.migrationBackup = evidence
		}
	}
}

type backupReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *backupReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
