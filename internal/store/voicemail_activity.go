package store

import (
	"context"
	"database/sql"
)

type VoicemailActivity struct {
	RecordingAvailable bool  `json:"recording_available"`
	Slot               int   `json:"slot"`
	CallID             int64 `json:"call_id"`
	StartedAt          int64 `json:"started_at"`
	ConnectedAt        int64 `json:"connected_at"`
	Ended              bool  `json:"ended"`
	RecordingAt        int64 `json:"recording_at"`
	RecordingDeleted   bool  `json:"recording_deleted"`
}

// Observed reports and persisted audio, never inferred board configuration.
func (s *Store) VoicemailActivity(ctx context.Context, device string) ([]VoicemailActivity, error) {
	out := make([]VoicemailActivity, 0, 2)
	for _, slot := range []int{1, 2} {
		item := VoicemailActivity{Slot: slot}
		err := s.DB.QueryRowContext(ctx, `SELECT c.id,c.start_ts,c.connected_ts,c.ended,coalesce((SELECT max(v.created_at) FROM voicemails v WHERE v.call_id=c.id AND v.stored_name<>''),0),EXISTS(SELECT 1 FROM voicemails v WHERE v.call_id=c.id AND v.stored_name='') FROM calls c WHERE c.device_id=? AND c.slot=? AND c.connected_ts>0 ORDER BY c.id DESC LIMIT 1`, device, slot).Scan(&item.CallID, &item.StartedAt, &item.ConnectedAt, &item.Ended, &item.RecordingAt, &item.RecordingDeleted)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
