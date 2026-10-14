package store

import (
	"context"
	"database/sql"
)

type NotificationRow struct {
	MessageDeleted bool   `json:"message_deleted"`
	ID             int64  `json:"id"`
	MessageID      int64  `json:"message_id"`
	Slot           int    `json:"slot"`
	State          string `json:"state"`
	Failures       int    `json:"failures"`
	NextAt         int64  `json:"next_at"`
	LastError      string `json:"last_error"`
	ManualRetryAt  int64  `json:"manual_retry_at"`
}
type NotificationSummary struct {
	Held        int    `json:"held"`
	Pending     int    `json:"pending"`
	Retrying    int    `json:"retrying"`
	Accepted    int    `json:"accepted"`
	TestAt      int64  `json:"test_at"`
	TestOutcome string `json:"test_outcome"`
}

func notificationCode(code string) string {
	switch code {
	case "transport_failed", "http_rejected", "invalid_response", "bark_rejected", "accepted":
		return code
	default:
		return "push_request_failed"
	}
}
func (s *Store) NotificationPage(ctx context.Context, state string, before int64) ([]NotificationRow, NotificationSummary, error) {
	var summary NotificationSummary
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, summary, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(state='pending'),0),coalesce(sum(state='pending' AND attempts>0),0),coalesce(sum(state='sent'),0),coalesce(sum(state='restore_held'),0) FROM notifications`).Scan(&summary.Pending, &summary.Retrying, &summary.Accepted, &summary.Held); err != nil {
		return nil, summary, err
	}
	err = tx.QueryRowContext(ctx, `SELECT requested_at,outcome FROM notification_test WHERE id=1`).Scan(&summary.TestAt, &summary.TestOutcome)
	if err != nil && err != sql.ErrNoRows {
		return nil, summary, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT n.id,n.message_id,m.slot,n.state,n.attempts,n.next_at,n.last_error,n.manual_retry_at,m.deleted_at>0 FROM notifications n JOIN messages m ON m.id=n.message_id WHERE (?='all' OR n.state=?) AND (?=0 OR n.id<?) ORDER BY n.id DESC LIMIT 51`, state, state, before, before)
	if err != nil {
		return nil, summary, err
	}
	defer rows.Close()
	out := []NotificationRow{}
	for rows.Next() {
		var n NotificationRow
		if err = rows.Scan(&n.ID, &n.MessageID, &n.Slot, &n.State, &n.Failures, &n.NextAt, &n.LastError, &n.ManualRetryAt, &n.MessageDeleted); err != nil {
			return nil, summary, err
		}
		out = append(out, n)
	}
	return out, summary, rows.Err()
}

// Only failed jobs still waiting in backoff may be brought forward. This does
// not create a second job, retry a sent job, or touch the chargeable outbox.
func (s *Store) RetryNotification(ctx context.Context, id, now int64) error {
	r, err := s.DB.ExecContext(ctx, `UPDATE notifications SET next_at=0,manual_retry_at=? WHERE id=? AND state='pending' AND attempts>0 AND next_at>? AND manual_retry_at<=?`, now, id, now, now-60)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var found int
	err = s.DB.QueryRowContext(ctx, `SELECT 1 FROM notifications WHERE id=?`, id).Scan(&found)
	if err != nil {
		return err
	}
	return &SafetyError{Code: "notification_retry_unavailable"}
}
func (s *Store) ReserveNotificationTest(ctx context.Context, now int64) error {
	r, err := s.DB.ExecContext(ctx, `INSERT INTO notification_test VALUES(1,?,'unknown') ON CONFLICT(id) DO UPDATE SET requested_at=excluded.requested_at,outcome='unknown' WHERE requested_at<=?`, now, now-60)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return &SafetyError{Code: "notification_test_cooldown"}
	}
	return nil
}
func (s *Store) CompleteNotificationTest(ctx context.Context, at int64, code string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE notification_test SET outcome=? WHERE id=1 AND requested_at=?`, notificationCode(code), at)
	return err
}
