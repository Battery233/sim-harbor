package store

import "context"

// UnlinkedVoicemail contains only upload metadata, never a guessed card identity.
type UnlinkedVoicemail struct {
	ID         int64  `json:"id"`
	Slot       int    `json:"slot"`
	Phone      string `json:"phone"`
	StartTS    int64  `json:"started_at"`
	CreatedAt  int64  `json:"created_at"`
	Size       int64  `json:"size"`
	StoredName string `json:"-"`
}

func (s *Store) UnlinkedVoicemails(ctx context.Context, before int64) ([]UnlinkedVoicemail, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,slot,phone,start_ts,created_at,size,stored_name FROM voicemails WHERE stored_name<>'' AND call_id IS NULL AND (?=0 OR id<?) ORDER BY id DESC LIMIT 51`, before, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []UnlinkedVoicemail{}
	for rows.Next() {
		var item UnlinkedVoicemail
		if err = rows.Scan(&item.ID, &item.Slot, &item.Phone, &item.StartTS, &item.CreatedAt, &item.Size, &item.StoredName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UnlinkedVoicemailFile(ctx context.Context, id int64) (VoicemailFile, error) {
	var file VoicemailFile
	err := s.DB.QueryRowContext(ctx, `SELECT stored_name,size FROM voicemails WHERE stored_name<>'' AND id=? AND call_id IS NULL`, id).Scan(&file.Name, &file.Size)
	return file, err
}
