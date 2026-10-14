package store

import (
	"context"
	"database/sql"
	"errors"
)

// DiagnosticOperation exposes no SMS, number or card identity. It is not used
// for business matching, authorisation, claims or command dispatch.
func (s *Store) DiagnosticOperation(ctx context.Context, tid, eventHash string) (string, int, error) {
	var id string
	var slot int
	err := sql.ErrNoRows
	if tid != "" {
		err = s.DB.QueryRowContext(ctx, `SELECT id,slot FROM outgoing WHERE tid=?`, tid).Scan(&id, &slot)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = s.DB.QueryRowContext(ctx, `SELECT o.id,o.slot FROM delivery_receipts r JOIN outgoing o ON o.id=r.matched_operation WHERE r.event_hash=?`, eventHash).Scan(&id, &slot)
	}
	return id, slot, err
}
