package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Battery233/sim-harbor/internal/protocol"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB          *sql.DB
	voicemailMu sync.Mutex
}
type Message struct {
	Multipart     bool   `json:"multipart"`
	PartsTotal    int    `json:"parts_total"`
	PartsReceived int    `json:"parts_received"`
	PartIndices   []int  `json:"part_indices"`
	CardID        string `json:"card_id"`
	CardName      string `json:"card_name"`
	ID            int64  `json:"id"`
	Slot          int    `json:"slot"`
	Phone         string `json:"phone"`
	Body          string `json:"body"`
	Timestamp     int64  `json:"timestamp"`
	Complete      bool   `json:"complete"`
	Read          bool   `json:"read"`
}
type Notification struct {
	AlertID     int64
	AlertKey    string
	AlertKind   string
	AlertLabel  string
	CallKind    string
	CallID      int64
	CallStartTS int64
	RingSeconds int64
	Phone       string
	ID          int64
	MessageID   int64
	Attempts    int
	Slot        int
	Body        string
	CardName    string
	CardGroup   string
}

func Open(path string) (*Store, error) {
	for _, marker := range []string{".restore-incomplete", ".sim.db.pending"} {
		if _, e := os.Lstat(filepath.Join(filepath.Dir(path), marker)); e == nil || !os.IsNotExist(e) {
			return nil, errors.New("restore_incomplete")
		}
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	// SQLite derives new sidecar permissions from the main file. Establish 0600
	// before opening it, rather than tightening permissions only after migration.
	if e = privateDatabase(abs); e != nil {
		return nil, e
	}
	// These settings belong to each physical connection, including replacements.
	dsn := url.URL{Scheme: "file", Path: abs, RawQuery: url.Values{"_pragma": {"foreign_keys(ON)", "busy_timeout(5000)", "synchronous(FULL)"}}.Encode()}
	db, e := sql.Open("sqlite", dsn.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if e = s.migrate(); e != nil {
		db.Close()
		return nil, e
	}
	if e = s.reconcileVoicemails(time.Now().Unix()); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}

func privateDatabase(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if suffix == "" {
			flags |= unix.O_CREAT
		}
		fd, err := unix.Open(path+suffix, flags, 0600)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			return errors.New("database_file_unavailable")
		}
		f := os.NewFile(uintptr(fd), "sim-database")
		info, err := f.Stat()
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("invalid_database_file")
		}
		if err == nil {
			err = f.Chmod(0600)
		}
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return errors.New("database_file_unavailable")
		}
	}
	return nil
}
func (s *Store) migrate() error {
	_, e := s.DB.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`
CREATE TABLE IF NOT EXISTS schema_version(version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS events(hash TEXT PRIMARY KEY,type INTEGER NOT NULL,received_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS device(id TEXT PRIMARY KEY,last_seen INTEGER NOT NULL,status TEXT NOT NULL DEFAULT '{}');
CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY,key TEXT UNIQUE NOT NULL,slot INTEGER NOT NULL,phone TEXT NOT NULL,body TEXT NOT NULL,sms_ts INTEGER NOT NULL,complete INTEGER NOT NULL,is_read INTEGER NOT NULL DEFAULT 0,fragments TEXT NOT NULL DEFAULT '{}');
CREATE TABLE IF NOT EXISTS notifications(id INTEGER PRIMARY KEY,message_id INTEGER UNIQUE NOT NULL REFERENCES messages(id),state TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '');
`)
	if e != nil {
		return e
	}
	var version sql.NullInt64
	if e = tx.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); e != nil {
		return e
	}
	if version.Int64 > SchemaVersion {
		return errors.New("database schema newer than application")
	}
	if version.Int64 < 2 {
		if _, e = tx.Exec(`ALTER TABLE device ADD COLUMN status_received_at INTEGER NOT NULL DEFAULT 0; DELETE FROM schema_version; INSERT INTO schema_version VALUES(2);`); e != nil {
			return e
		}
	}
	if version.Int64 < 3 {
		if _, e = tx.Exec(outgoingSchema); e != nil {
			return e
		}
	}
	if version.Int64 < 4 {
		if _, e = tx.Exec(`ALTER TABLE notifications ADD COLUMN manual_retry_at INTEGER NOT NULL DEFAULT 0;
CREATE TABLE notification_test(id INTEGER PRIMARY KEY CHECK(id=1),requested_at INTEGER NOT NULL,outcome TEXT NOT NULL);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(4);`); e != nil {
			return e
		}
	}
	if version.Int64 < 5 {
		if _, e = tx.Exec(recoverySchema); e != nil {
			return e
		}
	}
	if version.Int64 < 6 {
		if _, e = tx.Exec(`ALTER TABLE messages ADD COLUMN card_name TEXT NOT NULL DEFAULT '';
DELETE FROM schema_version; INSERT INTO schema_version VALUES(6);`); e != nil {
			return e
		}
	}
	if version.Int64 < 7 {
		if _, e = tx.Exec(cardSchema); e != nil {
			return e
		}
	}
	if version.Int64 < 8 {
		if _, e = tx.Exec(notificationStatsSchema); e != nil {
			return e
		}
	}
	if version.Int64 < 9 {
		if _, e = tx.Exec(messageDeletionSchema); e != nil {
			return e
		}
	}
	if version.Int64 < 10 {
		if _, e = tx.Exec(cardNumberSchema); e != nil {
			return e
		}
	}
	if version.Int64 < 11 {
		if _, e = tx.Exec(callSchema); e != nil {
			return e
		}
	}
	if version.Int64 < 12 {
		if _, e = tx.Exec(callTimeoutSchema); e != nil {
			return e
		}
	}
	if version.Int64 < 13 {
		if _, e = tx.Exec(`ALTER TABLE cards ADD COLUMN notification_enabled INTEGER NOT NULL DEFAULT 1 CHECK(notification_enabled IN (0,1)); DELETE FROM schema_version;INSERT INTO schema_version VALUES(13);`); e != nil {
			return e
		}
	}
	if version.Int64 < 14 {
		if _, e = tx.Exec(statusAlertSchema); e != nil {
			return e
		}
	}
	if version.Int64 < 15 {
		if e = migrateRecoveryConfirmation(tx); e != nil {
			return e
		}
	}
	if version.Int64 < 16 {
		if e = migrateMultipart(tx); e != nil {
			return e
		}
	}
	if version.Int64 < 18 {
		// v1.0.4 published schema 17 for sms_receipts. Earlier builds of this
		// branch used 17 for voicemail, so a database at 17 may lack either
		// table. Receipts stay 17; voicemail objects are added as 18.
		if _, e = tx.Exec(smsReceiptSchema); e != nil {
			return e
		}
		if e = migrateVoicemail(tx); e != nil {
			return e
		}
	}
	if version.Int64 < 19 {
		// The control ledger is created even when voicemail stays off. It records
		// paid setup SMS without the password or the command body.
		if e = migrateVoicemailControls(tx); e != nil {
			return e
		}
	}

	if _, e = tx.Exec(`UPDATE calls SET state='unknown' WHERE ended=0`); e != nil {
		return e
	}
	// Outstanding chargeable commands are NEVER replayed after restart.
	if _, e = tx.Exec(`UPDATE outgoing SET state='unknown' WHERE rank BETWEEN 1 AND 3 AND state IN ('waiting_device','accepted','processed');`); e != nil {
		return e
	}
	return tx.Commit()
}
func hash(p []byte) string { h := sha256.Sum256(p); return hex.EncodeToString(h[:]) }

func (s *Store) Ingest(ctx context.Context, e protocol.Event, raw []byte, notify bool, voicemailSlots ...int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO events VALUES(?,?,?)`, hash(raw), int(e.Type), now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO device(id,last_seen) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET last_seen=excluded.last_seen`, e.DeviceID, now)
	if err != nil {
		return err
	}
	if e.Type == 402 && e.SubType == 46 && e.Code != nil && *e.Code == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE device SET status=?,status_received_at=? WHERE id=?`, string(raw), now, e.DeviceID)
		if err != nil {
			return err
		}
	}
	if e.Type == 402 && e.SubType == 46 && e.Code != nil && *e.Code == 0 {
		if err = recordStatusCards(ctx, tx, e.SlotInfo); err != nil {
			return err
		}
	}
	if e.Type == 501 {
		if err = ingestSMS(ctx, tx, e, notify); err != nil {
			return err
		}
	}
	if protocol.IsIncomingCall(e.Type) {
		voicemail := false
		for _, slot := range voicemailSlots {
			if slot == int(e.Slot) {
				voicemail = true
			}
		}
		if err = ingestCall(ctx, tx, e, raw, notify, now, voicemail); err != nil {
			return err
		}
		if err = relinkVoicemails(ctx, tx, int(e.Slot), now); err != nil {
			return err
		}
	}
	if err = ingestOutgoing(ctx, tx, e, hash(raw), now); err != nil {
		return err
	}
	return tx.Commit()
}

func ingestSMS(ctx context.Context, tx *sql.Tx, e protocol.Event, notify bool) error {
	if e.Group != "" && (e.Max < 1 || e.Max > 255) {
		return ErrSMSFragmentInvalid
	}
	iccid := cardIdentity(e.ICCID)
	cardID, err := ensureCard(ctx, tx, iccid, e.CardName, false)
	if err != nil {
		return err
	}
	incoming := map[string]string{}
	if e.Group != "" && (e.Complete == nil || !*e.Complete) {
		incoming, err = parseSMSFragments(e.Fragments, int(e.Max))
		if err != nil {
			return err
		}
	}
	key, err := resolveSMSKey(ctx, tx, e, cardID, incoming)
	if err != nil {
		return err
	}
	parts := map[string]string{}
	body := e.Body
	complete := (e.Complete == nil && e.Group == "") || (e.Complete != nil && *e.Complete)
	var id int64
	var storedBody, storedParts string
	var storedComplete bool
	var deletedAt int64
	var total int
	var group string
	err = tx.QueryRowContext(ctx, `SELECT id,body,complete,fragments,deleted_at,sms_total,sms_group_ref FROM messages WHERE key=?`, key).Scan(&id, &storedBody, &storedComplete, &storedParts, &deletedAt, &total, &group)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if deletedAt != 0 {
		return nil
	}
	if e.Group != "" {
		if total > 0 && total != int(e.Max) {
			return ErrSMSTotalMismatch
		}
		total, group = int(e.Max), SMSGroupReference(e)
	}
	if storedParts != "" {
		if err = json.Unmarshal([]byte(storedParts), &parts); err != nil {
			return err
		}
	}
	for key := range parts {
		n, parseErr := strconv.Atoi(key)
		if parseErr != nil || n < 1 || n > 255 || (total > 0 && n > total) {
			return ErrSMSTotalMismatch
		}
	}
	if storedComplete {
		body = storedBody
		complete = true
	} else if !complete && e.Group != "" {
		for a, b := range incoming {
			if old, ok := parts[a]; ok && old != b {
				return ErrSMSFragmentConflict
			}
			parts[a] = b
		}
		if e.Max > 0 && len(parts) == int(e.Max) {
			var b strings.Builder
			complete = true
			for i := 1; i <= int(e.Max); i++ {
				v, ok := parts[strconv.Itoa(i)]
				if !ok {
					complete = false
					break
				}
				b.WriteString(v)
			}
			if complete {
				body = b.String()
			}
		}
		if body == "" && !complete {
			var partial strings.Builder
			for i := 1; i <= int(e.Max); i++ {
				partial.WriteString(parts[strconv.Itoa(i)])
			}
			body = partial.String()
		}
	}
	p, _ := json.Marshal(parts)
	// The first receipt owns the name snapshot, including an unknown name.
	// Replays/fragments must not relabel an old message after a SIM swap.
	_, err = tx.ExecContext(ctx, `INSERT INTO messages(key,slot,phone,body,sms_ts,complete,fragments,card_name,card_id,device_id,sms_total,sms_group_ref) VALUES(?,?,?,?,?,?,?,?,nullif(?,''),?,?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body,complete=excluded.complete,fragments=excluded.fragments,card_id=coalesce(messages.card_id,excluded.card_id),sms_total=excluded.sms_total,sms_group_ref=excluded.sms_group_ref`, key, int(e.Slot), e.Phone, body, int64(e.SMSTS), complete, string(p), cleanCardName(e.CardName), cardID, e.DeviceID, total, group)
	if err != nil {
		return err
	}
	if id == 0 {
		if err = tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE key=?`, key).Scan(&id); err != nil {
			return err
		}
	}
	if e.Group != "" && e.Max > 1 {
		receipt, _ := smsBusinessKeys(e)
		if _, err = tx.ExecContext(ctx, `INSERT INTO sms_receipts(key,message_id,sms_ts) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, receipt, id, int64(e.SMSTS)); err != nil {
			return err
		}
	}
	if notify {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO notifications(message_id,state) SELECT ?,CASE WHEN coalesce((SELECT notification_enabled FROM cards WHERE id=?),1)=1 THEN 'pending' ELSE 'canceled' END WHERE ? OR coalesce((SELECT notification_enabled FROM cards WHERE id=?),1)=0`, id, cardID, complete, cardID)
	}
	return err
}

func (s *Store) Messages(ctx context.Context, slot int, q string) ([]Message, error) {
	return s.MessagePage(ctx, slot, q, 0, 200)
}
func (s *Store) MessagePage(ctx context.Context, slot int, q string, before int64, limit int) ([]Message, error) {
	return s.CardMessagePage(ctx, "", slot, q, before, limit)
}

func (s *Store) Read(ctx context.Context, id int64) error {
	r, e := s.DB.ExecContext(ctx, `UPDATE messages SET is_read=1 WHERE id=? AND deleted_at=0`, id)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Message(ctx context.Context, id int64) (Message, error) {
	return scanMessage(s.DB.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id=? AND deleted_at=0`, id))
}

func (s *Store) State(ctx context.Context, id string) (map[string]any, error) {
	var ts, statusAt int64
	var raw string
	e := s.DB.QueryRowContext(ctx, `SELECT last_seen,status,status_received_at FROM device WHERE id=?`, id).Scan(&ts, &raw, &statusAt)
	if errors.Is(e, sql.ErrNoRows) {
		raw, e = "{}", nil
	}
	if e != nil {
		return nil, e
	}
	var reported map[string]any
	if e = json.Unmarshal([]byte(raw), &reported); e != nil {
		return nil, e
	}
	var pending, failed int
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(attempts>0),0) FROM (SELECT state,attempts FROM notifications UNION ALL SELECT state,attempts FROM call_notifications UNION ALL SELECT state,attempts FROM status_alert_jobs) WHERE state='pending'`).Scan(&pending, &failed); e != nil {
		return nil, e
	}
	return map[string]any{"last_seen": ts, "status_received_at": statusAt, "reported": reported, "notifications_pending": pending, "notifications_retrying": failed}, nil
}
func (s *Store) NextNotification(ctx context.Context) (Notification, error) {
	var n Notification
	e := s.DB.QueryRowContext(ctx, `SELECT n.id,n.message_id,n.attempts,m.slot,m.body,m.card_name,coalesce(c.group_name,'来源未确认') FROM notifications n JOIN messages m ON m.id=n.message_id LEFT JOIN cards c ON c.id=m.card_id WHERE n.state='pending' AND coalesce(c.notification_enabled,1)=1 AND m.deleted_at=0 AND n.next_at<=? ORDER BY n.id LIMIT 1`, time.Now().Unix()).Scan(&n.ID, &n.MessageID, &n.Attempts, &n.Slot, &n.Body, &n.CardName, &n.CardGroup)
	if errors.Is(e, sql.ErrNoRows) {
		n, e = s.nextCallNotification(ctx)
		if errors.Is(e, sql.ErrNoRows) {
			return s.nextStatusAlert(ctx)
		}
		return n, e
	}
	return n, e
}
func (s *Store) NotificationResult(ctx context.Context, n Notification, success bool) error {
	return s.NotificationResultCode(ctx, n, success, "push_request_failed")
}
func (s *Store) NotificationResultCode(ctx context.Context, n Notification, success bool, code string) error {
	return s.NotificationResultCodeAt(ctx, n, success, code, time.Now().Unix())
}

// Ignore late failures and preserve the first acceptance on duplicate callbacks.
func (s *Store) NotificationResultCodeAt(ctx context.Context, n Notification, success bool, code string, now int64) error {
	if n.AlertID > 0 {
		return s.statusAlertResult(ctx, n, success, code, now)
	}
	if n.CallID > 0 {
		return s.callNotificationResult(ctx, n, success, code, now)
	}
	if success {
		_, e := s.DB.ExecContext(ctx, `UPDATE notifications SET state='sent',last_error='',accepted_at=CASE WHEN state='sent' THEN accepted_at ELSE ? END WHERE id=? AND state IN ('pending','sent')`, now, n.ID)
		return e
	}
	delay := min(3600, 5*(1<<min(n.Attempts, 9)))
	_, e := s.DB.ExecContext(ctx, `UPDATE notifications SET attempts=attempts+1,next_at=?,last_error=? WHERE id=? AND state='pending'`, now+int64(delay), notificationCode(code), n.ID)
	return e
}
