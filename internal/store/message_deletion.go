package store

import (
	"context"
)

const messageDeletionSchema = `ALTER TABLE messages ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0 CHECK(deleted_at>=0);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(9);`

// Keep the business key and row ID so replay cannot resurrect a deleted SMS.
// Successful notification evidence remains independent of inbox contents.
func (s *Store) DeleteMessage(ctx context.Context, id, now int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var deleted int64
	if err = tx.QueryRowContext(ctx, `SELECT deleted_at FROM messages WHERE id=?`, id).Scan(&deleted); err != nil {
		return err
	}
	if deleted != 0 {
		return tx.Commit()
	}
	if now < 1 {
		now = 1
	}
	if _, err = tx.ExecContext(ctx, `UPDATE messages SET deleted_at=?,phone='',body='',fragments='{}',card_name='',sms_total=0,sms_group_ref='' WHERE id=?`, now, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE notifications SET state='canceled',last_error='message_deleted' WHERE message_id=? AND state IN ('pending','restore_held')`, id); err != nil {
		return err
	}
	return tx.Commit()
}
