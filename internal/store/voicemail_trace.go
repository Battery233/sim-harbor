package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

type CallTraceNotification struct {
	Kind       string `json:"kind"`
	State      string `json:"state"`
	Error      string `json:"error"`
	RecordedAt int64  `json:"recorded_at"`
	AcceptedAt int64  `json:"accepted_at"`
}

type CallTrace struct {
	CallID        int64                   `json:"call_id"`
	Slot          int                     `json:"slot"`
	StartedAt     int64                   `json:"started_at"`
	ConnectedAt   int64                   `json:"connected_at"`
	EndedAt       int64                   `json:"ended_at"`
	State         string                  `json:"state"`
	RecordingAt   int64                   `json:"recording_at"`
	Recording     string                  `json:"recording"`
	Notifications []CallTraceNotification `json:"notifications"`
	Key           string                  `json:"-"`
	MediaID       string                  `json:"-"`
}

func (s *Store) DiagnosticCallKey(ctx context.Context, device string, id int64) string {
	var key string
	if err := s.DB.QueryRowContext(ctx, `SELECT key FROM calls WHERE device_id=? AND id=?`, device, id).Scan(&key); err != nil {
		return ""
	}
	return key
}

func (s *Store) CallTrace(ctx context.Context, device string, id int64) (CallTrace, error) {
	v := CallTrace{Notifications: []CallTraceNotification{}, Recording: "unlinked"}
	err := s.DB.QueryRowContext(ctx, `SELECT c.id,c.key,c.slot,c.start_ts,c.connected_ts,c.end_ts,c.state,coalesce(v.media_id,''),coalesce(v.created_at,0),CASE WHEN v.id IS NULL THEN 'unlinked' WHEN v.stored_name='' THEN 'deleted' ELSE 'stored' END FROM calls c LEFT JOIN voicemails v ON v.call_id=c.id WHERE c.device_id=? AND c.id=?`, device, id).Scan(&v.CallID, &v.Key, &v.Slot, &v.StartedAt, &v.ConnectedAt, &v.EndedAt, &v.State, &v.MediaID, &v.RecordingAt, &v.Recording)
	if err != nil {
		return v, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT kind,state,last_error,created_at,accepted_at FROM call_notifications WHERE call_id=? ORDER BY phase LIMIT 2`, id)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var n CallTraceNotification
		if err = rows.Scan(&n.Kind, &n.State, &n.Error, &n.RecordedAt, &n.AcceptedAt); err != nil {
			return v, err
		}
		v.Notifications = append(v.Notifications, n)
	}
	return v, rows.Err()
}

// Audio reports may omit identity fields. Require an explicit slot, call start
// and caller, and a unique stored candidate; never infer from receipt time.
func (s *Store) AudioReportCallKey(ctx context.Context, e protocol.Event) string {
	if e.TelStartTS <= 0 || e.Slot < 1 || e.Slot > 2 || strings.TrimSpace(e.Phone) == "" {
		return ""
	}
	var key sql.NullString
	var count int
	iccid := cardIdentity(e.ICCID)
	err := s.DB.QueryRowContext(ctx, `SELECT min(key),count(*) FROM calls WHERE device_id=? AND slot=? AND start_ts=? AND phone=? AND (?='' OR card_id=?)`, e.DeviceID, int(e.Slot), int64(e.TelStartTS), strings.TrimSpace(e.Phone), iccid, iccid).Scan(&key, &count)
	if err != nil || count != 1 {
		return ""
	}
	return key.String
}

func (s *Store) RecordingTraceKeys(ctx context.Context, media string) (string, string) {
	var key, state string
	err := s.DB.QueryRowContext(ctx, `SELECT coalesce(c.key,''),CASE WHEN v.stored_name='' THEN 'deleted' ELSE 'stored' END FROM voicemails v LEFT JOIN calls c ON c.id=v.call_id WHERE v.media_id=?`, media).Scan(&key, &state)
	if err != nil {
		return "", "unknown"
	}
	return key, state
}
