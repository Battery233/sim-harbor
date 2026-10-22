package store

import "context"

// Server-persisted playback state is idempotent and survives ordinary restarts
// and backup/restore. Calling this API does not fetch or notify any recording.
func (s *Store) MarkVoicemailPlayed(ctx context.Context, media string, now int64) (int64, error) {
	if !mediaIdentity.MatchString(media) || now <= 0 {
		return 0, safety("invalid_id")
	}
	s.voicemailMu.Lock()
	defer s.voicemailMu.Unlock()
	_, err := s.DB.ExecContext(ctx, `UPDATE voicemails SET played_at=CASE WHEN played_at=0 THEN ? ELSE played_at END WHERE media_id=? AND stored_name<>''`, now, media)
	if err != nil {
		return 0, err
	}
	var at int64
	err = s.DB.QueryRowContext(ctx, `SELECT played_at FROM voicemails WHERE media_id=? AND stored_name<>''`, media).Scan(&at)
	return at, err
}
