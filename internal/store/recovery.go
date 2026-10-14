package store

import (
	"context"
	"database/sql"
)

const SchemaVersion = 14
const recoverySchema = `CREATE TABLE IF NOT EXISTS recovery_guard(id INTEGER PRIMARY KEY CHECK(id=1),restored_at INTEGER NOT NULL,archive_sha256 TEXT NOT NULL);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(5);`

func restoreBlocked(ctx context.Context, tx *sql.Tx) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM recovery_guard`).Scan(&n)
	return n != 0, err
}
func (s *Store) RestoreBlocked(ctx context.Context) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM recovery_guard`).Scan(&n)
	return n != 0, err
}

// Called only on a private validated copy. No API unlock exists: a restored
// database can lack post-backup claims/limits, so simply toggling config is unsafe.
func (s *Store) HoldRestoredData(ctx context.Context, archive string, now int64) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `INSERT INTO recovery_guard VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET restored_at=excluded.restored_at,archive_sha256=excluded.archive_sha256`, now, archive); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE outgoing SET state='canceled',error_code='restored_preview_canceled',expires_at=0,updated_at=? WHERE rank=0 AND state='prepared'; UPDATE outgoing SET state='unknown' WHERE rank BETWEEN 1 AND 3 AND state IN ('waiting_device','accepted','processed'); UPDATE notifications SET state='restore_held',last_error='restored_notification_held' WHERE state='pending';`, now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE call_notifications SET state='restore_held',last_error='restored_notification_held' WHERE state='pending'`); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE status_alert_jobs SET state='restore_held' WHERE state='pending'; UPDATE status_alert_monitors SET bad_since=0,good_since=0;`); e != nil {
		return e
	}
	// Hold even starts whose deadlines have not expired yet. A later final
	// report must not turn restored old starts into new automatic reminders.
	if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,state,last_error,kind) SELECT c.id,c.card_name,coalesce(cards.group_name,'来源未确认'),'restore_held','restored_notification_held','timeout' FROM calls c LEFT JOIN cards ON cards.id=c.card_id WHERE c.timeout_at>0 AND c.ended=0`); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE calls SET timeout_at=0`); e != nil {
		return e
	}
	return tx.Commit()
}
