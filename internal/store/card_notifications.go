package store

import (
	"context"
)

// Called under the worker's gate: a completed disable cannot race a new push.
// Canceled jobs remain dedup evidence and are never revived by enabling.
func (s *Store) SetCardNotifications(ctx context.Context, id string, enabled, expected bool) error {
	if id == "" || id == "unknown" || !ValidCardFilter(id) {
		return &SafetyError{Code: "invalid_filter"}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current bool
	if err = tx.QueryRowContext(ctx, `SELECT notification_enabled FROM cards WHERE id=?`, id).Scan(&current); err != nil {
		return err
	}
	if current != expected && current != enabled {
		return &SafetyError{Code: "notification_preference_conflict"}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cards SET notification_enabled=? WHERE id=?`, enabled, id); err != nil {
		return err
	}
	if !enabled {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO notifications(message_id,state,last_error) SELECT id,'canceled','card_notifications_disabled' FROM messages WHERE card_id=? AND complete=0 AND deleted_at=0; INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,state,last_error,kind) SELECT c.id,c.card_name,cards.group_name,'canceled','card_notifications_disabled',CASE WHEN c.voicemail_due_at>0 THEN 'voicemail_missing' ELSE 'timeout' END FROM calls c JOIN cards ON cards.id=c.card_id WHERE c.card_id=? AND ((c.ended=0 AND c.timeout_at>0) OR c.voicemail_due_at>0);`, id, id); err != nil {
			return err
		}

		if _, err = tx.ExecContext(ctx, `UPDATE calls SET voicemail_due_at=-1 WHERE card_id=? AND voicemail_due_at>0`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE notifications SET state='canceled',last_error='card_notifications_disabled' WHERE state='pending' AND message_id IN (SELECT id FROM messages WHERE card_id=?); UPDATE call_notifications SET state='canceled',last_error='card_notifications_disabled' WHERE state='pending' AND call_id IN (SELECT id FROM calls WHERE card_id=?);`, id, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
