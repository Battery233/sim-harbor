package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
)

var mediaIdentity = regexp.MustCompile(`^[a-f0-9]{32}$`)
var ErrVoicemailCleanup = errors.New("recording deletion registered; file cleanup pending")

// An empty stored_name is a deletion tombstone. Keep the original digest,
// identity and call link so retransmission cannot recreate audio or a push.
// Commit that decision before unlinking. Interrupted cleanup is safe to repeat.
func (s *Store) DeleteVoicemail(ctx context.Context, dataDir, mediaID string) error {
	if !mediaIdentity.MatchString(mediaID) {
		return safety("invalid_id")
	}
	s.voicemailMu.Lock()
	defer s.voicemailMu.Unlock()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	var callID any
	var callNumber *int64
	if err = tx.QueryRowContext(ctx, `SELECT stored_name,call_id FROM voicemails WHERE media_id=?`, mediaID).Scan(&name, &callNumber); err != nil {
		return err
	}
	if name != "" && name != mediaID+".amr" {
		return safety("voicemail_file_invalid")
	}
	if callNumber != nil {
		callID = *callNumber
	}
	if _, err = tx.ExecContext(ctx, `UPDATE voicemails SET stored_name='' WHERE media_id=?`, mediaID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE call_notifications SET state='canceled',last_error='voicemail_deleted' WHERE call_id=? AND kind IN ('voicemail','voicemail_late') AND state IN ('pending','restore_held')`, callID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = removeDeletedVoicemail(dataDir, mediaID); err != nil {
		return ErrVoicemailCleanup
	}
	return nil
}

// Only files whose indexed tombstone proves a prior explicit deletion are
// cleaned on startup. Never glob or delete unrelated recordings/partial files.
func (s *Store) CleanupDeletedVoicemails(ctx context.Context, dataDir string) error {
	s.voicemailMu.Lock()
	defer s.voicemailMu.Unlock()
	rows, err := s.DB.QueryContext(ctx, `SELECT media_id FROM voicemails WHERE stored_name=''`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = removeDeletedVoicemail(dataDir, id); err != nil {
			return err
		}
	}
	return nil
}
func removeDeletedVoicemail(dataDir, mediaID string) error {
	if !mediaIdentity.MatchString(mediaID) {
		return os.ErrInvalid
	}
	dir := filepath.Join(dataDir, "voicemail")
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return os.ErrInvalid
	}
	path := filepath.Join(dir, mediaID+".amr")
	info, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return os.ErrInvalid
	}
	return os.Remove(path)
}
