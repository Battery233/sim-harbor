package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func migrateVoicemail(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS voicemails(
  id INTEGER PRIMARY KEY,
  media_id TEXT NOT NULL UNIQUE,
  call_id INTEGER UNIQUE REFERENCES calls(id),
  slot INTEGER NOT NULL DEFAULT 0,
  phone TEXT NOT NULL DEFAULT '',
  start_ts INTEGER NOT NULL DEFAULT 0,
  sha256 TEXT NOT NULL,
  size INTEGER NOT NULL,
  stored_name TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS voicemails_identity ON voicemails(sha256, slot, start_ts, size);
CREATE TABLE call_notifications_v17(
  id INTEGER PRIMARY KEY,
  call_id INTEGER UNIQUE NOT NULL REFERENCES calls(id),
  card_name TEXT NOT NULL,
  group_name TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  next_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  accepted_at INTEGER NOT NULL DEFAULT 0 CHECK(accepted_at>=0),
  kind TEXT NOT NULL DEFAULT 'missed' CHECK(kind IN ('missed','timeout','voicemail')),
  attempted_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO call_notifications_v17(id,call_id,card_name,group_name,state,attempts,next_at,last_error,accepted_at,kind,attempted_at)
SELECT id,call_id,card_name,group_name,state,attempts,next_at,last_error,accepted_at,kind,attempted_at FROM call_notifications;
DROP TABLE call_notifications;
ALTER TABLE call_notifications_v17 RENAME TO call_notifications;
CREATE INDEX call_notifications_success_time ON call_notifications(state,accepted_at);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(18);`)
	return err
}

type VoicemailFile struct {
	Name string
	Size int64
}

func (s *Store) VoicemailFile(ctx context.Context, callID int64) (VoicemailFile, error) {
	var file VoicemailFile
	err := s.DB.QueryRowContext(ctx, `SELECT stored_name, size FROM voicemails WHERE call_id=?`, callID).Scan(&file.Name, &file.Size)
	return file, err
}

// SaveVoicemail stores one AMR recording. The same bytes, slot, start and size
// return the original media id and do not queue another notification.
func (s *Store) SaveVoicemail(ctx context.Context, dataDir string, audio []byte, slot int, phone string, start, now int64) (mediaID string, createdAt int64, duplicate bool, err error) {
	sum := sha256.Sum256(audio)
	digest := hex.EncodeToString(sum[:])
	err = s.DB.QueryRowContext(ctx, `SELECT media_id, created_at FROM voicemails WHERE sha256=? AND slot=? AND start_ts=? AND size=?`, digest, slot, start, len(audio)).Scan(&mediaID, &createdAt)
	if err == nil {
		if err = s.linkStoredVoicemail(ctx, mediaID, phone, now); err != nil {
			return "", 0, false, err
		}
		return mediaID, createdAt, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, err
	}
	buf := make([]byte, 16)
	if _, err = rand.Read(buf); err != nil {
		return "", 0, false, err
	}
	mediaID = hex.EncodeToString(buf)
	name := mediaID + ".amr"
	dir := filepath.Join(dataDir, "voicemail")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", 0, false, err
	}
	final := filepath.Join(dir, name)
	partial := final + ".partial"
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", 0, false, err
	}
	_, writeErr := f.Write(audio)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(partial)
		if writeErr != nil {
			return "", 0, false, writeErr
		}
		return "", 0, false, closeErr
	}
	if err = os.Rename(partial, final); err != nil {
		os.Remove(partial)
		return "", 0, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		os.Remove(final)
		return "", 0, false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO voicemails(media_id,slot,phone,start_ts,sha256,size,stored_name,created_at) VALUES(?,?,?,?,?,?,?,?)`, mediaID, slot, phone, start, digest, len(audio), name, now)
	if err != nil {
		os.Remove(final)
		var again string
		var againAt int64
		if scanErr := s.DB.QueryRowContext(ctx, `SELECT media_id, created_at FROM voicemails WHERE sha256=? AND slot=? AND start_ts=? AND size=?`, digest, slot, start, len(audio)).Scan(&again, &againAt); scanErr == nil {
			if linkErr := s.linkStoredVoicemail(ctx, again, phone, now); linkErr != nil {
				return "", 0, false, linkErr
			}
			return again, againAt, true, nil
		}
		return "", 0, false, err
	}
	rowID, err := res.LastInsertId()
	if err != nil {
		os.Remove(final)
		return "", 0, false, err
	}
	if err = linkVoicemail(ctx, tx, rowID, slot, phone, start, now); err != nil {
		os.Remove(final)
		return "", 0, false, err
	}
	if err = tx.Commit(); err != nil {
		os.Remove(final)
		return "", 0, false, err
	}
	return mediaID, now, false, nil
}

func (s *Store) linkStoredVoicemail(ctx context.Context, mediaID, phone string, now int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rowID, slot, start int64
	var storedPhone string
	err = tx.QueryRowContext(ctx, `SELECT id, slot, phone, start_ts FROM voicemails WHERE media_id=?`, mediaID).Scan(&rowID, &slot, &storedPhone, &start)
	if err != nil {
		return err
	}
	if strings.TrimSpace(storedPhone) == "" && strings.TrimSpace(phone) != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE voicemails SET phone=? WHERE id=? AND phone=''`, strings.TrimSpace(phone), rowID); err != nil {
			return err
		}
		storedPhone = strings.TrimSpace(phone)
	}
	if err = linkVoicemail(ctx, tx, rowID, int(slot), storedPhone, start, now); err != nil {
		return err
	}
	return tx.Commit()
}

func relinkVoicemails(ctx context.Context, tx *sql.Tx, slot int, now int64) error {
	if slot < 1 || slot > 2 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, phone, start_ts FROM voicemails WHERE call_id IS NULL AND slot=?`, slot)
	if err != nil {
		return err
	}
	type pending struct {
		id    int64
		phone string
		start int64
	}
	list := []pending{}
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.id, &item.phone, &item.start); err != nil {
			rows.Close()
			return err
		}
		list = append(list, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range list {
		if err = linkVoicemail(ctx, tx, item.id, slot, item.phone, item.start, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) reconcileVoicemails(now int64) error {
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, slot, phone, start_ts FROM voicemails WHERE call_id IS NULL`)
	if err != nil {
		return err
	}
	type pending struct {
		id, start int64
		slot      int
		phone     string
	}
	list := []pending{}
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.id, &item.slot, &item.phone, &item.start); err != nil {
			rows.Close()
			return err
		}
		list = append(list, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range list {
		if err = linkVoicemail(ctx, tx, item.id, item.slot, item.phone, item.start, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func linkVoicemail(ctx context.Context, tx *sql.Tx, rowID int64, slot int, phone string, start, now int64) error {
	callID, ok, err := chooseVoicemailCall(ctx, tx, slot, phone, start)
	if err != nil || !ok {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE voicemails SET call_id=? WHERE id=? AND call_id IS NULL AND NOT EXISTS(SELECT 1 FROM voicemails o WHERE o.call_id=? AND o.id<>?)`, callID, rowID, callID, rowID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "constraint") {
			return nil
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return err
	}
	blocked, err := restoreBlocked(ctx, tx)
	if err != nil {
		return err
	}
	held := 0
	if blocked {
		held = 1
	}
	res, err = tx.ExecContext(ctx, `UPDATE call_notifications SET kind='voicemail', state=CASE WHEN ?=1 THEN 'restore_held' ELSE state END, last_error=CASE WHEN ?=1 THEN 'restored_notification_held' ELSE last_error END WHERE call_id=? AND state='pending' AND kind IN ('missed','timeout')`, held, held, callID)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,state,last_error,kind) SELECT c.id,c.card_name,coalesce(cards.group_name,'来源未确认'),CASE WHEN coalesce(cards.notification_enabled,1)=0 THEN 'canceled' WHEN ?=1 THEN 'restore_held' ELSE 'pending' END,CASE WHEN ?=1 AND coalesce(cards.notification_enabled,1)=1 THEN 'restored_notification_held' ELSE '' END,'voicemail' FROM calls c LEFT JOIN cards ON cards.id=c.card_id WHERE c.id=?`, held, held, callID)
	return err
}

func chooseVoicemailCall(ctx context.Context, tx *sql.Tx, slot int, phone string, start int64) (int64, bool, error) {
	if start <= 0 || slot < 1 || slot > 2 {
		return 0, false, nil
	}
	exact, err := voicemailCallCandidates(ctx, tx, `SELECT c.id, c.phone, coalesce(c.card_id,'') FROM calls c WHERE c.slot=? AND c.start_ts=?`, slot, start)
	if err != nil {
		return 0, false, err
	}
	matched := make([]int64, 0, len(exact))
	for _, item := range exact {
		if PhonesMatch(phone, item.phone) {
			matched = append(matched, item.id)
		}
	}
	if len(matched) == 1 {
		return matched[0], true, nil
	}
	if len(matched) > 1 || !ConfirmableCaller(phone) {
		return 0, false, nil
	}
	window, err := voicemailCallCandidates(ctx, tx, `SELECT c.id, c.phone, coalesce(c.card_id,'') FROM calls c WHERE c.slot=? AND c.start_ts BETWEEN ? AND ?`, slot, start-900, start+900)
	if err != nil {
		return 0, false, err
	}
	// A non-empty card id does not prove the recording belongs to that call.
	// Every same-number candidate in the window stays in the ambiguous set,
	// including a call whose card is still unknown. Link only when one
	// candidate remains and that candidate has a card id.
	candidates := make([]voicemailCallCandidate, 0, len(window))
	for _, item := range window {
		if PhonesMatch(phone, item.phone) {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 1 && candidates[0].cardID != "" {
		return candidates[0].id, true, nil
	}
	return 0, false, nil
}

type voicemailCallCandidate struct {
	id     int64
	phone  string
	cardID string
}

func voicemailCallCandidates(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]voicemailCallCandidate, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []voicemailCallCandidate{}
	for rows.Next() {
		var item voicemailCallCandidate
		if err = rows.Scan(&item.id, &item.phone, &item.cardID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func phoneDigits(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PhonesMatch treats a country-code prefix as the same number. Empty values never match.
func PhonesMatch(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	da, db := phoneDigits(a), phoneDigits(b)
	if da == "" || db == "" {
		return strings.EqualFold(a, b)
	}
	if da == db {
		return true
	}
	short, long := da, db
	if len(short) > len(long) {
		short, long = long, short
	}
	if len(short) < 8 || len(long)-len(short) > 4 {
		return false
	}
	return strings.HasSuffix(long, short)
}

// ConfirmableCaller is a real number. Anonymous and empty callers stay on exact matches only.
func ConfirmableCaller(phone string) bool {
	phone = strings.TrimSpace(phone)
	if phone == "" || strings.EqualFold(phone, "anonymous") {
		return false
	}
	return len(phoneDigits(phone)) >= 8
}
