package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Battery233/sim-harbor/internal/outbound"
)

const outgoingSchema = `
CREATE TABLE outgoing(id TEXT PRIMARY KEY,device_id TEXT NOT NULL,tid TEXT UNIQUE NOT NULL,fingerprint TEXT NOT NULL,slot INTEGER NOT NULL,phone TEXT NOT NULL,body TEXT NOT NULL,segments INTEGER NOT NULL,iccid TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,dispatched_at INTEGER NOT NULL DEFAULT 0,deadline_at INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'prepared',rank INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '');
CREATE INDEX outgoing_content ON outgoing(fingerprint,created_at);
CREATE INDEX outgoing_limits ON outgoing(device_id,slot,dispatched_at);
CREATE TABLE outgoing_refs(operation_id TEXT NOT NULL REFERENCES outgoing(id),mr INTEGER NOT NULL,PRIMARY KEY(operation_id,mr));
CREATE TABLE delivery_receipts(id INTEGER PRIMARY KEY,event_hash TEXT UNIQUE NOT NULL,device_id TEXT NOT NULL,slot INTEGER NOT NULL,phone TEXT NOT NULL,iccid TEXT NOT NULL,mr INTEGER NOT NULL,status INTEGER NOT NULL,scts INTEGER NOT NULL,report_ts INTEGER NOT NULL,matched_operation TEXT REFERENCES outgoing(id),match_state TEXT NOT NULL DEFAULT 'unmatched');
CREATE INDEX receipt_pair ON delivery_receipts(device_id,slot,phone,mr,scts);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(3);`

type Outgoing struct {
	Sequence         int64  `json:"sequence"`
	ID               string `json:"id"`
	DeviceID         string `json:"-"`
	TID              string `json:"tid"`
	Fingerprint      string `json:"-"`
	Slot             int    `json:"slot"`
	Phone            string `json:"phone"`
	Body             string `json:"body"`
	Segments         int    `json:"segments_estimate"`
	ICCID            string `json:"-"`
	CardTail         string `json:"card_tail"`
	CardID           string `json:"card_id,omitempty"`
	CardCurrentName  string `json:"card_current_name,omitempty"`
	CardNumber       string `json:"card_number,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
	ExpiresAt        int64  `json:"expires_at"`
	DispatchedAt     int64  `json:"dispatched_at"`
	DeadlineAt       int64  `json:"deadline_at"`
	State            string `json:"state"`
	Rank             int    `json:"-"`
	ErrorCode        string `json:"error_code"`
	LastPhase        string `json:"last_phase"`
	Delivery         string `json:"delivery"`
	DeliveryExpected int    `json:"delivery_expected"`
	DeliveryReceived int    `json:"delivery_received"`
}

type SendLimits struct {
	Interval int64
	Daily    int
	DayStart int64
	DayEnd   int64
}
type SafetyError struct {
	Code       string
	ExistingID string
}

func (e *SafetyError) Error() string { return e.Code }
func safety(code string) error       { return &SafetyError{Code: code} }

const outgoingColumns = `rowid,id,device_id,tid,fingerprint,slot,phone,body,segments,iccid,created_at,updated_at,expires_at,dispatched_at,deadline_at,state,rank,error_code`

type scanner interface{ Scan(...any) error }

func scanOutgoing(row scanner) (Outgoing, error) {
	var o Outgoing
	err := row.Scan(&o.Sequence, &o.ID, &o.DeviceID, &o.TID, &o.Fingerprint, &o.Slot, &o.Phone, &o.Body, &o.Segments, &o.ICCID, &o.CreatedAt, &o.UpdatedAt, &o.ExpiresAt, &o.DispatchedAt, &o.DeadlineAt, &o.State, &o.Rank, &o.ErrorCode)
	return o, err
}

func fingerprint(device string, in outbound.Input) string {
	p, _ := json.Marshal([]any{device, in.Slot, in.Phone, in.Body})
	return hash(p)
}

// Prepare creates an immutable preview. A repeated ID must have identical
// content. The transaction includes duplicate-content checks across IDs.
func (s *Store) PrepareOutgoing(ctx context.Context, device, tid, iccid string, in outbound.Input, segments int, now int64) (Outgoing, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Outgoing{}, e
	}
	defer tx.Rollback()
	if blocked, e := restoreBlocked(ctx, tx); e != nil {
		return Outgoing{}, e
	} else if blocked {
		return Outgoing{}, safety("outbound_restore_blocked")
	}
	key := fingerprint(device, in)
	o, e := scanOutgoing(tx.QueryRowContext(ctx, `SELECT `+outgoingColumns+` FROM outgoing WHERE id=?`, in.ID))
	if e == nil {
		if o.Fingerprint != key {
			return o, safety("operation_conflict")
		}
		return o, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return o, e
	}
	var existing string
	e = tx.QueryRowContext(ctx, `SELECT id FROM outgoing WHERE device_id=? AND iccid=? AND phone=? AND body=? AND ((state='prepared' AND expires_at>?) OR (dispatched_at>0 AND (dispatched_at>? OR (rank<4 AND state!='failed')))) ORDER BY created_at DESC LIMIT 1`, device, iccid, in.Phone, in.Body, now, now-86400).Scan(&existing)
	if e == nil {
		return o, &SafetyError{Code: "duplicate_content", ExistingID: existing}
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return o, e
	}
	if iccid == "" {
		return o, safety("card_identity_required")
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO outgoing(id,device_id,tid,fingerprint,slot,phone,body,segments,iccid,created_at,updated_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, device, tid, key, in.Slot, in.Phone, in.Body, segments, iccid, now, now, now+300)
	if e != nil {
		return o, e
	}
	if e = tx.Commit(); e != nil {
		return o, e
	}
	return s.Outgoing(ctx, in.ID, now)
}

// ClaimOutgoing persists the irreversible "possibly sent" boundary BEFORE any
// network write. It is safe across concurrent callers and application instances.
// A prior claim always wins, even if the write/response was lost.
func (s *Store) ClaimOutgoing(ctx context.Context, id, iccid string, now int64, limits SendLimits) (Outgoing, bool, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Outgoing{}, false, e
	}
	defer tx.Rollback()
	if blocked, e := restoreBlocked(ctx, tx); e != nil {
		return Outgoing{}, false, e
	} else if blocked {
		return Outgoing{}, false, safety("outbound_restore_blocked")
	}
	o, e := scanOutgoing(tx.QueryRowContext(ctx, `SELECT `+outgoingColumns+` FROM outgoing WHERE id=?`, id))
	if e != nil {
		return o, false, e
	}
	if o.DispatchedAt > 0 {
		return o, false, nil
	}
	if o.State != "prepared" || now >= o.ExpiresAt {
		return o, false, safety("preview_expired")
	}
	if iccid == "" || iccid != o.ICCID {
		return o, false, safety("card_changed")
	}
	if e = chargeableSendAllowed(ctx, tx, o.DeviceID, o.ICCID, now, limits, false); e != nil {
		return o, false, e
	}
	// Recheck content at the claim boundary, including previews prepared earlier.
	var duplicate string
	e = tx.QueryRowContext(ctx, `SELECT id FROM outgoing WHERE device_id=? AND iccid=? AND phone=? AND body=? AND id!=? AND dispatched_at>0 AND (dispatched_at>? OR (rank<4 AND state!='failed')) LIMIT 1`, o.DeviceID, o.ICCID, o.Phone, o.Body, id, now-86400).Scan(&duplicate)
	if e == nil {
		return o, false, &SafetyError{Code: "duplicate_content", ExistingID: duplicate}
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return o, false, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE outgoing SET dispatched_at=?,deadline_at=?,state='unknown',rank=1,updated_at=? WHERE id=? AND dispatched_at=0`, now, now+60, now, id)
	if e != nil {
		return o, false, e
	}
	if e = tx.Commit(); e != nil {
		return o, false, e
	}
	o, e = s.Outgoing(ctx, id, now)
	return o, true, e
}

func (s *Store) MarkOutgoingWrite(ctx context.Context, id string, written bool, now int64) error {
	state, code := "waiting_device", ""
	if !written {
		state, code = "unknown", "transport_uncertain"
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE outgoing SET state=?,error_code=?,updated_at=? WHERE id=? AND rank=1 AND state='unknown'`, state, code, now, id)
	return e
}

func (s *Store) CancelOutgoing(ctx context.Context, id string, now int64) error {
	r, e := s.DB.ExecContext(ctx, `UPDATE outgoing SET state='canceled',updated_at=? WHERE id=? AND rank=0 AND state='prepared'`, now, id)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return safety("already_dispatched")
	}
	return nil
}

func (s *Store) ExpireOutgoing(ctx context.Context, now int64) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE outgoing SET state='expired' WHERE rank=0 AND state='prepared' AND expires_at<=?; UPDATE outgoing SET state='unknown' WHERE rank BETWEEN 1 AND 3 AND state IN ('waiting_device','accepted','processed') AND deadline_at<=?;`, now, now)
	return e
}

func (s *Store) Outgoing(ctx context.Context, id string, now int64) (Outgoing, error) {
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return Outgoing{}, e
	}
	defer tx.Rollback()
	o, e := scanOutgoing(tx.QueryRowContext(ctx, `SELECT `+outgoingColumns+` FROM outgoing WHERE id=?`, id))
	if e != nil {
		return o, e
	}
	return outgoingView(ctx, tx, o, now)
}
func (s *Store) OutgoingPage(ctx context.Context, before int64, now int64, card string) ([]Outgoing, error) {
	if !ValidCardFilter(card) {
		return nil, safety("invalid_filter")
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT `+outgoingColumns+` FROM outgoing WHERE (?=0 OR rowid<?) AND (?='' OR (?='unknown' AND NOT EXISTS(SELECT 1 FROM cards WHERE cards.iccid=trim(outgoing.iccid))) OR EXISTS(SELECT 1 FROM cards WHERE cards.id=? AND cards.iccid=trim(outgoing.iccid))) ORDER BY rowid DESC LIMIT 51`, before, before, card, card, card)
	if e != nil {
		return nil, e
	}
	list := []Outgoing{}
	for rows.Next() {
		o, e := scanOutgoing(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		list = append(list, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i := range list {
		list[i], e = outgoingView(ctx, tx, list[i], now)
		if e != nil {
			return nil, e
		}
	}
	return list, nil
}

func outgoingView(ctx context.Context, tx *sql.Tx, o Outgoing, now int64) (Outgoing, error) {
	if iccid := cardIdentity(o.ICCID); iccid != "" {
		err := tx.QueryRowContext(ctx, `SELECT id,name,number FROM cards WHERE iccid=?`, iccid).Scan(&o.CardID, &o.CardCurrentName, &o.CardNumber)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return o, err
		}
	}
	if len(o.ICCID) >= 4 {
		o.CardTail = o.ICCID[len(o.ICCID)-4:]
	}
	o.LastPhase = []string{"prepared", "dispatch_started", "accepted", "processed", "submitted"}[min(o.Rank, 4)]
	if o.Rank == 0 && o.State == "prepared" && now >= o.ExpiresAt {
		o.State = "expired"
	}
	if o.Rank > 0 && o.Rank < 4 && o.State != "failed" && now >= o.DeadlineAt {
		o.State = "unknown"
	}
	o.Delivery = "unconfirmed"
	return deliveryView(ctx, tx, o)
}

// chargeableSendAllowed is the single quota check for ordinary outgoing SMS and
// voicemail control SMS on one SIM. Both paths count both ledgers inside the
// caller's transaction. relaxInterval is only for another required command
// inside the same confirmed voicemail action; that command still uses a daily slot.
func chargeableSendAllowed(ctx context.Context, tx *sql.Tx, device, iccid string, now int64, limits SendLimits, relaxInterval bool) error {
	if limits.Interval < 10 || limits.Daily < 1 || limits.DayEnd <= limits.DayStart {
		return safety("invalid_send_limits")
	}
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT max(ts) FROM (SELECT dispatched_at AS ts FROM outgoing WHERE device_id=? AND iccid=? AND dispatched_at>0 UNION ALL SELECT sent_at FROM voicemail_controls WHERE device_id=? AND iccid=? AND sent_at>0)`, device, iccid, device, iccid).Scan(&last); err != nil {
		return err
	}
	if !relaxInterval && last.Valid && now-last.Int64 < limits.Interval {
		return safety("send_interval")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM outgoing WHERE device_id=? AND iccid=? AND dispatched_at>=? AND dispatched_at<?) + (SELECT count(*) FROM voicemail_controls WHERE device_id=? AND iccid=? AND sent_at>=? AND sent_at<?)`, device, iccid, limits.DayStart, limits.DayEnd, device, iccid, limits.DayStart, limits.DayEnd).Scan(&count); err != nil {
		return err
	}
	if count >= limits.Daily {
		return safety("daily_limit")
	}
	return nil
}

func DayLimits(now time.Time, interval int64, daily int) SendLimits {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return SendLimits{Interval: interval, Daily: daily, DayStart: start.Unix(), DayEnd: start.AddDate(0, 0, 1).Unix()}
}
