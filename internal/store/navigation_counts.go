package store

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Battery233/sim-harbor/internal/recording"
)

type NavigationCounts struct {
	UnreadMessages     int64 `json:"unread_messages"`
	UnplayedRecordings int64 `json:"unplayed_recordings"`
}

// Counts include only unread live messages and physically available, linked audio.
func (s *Store) NavigationCounts(ctx context.Context, dataDir string) (NavigationCounts, error) {
	var counts NavigationCounts
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE deleted_at=0 AND is_read=0`).Scan(&counts.UnreadMessages); err != nil {
		return counts, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT media_id,stored_name,size FROM voicemails WHERE call_id IS NOT NULL AND stored_name<>'' AND played_at=0`)
	if err != nil {
		return counts, err
	}
	var files []VoicemailFile
	for rows.Next() {
		var id string
		var f VoicemailFile
		if err = rows.Scan(&id, &f.Name, &f.Size); err != nil {
			break
		}
		if !recording.ValidMediaID(id) || f.Name != id+".amr" {
			continue
		}
		files = append(files, f)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return counts, err
	}
	if len(files) == 0 {
		return counts, nil
	}
	dir := filepath.Join(dataDir, "voicemail")
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return counts, nil
	}
	if err != nil {
		return counts, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return counts, os.ErrInvalid
	}
	for _, f := range files {
		if err = ctx.Err(); err != nil {
			return counts, err
		}
		st, e := os.Lstat(filepath.Join(dir, f.Name))
		if e == nil && st.Mode().IsRegular() && st.Size() == f.Size {
			counts.UnplayedRecordings++
		}
	}
	return counts, nil
}
