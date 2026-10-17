package diagnostics

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Fields struct {
	SMSGroup          string `json:"sms_group,omitempty"`
	SMSTotal          int    `json:"sms_total,omitempty"`
	SMSParts          string `json:"sms_parts,omitempty"`
	SMSInvalidParts   int    `json:"sms_invalid_parts,omitempty"`
	SMSComplete       *bool  `json:"sms_complete,omitempty"`
	SMSStoredComplete *bool  `json:"sms_stored_complete,omitempty"`
	SMSTime           int64  `json:"sms_time,omitempty"`
	MessageID         int64  `json:"message_id,omitempty"`
	Session           string `json:"session,omitempty"`
	Operation         string `json:"operation,omitempty"`
	Transaction       string `json:"transaction,omitempty"`
	Type              int    `json:"event_type,omitempty"`
	SubType           int    `json:"sub_type,omitempty"`
	Slot              int    `json:"slot,omitempty"`
	DurationMS        int64  `json:"duration_ms,omitempty"`
	Count             int    `json:"count,omitempty"`
	Outcome           string `json:"outcome,omitempty"`
}
type Event struct {
	ID   int64  `json:"id"`
	Time int64  `json:"time_ms"`
	Code string `json:"code"`
	Definition
	Fields
}
type Status struct {
	Level          string `json:"level"`
	Retained       int    `json:"retained"`
	Limit          int    `json:"limit"`
	FileEnabled    bool   `json:"file_enabled"`
	FileOK         bool   `json:"file_ok"`
	RetentionDays  int    `json:"retention_days"`
	MaxDiskBytes   int64  `json:"max_disk_bytes"`
	CapacityPaused bool   `json:"capacity_paused"`
}
type Filter struct {
	Level, Category, Reference string
	Before                     int64
}
type Recorder struct {
	mu        sync.Mutex
	level     string
	limit     int
	sequence  int64
	events    []Event
	output    io.Writer
	file      *rotatingFile
	fileOK    bool
	retention time.Duration
	diskFull  bool
}

// Reference hashes externally supplied identifiers, including a modem's tid.
// The same input correlates events without copying arbitrary strings to logs.
func Reference(value string) string {
	if value == "" {
		return ""
	}
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:12])
}
func validReference(v string) bool {
	if len(v) != 24 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func clean(f Fields) Fields {
	if !validReference(f.SMSGroup) {
		f.SMSGroup = ""
	}
	if f.SMSTotal < 0 || f.SMSTotal > 255 {
		f.SMSTotal = 0
	}
	if f.SMSTime < 1 || f.SMSTime > 9999999999 {
		f.SMSTime = 0
	}
	if f.MessageID < 0 {
		f.MessageID = 0
	}
	if f.SMSInvalidParts < 0 || f.SMSInvalidParts > 65536 {
		f.SMSInvalidParts = 0
	}
	f.SMSParts = cleanPartNumbers(f.SMSParts)
	if f.SMSComplete != nil {
		value := *f.SMSComplete
		f.SMSComplete = &value
	}
	if f.SMSStoredComplete != nil {
		value := *f.SMSStoredComplete
		f.SMSStoredComplete = &value
	}
	if !validReference(f.Session) {
		f.Session = ""
	}
	if !validReference(f.Operation) {
		f.Operation = ""
	}
	if !validReference(f.Transaction) {
		f.Transaction = ""
	}
	if f.Type < 0 || f.Type > 9999 {
		f.Type = 0
	}
	if f.SubType < 0 || f.SubType > 9999 {
		f.SubType = 0
	}
	if f.Slot != 1 && f.Slot != 2 {
		f.Slot = 0
	}
	if f.DurationMS < 0 || f.DurationMS > 86400000 {
		f.DurationMS = 0
	}
	if f.Count < 0 || f.Count > 1000000 {
		f.Count = 0
	}
	if !outcomes[f.Outcome] {
		f.Outcome = ""
	}
	return f
}

func cleanPartNumbers(value string) string {
	if value == "" || len(value) > 1024 {
		return ""
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(value, ",") {
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 255 {
			return ""
		}
		seen[n] = true
	}
	parts := make([]int, 0, len(seen))
	for n := range seen {
		parts = append(parts, n)
	}
	sort.Ints(parts)
	out := make([]string, 0, len(parts))
	for _, n := range parts {
		out = append(out, strconv.Itoa(n))
	}
	return strings.Join(out, ",")
}
func priority(level string) int {
	switch level {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn":
		return 2
	case "error":
		return 3
	}
	return -1
}
func ValidFilter(f Filter) bool {
	return (f.Level == "" || priority(f.Level) >= 0) &&
		(f.Category == "" || f.Category == "service" || f.Category == "tcp" || f.Category == "query" || f.Category == "sms" || f.Category == "notification") &&
		(f.Reference == "" || validReference(f.Reference)) && f.Before >= 0
}
func New(level string, limit int, output io.Writer) *Recorder {
	if priority(level) < 0 {
		level = "info"
	}
	if limit < 1 || limit > 5000 {
		limit = 5000
	}
	return &Recorder{level: level, limit: limit, output: output}
}

// Open uses only a fixed log name inside the private application data directory.
func Open(dataDir, level string, limit int, maxBytes int64, backups int, output io.Writer, retentionDays ...int) (*Recorder, error) {
	if maxBytes < 65536 || maxBytes > 16*1024*1024 || backups < 1 || backups > 5 {
		return nil, errors.New("invalid logging limits")
	}
	r := New(level, limit, output)
	r.retention = 7 * 24 * time.Hour
	if len(retentionDays) > 1 || len(retentionDays) == 1 && (retentionDays[0] < 1 || retentionDays[0] > 30) {
		return nil, errors.New("invalid log retention days")
	}
	if len(retentionDays) == 1 {
		r.retention = time.Duration(retentionDays[0]) * 24 * time.Hour
	}
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("log directory unavailable")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid log directory")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, errors.New("log directory permissions unavailable")
	}
	f := &rotatingFile{path: filepath.Join(dir, "service.jsonl"), max: maxBytes, backups: backups}
	f.protectRecent = r.retention > 0
	if r.retention > 0 {
		if err = f.prune(time.Now().Add(-r.retention).UnixMilli()); err != nil {
			return nil, errors.New("log retention cleanup unavailable")
		}
	}
	skipped := 0
	for i := backups; i >= 0; i-- {
		path := f.name(i)
		if err = regular(path); err != nil {
			return nil, errors.New("invalid log file")
		}
		p, e := os.Open(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, errors.New("log history unavailable")
		}
		scan := bufio.NewScanner(io.LimitReader(p, maxBytes))
		scan.Buffer(make([]byte, 1024), 8192)
		for scan.Scan() {
			var event Event
			decoder := json.NewDecoder(bytes.NewReader(scan.Bytes()))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&event) != nil || decoder.Decode(new(any)) != io.EOF {
				skipped++
				continue
			}
			d, known := catalog[event.Code]
			if !known || event.ID <= r.sequence || event.ID > 9007199254740991 || event.Time <= 0 || event.Time > time.Now().Add(time.Minute).UnixMilli() {
				skipped++
				continue
			}
			event.Definition, event.Fields = d, clean(event.Fields)
			r.sequence = event.ID
			r.append(event)
		}
		if scan.Err() != nil {
			skipped++
		}
		p.Close()
	}
	if err = f.open(); err != nil {
		return nil, errors.New("log file unavailable")
	}
	r.file, r.fileOK = f, true
	if skipped > 0 {
		r.Record("logging_restore_skipped", Fields{Count: skipped})
	}
	return r, nil
}
func (r *Recorder) append(e Event) {
	if len(r.events) == r.limit {
		copy(r.events, r.events[1:])
		r.events[len(r.events)-1] = e
	} else {
		r.events = append(r.events, e)
	}
}
func (r *Recorder) Record(code string, fields Fields) {
	if r == nil {
		return
	}
	d, known := catalog[code]
	if !known || priority(d.Level) < priority(r.level) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	e := Event{ID: r.sequence, Time: time.Now().UnixMilli(), Code: code, Definition: d, Fields: clean(fields)}
	r.append(e)
	p, _ := json.Marshal(e)
	p = append(p, '\n')
	if r.output != nil {
		_, _ = r.output.Write(p)
	}
	if r.file != nil && r.fileOK {
		if _, err := r.file.Write(p); err != nil {
			r.fileOK = false
			r.diskFull = errors.Is(err, errLogCapacity)
			r.sequence++
			code := "logging_write_failed"
			if r.diskFull {
				code = "logging_capacity_reached"
			}
			failure := Event{ID: r.sequence, Time: time.Now().UnixMilli(), Code: code, Definition: catalog[code]}
			r.append(failure)
			if r.output != nil {
				b, _ := json.Marshal(failure)
				_, _ = r.output.Write(append(b, '\n'))
			}
		}
	}
}
func (r *Recorder) Snapshot(filter Filter, limit int) ([]Event, Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := Status{Level: r.level, Retained: len(r.events), Limit: r.limit, FileEnabled: r.file != nil, FileOK: r.fileOK, RetentionDays: int(r.retention / (24 * time.Hour)), CapacityPaused: r.diskFull}
	if r.file != nil {
		status.MaxDiskBytes = r.file.max * int64(r.file.backups+1)
	}
	rows := []Event{}
	if !ValidFilter(filter) || limit < 1 {
		return rows, status
	}
	if limit > r.limit {
		limit = r.limit
	}
	for i := len(r.events) - 1; i >= 0 && len(rows) < limit; i-- {
		e := r.events[i]
		if filter.Before > 0 && e.ID >= filter.Before || filter.Level != "" && e.Level != filter.Level || filter.Category != "" && e.Category != filter.Category {
			continue
		}
		if filter.Reference != "" && filter.Reference != e.Session && filter.Reference != e.Transaction && filter.Reference != e.Operation && filter.Reference != e.SMSGroup {
			continue
		}
		if e.SMSComplete != nil {
			value := *e.SMSComplete
			e.SMSComplete = &value
		}
		if e.SMSStoredComplete != nil {
			value := *e.SMSStoredComplete
			e.SMSStoredComplete = &value
		}
		rows = append(rows, e)
	}
	return rows, status
}
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

type rotatingFile struct {
	path          string
	max           int64
	backups       int
	file          *os.File
	size          int64
	protectRecent bool
}

func (f *rotatingFile) name(i int) string {
	if i == 0 {
		return f.path
	}
	return f.path + "." + strconv.Itoa(i)
}
func regular(path string) error {
	i, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !i.Mode().IsRegular() {
		return errors.New("invalid log file")
	}
	return nil
}
func (f *rotatingFile) open() error {
	if err := regular(f.path); err != nil {
		return err
	}
	p, err := os.OpenFile(f.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err = p.Chmod(0600); err != nil {
		p.Close()
		return err
	}
	i, err := p.Stat()
	if err != nil {
		p.Close()
		return err
	}
	f.file, f.size = p, i.Size()
	// A crash can leave a partial final line. Isolate it from the next event.
	if f.size > 0 {
		var last [1]byte
		if _, err = p.ReadAt(last[:], f.size-1); err != nil {
			p.Close()
			return err
		}
		if last[0] != '\n' {
			n, e := p.Write([]byte{'\n'})
			f.size += int64(n)
			if e != nil {
				p.Close()
				return e
			}
		}
	}
	return nil
}
func (f *rotatingFile) Write(p []byte) (int, error) {
	if int64(len(p)) > f.max {
		return 0, errors.New("log record too large")
	}
	if f.size+int64(len(p)) > f.max {
		for i := 0; i <= f.backups; i++ {
			if err := regular(f.name(i)); err != nil {
				return 0, err
			}
		}
		if f.protectRecent {
			if info, err := os.Stat(f.name(f.backups)); err == nil && info.Size() > 0 {
				return 0, errLogCapacity
			} else if err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		}
		if err := f.file.Close(); err != nil {
			return 0, err
		}
		if err := os.Remove(f.name(f.backups)); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		for i := f.backups - 1; i >= 0; i-- {
			if err := os.Rename(f.name(i), f.name(i+1)); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		}
		if err := f.open(); err != nil {
			return 0, err
		}
	}
	n, err := f.file.Write(p)
	f.size += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
func (f *rotatingFile) Close() error { return f.file.Close() }
