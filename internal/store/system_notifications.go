package store

import (
	"context"
	"database/sql"
	"errors"
)

// Capacity delivery keeps its existing private checkpoint. This table adds
// queryable task history; it never stores notification bodies or credentials.
const systemNotificationSchema = `CREATE TABLE system_notifications(
 id INTEGER PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind='voicemail_capacity'),
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sent','canceled','restore_held')),
 attempts INTEGER NOT NULL DEFAULT 0,
 next_at INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 accepted_at INTEGER NOT NULL DEFAULT 0,
 used_bytes INTEGER NOT NULL,
 limit_bytes INTEGER NOT NULL,
 target_bytes INTEGER NOT NULL);
CREATE UNIQUE INDEX system_notification_pending ON system_notifications(kind) WHERE state='pending';`

func migrateSystemNotifications(tx *sql.Tx) error {
	var found int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('status_alert_jobs') WHERE name='created_at'`).Scan(&found); err != nil {
		return err
	}
	if found == 0 {
		if _, err := tx.Exec(`ALTER TABLE status_alert_jobs ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// Old accepted results have an acceptance time but no creation time. Pin the
	// available time once during migration; old pending tasks keep unknown time.
	if _, err := tx.Exec(`UPDATE status_alert_jobs SET created_at=accepted_at WHERE created_at=0 AND accepted_at>0`); err != nil {
		return err
	}
	// Column/table guards also support diagnostic fixtures that retain newer objects.
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='system_notifications'`).Scan(&found); err != nil {
		return err
	}
	if found == 0 {
		_, err := tx.Exec(systemNotificationSchema)
		return err
	}
	return nil
}

// Return an existing accepted result after a checkpoint-write failure. Otherwise
// coalesce the one pending capacity task, without changing its stable record time.
func (s *Store) PrepareCapacityNotification(ctx context.Context, previous, level, limit, used, next, now int64) (id int64, accepted bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	if previous > 0 {
		var state string
		var target, oldLimit int64
		err = tx.QueryRowContext(ctx, `SELECT id,state,target_bytes,limit_bytes FROM system_notifications WHERE id=? AND kind='voicemail_capacity'`, previous).Scan(&id, &state, &target, &oldLimit)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, false, err
		}
		if err == nil && state == "sent" && oldLimit == limit && target >= level {
			return id, true, nil
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM system_notifications WHERE kind='voicemail_capacity' AND state='pending'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		result, e := tx.ExecContext(ctx, `INSERT INTO system_notifications(kind,created_at,used_bytes,limit_bytes,target_bytes,next_at) VALUES('voicemail_capacity',?,?,?,?,?)`, now, used, limit, level, next)
		if e != nil {
			return 0, false, e
		}
		id, err = result.LastInsertId()
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE system_notifications SET used_bytes=?,limit_bytes=?,target_bytes=?,next_at=? WHERE id=? AND (used_bytes<>? OR limit_bytes<>? OR target_bytes<>? OR next_at<>?)`, used, limit, level, next, id, used, limit, level, next)
	}
	if err != nil {
		return 0, false, err
	}
	return id, false, tx.Commit()
}
func (s *Store) ReserveCapacityNotification(ctx context.Context, id, next int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE system_notifications SET next_at=?,last_error='unknown' WHERE id=? AND state='pending'`, next, id)
	return err
}
func (s *Store) CapacityNotificationResult(ctx context.Context, id int64, success bool, code string, now int64) error {
	if success {
		_, err := s.DB.ExecContext(ctx, `UPDATE system_notifications SET state='sent',accepted_at=?,last_error='',next_at=0 WHERE id=? AND state='pending'`, now, id)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE system_notifications SET attempts=attempts+1,last_error=? WHERE id=? AND state='pending'`, notificationCode(code), id)
	return err
}
func (s *Store) CancelCapacityNotifications(ctx context.Context, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE system_notifications SET state='canceled',last_error=?,next_at=0 WHERE kind='voicemail_capacity' AND state='pending'`, reason)
	return err
}
