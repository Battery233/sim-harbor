package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strconv"
)

type NotificationRecord struct {
	ID             int64  `json:"id"`
	Key            string `json:"key"`
	Source         string `json:"source"`
	Kind           string `json:"kind"`
	MessageID      int64  `json:"message_id,omitempty"`
	CallID         int64  `json:"call_id,omitempty"`
	Slot           int    `json:"slot"`
	State          string `json:"state"`
	Failures       int    `json:"failures"`
	NextAt         int64  `json:"next_at"`
	LastError      string `json:"last_error"`
	ManualRetryAt  int64  `json:"manual_retry_at"`
	MessageDeleted bool   `json:"message_deleted"`
	RecordedAt     int64  `json:"recorded_at"`
	AcceptedAt     int64  `json:"accepted_at"`
	SystemKey      string `json:"system_key,omitempty"`
	UsedBytes      int64  `json:"used_bytes,omitempty"`
	LimitBytes     int64  `json:"limit_bytes,omitempty"`
	stream         string
}

type notificationRecordCursor struct {
	At     int64  `json:"at"`
	Source string `json:"source"`
	ID     int64  `json:"id"`
}

func NotificationRecordCursor(row NotificationRecord) string {
	stream := row.stream
	if stream == "" {
		stream = row.Source
	}
	data, _ := json.Marshal(notificationRecordCursor{row.RecordedAt, stream, row.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}
func checkedRecordCursor(value string) (notificationRecordCursor, error) {
	var cursor notificationRecordCursor
	if value == "" {
		return cursor, nil
	}
	if len(value) > 192 {
		return cursor, safety("invalid_filter")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.At < 0 || cursor.ID <= 0 || (cursor.Source != "sms" && cursor.Source != "call" && cursor.Source != "status" && cursor.Source != "capacity") {
		return cursor, safety("invalid_filter")
	}
	return cursor, nil
}

// Type plus ID disambiguates independently numbered SMS and call tasks. Sort
// by a stable record time and a complete tie-breaker, never mutable push state.
const notificationRecordsCTE = `WITH records AS (
 SELECT n.id,'sms' source,'sms' kind,n.message_id,0 call_id,m.slot,n.state,n.attempts,n.next_at,n.last_error,n.manual_retry_at,m.deleted_at>0 message_deleted,max(0,m.sms_ts) recorded_at,n.accepted_at,'' system_key,0 used_bytes,0 limit_bytes
 FROM notifications n JOIN messages m ON m.id=n.message_id
 UNION ALL
 SELECT n.id,'call',n.kind,0,n.call_id,c.slot,n.state,n.attempts,n.next_at,n.last_error,0,0,max(0,CASE WHEN n.created_at>0 THEN n.created_at ELSE c.start_ts END),n.accepted_at,'',0,0
 FROM call_notifications n JOIN calls c ON c.id=n.call_id
 UNION ALL
 SELECT n.id,'status',CASE WHEN n.kind='fault' THEN 'status_fault' ELSE 'status_recovery' END,0,0,CASE n.key WHEN 'sim1' THEN 1 WHEN 'sim2' THEN 2 ELSE 0 END,n.state,n.attempts,n.next_at,n.last_error,0,0,max(0,n.created_at),n.accepted_at,n.key,0,0
 FROM status_alert_jobs n
 UNION ALL
 SELECT id,'capacity',kind,0,0,0,state,attempts,next_at,last_error,0,0,created_at,accepted_at,'voicemail',used_bytes,limit_bytes FROM system_notifications
) `

// "all" retains the existing SMS+call filter; "everything" includes system tasks.
const notificationSourceFilter = ` (?='everything' OR (?='all' AND source IN ('sms','call')) OR (?='system' AND source IN ('status','capacity')) OR source=?) `

func (s *Store) NotificationRecords(ctx context.Context, state, source, before string) ([]NotificationRecord, NotificationSummary, error) {
	var summary NotificationSummary
	if (source != "all" && source != "sms" && source != "call" && source != "system" && source != "everything") || (state != "all" && state != "pending" && state != "sent" && state != "canceled" && state != "restore_held") {
		return nil, summary, safety("invalid_filter")
	}
	cursor, err := checkedRecordCursor(before)
	if err != nil {
		return nil, summary, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, summary, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, notificationRecordsCTE+`SELECT coalesce(sum(state='pending'),0),coalesce(sum(state='pending' AND attempts>0),0),coalesce(sum(state='sent'),0),coalesce(sum(state='restore_held'),0) FROM records WHERE `+notificationSourceFilter, source, source, source, source).Scan(&summary.Pending, &summary.Retrying, &summary.Accepted, &summary.Held); err != nil {
		return nil, summary, err
	}
	err = tx.QueryRowContext(ctx, `SELECT requested_at,outcome FROM notification_test WHERE id=1`).Scan(&summary.TestAt, &summary.TestOutcome)
	if err != nil && err != sql.ErrNoRows {
		return nil, summary, err
	}
	rows, err := tx.QueryContext(ctx, notificationRecordsCTE+`SELECT id,source,kind,message_id,call_id,slot,state,attempts,next_at,last_error,manual_retry_at,message_deleted,recorded_at,accepted_at,system_key,used_bytes,limit_bytes FROM records WHERE `+notificationSourceFilter+` AND (?='all' OR state=?) AND (?='' OR (recorded_at,source,id)<(?,?,?)) ORDER BY recorded_at DESC,source DESC,id DESC LIMIT 51`, source, source, source, source, state, state, before, cursor.At, cursor.Source, cursor.ID)
	if err != nil {
		return nil, summary, err
	}
	defer rows.Close()
	out := []NotificationRecord{}
	for rows.Next() {
		var n NotificationRecord
		if err = rows.Scan(&n.ID, &n.stream, &n.Kind, &n.MessageID, &n.CallID, &n.Slot, &n.State, &n.Failures, &n.NextAt, &n.LastError, &n.ManualRetryAt, &n.MessageDeleted, &n.RecordedAt, &n.AcceptedAt, &n.SystemKey, &n.UsedBytes, &n.LimitBytes); err != nil {
			return nil, summary, err
		}
		n.Key = n.stream + ":" + strconv.FormatInt(n.ID, 10)
		n.Source = n.stream
		if n.stream == "status" || n.stream == "capacity" {
			n.Source = "system"
		}
		out = append(out, n)
	}
	return out, summary, rows.Err()
}
