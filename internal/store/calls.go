package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/Battery233/sim/internal/protocol"
)

const callSchema = `CREATE TABLE calls(id INTEGER PRIMARY KEY,key TEXT UNIQUE NOT NULL,device_id TEXT NOT NULL,card_id TEXT REFERENCES cards(id),card_name TEXT NOT NULL DEFAULT '',slot INTEGER NOT NULL,phone TEXT NOT NULL,start_ts INTEGER NOT NULL,connected_ts INTEGER NOT NULL DEFAULT 0,end_ts INTEGER NOT NULL DEFAULT 0,ended INTEGER NOT NULL DEFAULT 0,connection_report INTEGER NOT NULL DEFAULT -1,conflict INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL,received_at INTEGER NOT NULL);
CREATE INDEX calls_card ON calls(card_id,id);
CREATE TABLE call_notifications(id INTEGER PRIMARY KEY,call_id INTEGER UNIQUE NOT NULL REFERENCES calls(id),card_name TEXT NOT NULL,group_name TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',accepted_at INTEGER NOT NULL DEFAULT 0 CHECK(accepted_at>=0));
CREATE INDEX call_notifications_success_time ON call_notifications(state,accepted_at);
DELETE FROM schema_version;INSERT INTO schema_version VALUES(11);`

const callTimeoutSchema = `ALTER TABLE calls ADD COLUMN timeout_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE call_notifications ADD COLUMN kind TEXT NOT NULL DEFAULT 'missed' CHECK(kind IN ('missed','timeout'));
ALTER TABLE call_notifications ADD COLUMN attempted_at INTEGER NOT NULL DEFAULT 0;
DELETE FROM schema_version;INSERT INTO schema_version VALUES(12);`

const CallTimeoutSeconds int64 = 300

type Call struct {
	ID                int64  `json:"id"`
	CardID            string `json:"card_id"`
	CardName          string `json:"card_name"`
	Slot              int    `json:"slot"`
	Phone             string `json:"phone"`
	StartTS           int64  `json:"started_at"`
	ConnectedTS       int64  `json:"connected_at"`
	EndTS             int64  `json:"ended_at"`
	State             string `json:"state"`
	RingSeconds       *int64 `json:"ring_seconds"`
	NotificationState string `json:"notification_state"`
	Failures          int    `json:"notification_failures"`
	LastError         string `json:"notification_error"`
	AcceptedAt        int64  `json:"notification_accepted_at"`
	ReceivedAt        int64  `json:"received_at"`
	NotificationKind  string `json:"notification_kind"`
	TimeoutAt         int64  `json:"-"`
	Ended             bool   `json:"-"`
	ConnectionReport  int    `json:"-"`
	Conflict          bool   `json:"-"`
}

const callColumns = `c.id,coalesce(c.card_id,''),c.card_name,c.slot,c.phone,c.start_ts,c.connected_ts,c.end_ts,c.ended,c.connection_report,c.conflict,c.state,coalesce(n.state,'not_queued'),coalesce(n.attempts,0),coalesce(n.last_error,''),coalesce(n.accepted_at,0),c.received_at,coalesce(n.kind,''),c.timeout_at`
const callJoin = ` FROM calls c LEFT JOIN call_notifications n ON n.call_id=c.id `

// Search the stored caller and the labels displayed by the shared UI.
// Bound instr arguments keep wildcard and SQL-like input literal.
const callSearchFilter = ` (?='' OR instr(lower(c.phone),lower(?))>0 OR instr(CASE WHEN lower(trim(c.phone))='anonymous' THEN '匿名来电' WHEN trim(c.phone)='' THEN '号码未提供' ELSE '' END,?)>0) `

func scanCall(row scanner, now int64, extra ...any) (Call, error) {
	var c Call
	fields := []any{&c.ID, &c.CardID, &c.CardName, &c.Slot, &c.Phone, &c.StartTS, &c.ConnectedTS, &c.EndTS, &c.Ended, &c.ConnectionReport, &c.Conflict, &c.State, &c.NotificationState, &c.Failures, &c.LastError, &c.AcceptedAt, &c.ReceivedAt, &c.NotificationKind, &c.TimeoutAt}
	err := row.Scan(append(fields, extra...)...)
	if !c.Ended && (now < c.StartTS || (c.TimeoutAt > 0 && now >= c.TimeoutAt) || (c.TimeoutAt == 0 && now-c.StartTS > 300)) {
		c.State = "unknown"
	}
	if c.State == "missed" && c.EndTS >= c.StartTS {
		duration := c.EndTS - c.StartTS
		c.RingSeconds = &duration
	} else if (c.State == "answered" || c.State == "connected") && c.ConnectedTS >= c.StartTS {
		duration := c.ConnectedTS - c.StartTS
		c.RingSeconds = &duration
	}
	return c, err
}

func ingestCall(ctx context.Context, tx *sql.Tx, e protocol.Event, raw []byte, notify bool, now int64) error {
	// querytel's dir=0 means inbound; real-time telDir=1 means inbound.
	// Never interpret historical query rows through this real-time path.
	if e.TelDir != nil && *e.TelDir != 1 {
		return nil
	}
	iccid := cardIdentity(e.ICCID)
	card, err := ensureCard(ctx, tx, iccid, e.CardName, false)
	if err != nil {
		return err
	}
	identity := any(iccid)
	if iccid == "" {
		identity = int(e.Slot)
	}
	phone := strings.TrimSpace(e.Phone)
	keyData, _ := json.Marshal([]any{e.DeviceID, identity, int64(e.TelStartTS), phone})
	if e.TelStartTS == 0 {
		// Missing start cannot safely be merged with another call by slot/phone.
		keyData, _ = json.Marshal([]any{e.DeviceID, "unknown-start", hash(raw)})
	}
	key := hash(keyData)
	deadline := int64(0)
	if e.Type == 601 && e.TelStartTS > 0 && notify {
		deadline = now + CallTimeoutSeconds
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO calls(key,device_id,card_id,card_name,slot,phone,start_ts,state,received_at,timeout_at) VALUES(?,?,nullif(?,''),?,?,?,?,'ringing',?,?)`, key, e.DeviceID, card, cleanCardName(e.CardName), int(e.Slot), phone, int64(e.TelStartTS), now, deadline)
	if err != nil {
		return err
	}
	c, err := scanCall(tx.QueryRowContext(ctx, `SELECT `+callColumns+callJoin+` WHERE c.key=?`, key), now)
	if err != nil {
		return err
	}
	previousState := c.State
	if c.CardName == "" {
		c.CardName = cleanCardName(e.CardName)
	}
	if c.StartTS == 0 {
		c.Conflict = true
	}
	connected := int64(0)
	if e.TelConnectedTS != nil {
		connected = int64(*e.TelConnectedTS)
	}
	if connected > 0 {
		if connected < c.StartTS || (c.ConnectedTS > 0 && c.ConnectedTS != connected) || c.ConnectionReport == 0 || e.Type == 601 {
			c.Conflict = true
		}
		if c.ConnectedTS == 0 {
			c.ConnectedTS = connected
		}
	}
	if e.Type == 602 && connected == 0 {
		c.Conflict = true
	}
	if e.Type == 603 {
		c.Ended = true
		end := int64(e.TelEndTS)
		if end <= 0 || end < c.StartTS || (c.EndTS > 0 && c.EndTS != end) {
			c.Conflict = true
		}
		if c.EndTS == 0 {
			c.EndTS = end
		}
		if e.TelConnectedTS != nil {
			report := 0
			if connected > 0 {
				report = 1
			}
			if (c.ConnectionReport >= 0 && c.ConnectionReport != report) || (report == 0 && c.ConnectedTS > 0) {
				c.Conflict = true
			}
			c.ConnectionReport = report
		}
	}
	if c.EndTS > 0 && c.ConnectedTS > c.EndTS {
		c.Conflict = true
	}
	c.State = "ringing"
	switch {
	case c.Conflict:
		c.State = "unknown"
	case c.Ended && c.ConnectedTS > 0:
		c.State = "answered"
	case c.Ended && c.ConnectionReport == 0:
		c.State = "missed"
	case c.Ended:
		c.State = "unknown"
	case c.ConnectedTS > 0:
		c.State = "connected"
	}
	_, err = tx.ExecContext(ctx, `UPDATE calls SET card_name=?,connected_ts=?,end_ts=?,ended=?,connection_report=?,conflict=?,state=?,received_at=? WHERE id=?`, c.CardName, c.ConnectedTS, c.EndTS, c.Ended, c.ConnectionReport, c.Conflict, c.State, now, c.ID)
	if err != nil {
		return err
	}
	if notify {
		// A start received while muted stays muted even if enabled before its end.
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,state,kind) SELECT ?,?,coalesce(group_name,'来源未确认'),'canceled','timeout' FROM cards WHERE id=? AND notification_enabled=0`, c.ID, c.CardName, c.CardID); err != nil {
			return err
		}
	}
	if c.State != "missed" {
		_, err = tx.ExecContext(ctx, `UPDATE call_notifications SET state='canceled',last_error='call_state_uncertain' WHERE call_id=? AND state='pending' AND (kind='missed' OR ?)`, c.ID, c.Ended || c.ConnectedTS > 0 || c.Conflict)
		return err
	}
	// A not-yet-attempted fallback can become the accurate final reminder. Its
	// ID remains the same; attempted/sent/held notifications are never replaced.
	_, err = tx.ExecContext(ctx, `UPDATE call_notifications SET kind='missed' WHERE call_id=? AND kind='timeout' AND state='pending' AND attempts=0 AND attempted_at=0`, c.ID)
	if err != nil {
		return err
	}
	if notify && previousState != "missed" {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,state) VALUES(?,?,coalesce((SELECT group_name FROM cards WHERE id=?),'来源未确认'),CASE WHEN coalesce((SELECT notification_enabled FROM cards WHERE id=?),1)=1 THEN 'pending' ELSE 'canceled' END)`, c.ID, c.CardName, c.CardID, c.CardID)
	}
	return err
}

func (s *Store) Calls(ctx context.Context, card, q string, before, now int64) ([]Call, error) {
	q = strings.TrimSpace(q)
	if !ValidCardFilter(card) || before < 0 || len(q) > 128 {
		return nil, safety("invalid_filter")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+callColumns+callJoin+` WHERE (?='' OR (?='unknown' AND c.card_id IS NULL) OR c.card_id=?) AND `+callSearchFilter+` AND (?=0 OR c.id<?) ORDER BY c.id DESC LIMIT 51`, card, card, card, q, q, q, before, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Call{}
	for rows.Next() {
		c, err := scanCall(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Call(ctx context.Context, id, now int64) (Call, error) {
	return scanCall(s.DB.QueryRowContext(ctx, `SELECT `+callColumns+callJoin+` WHERE c.id=?`, id), now)
}

func (s *Store) nextCallNotification(ctx context.Context) (Notification, error) {
	var n Notification
	err := s.DB.QueryRowContext(ctx, `SELECT n.id,c.id,n.attempts,c.slot,c.phone,c.start_ts,CASE WHEN n.kind='missed' THEN c.end_ts-c.start_ts ELSE 0 END,n.card_name,n.group_name,n.kind FROM call_notifications n JOIN calls c ON c.id=n.call_id LEFT JOIN cards card ON card.id=c.card_id WHERE coalesce(card.notification_enabled,1)=1 AND n.state='pending' AND (c.state='missed' OR (n.kind='timeout' AND c.ended=0 AND c.connected_ts=0 AND c.conflict=0)) AND n.next_at<=? ORDER BY n.id LIMIT 1`, time.Now().Unix()).Scan(&n.ID, &n.CallID, &n.Attempts, &n.Slot, &n.Phone, &n.CallStartTS, &n.RingSeconds, &n.CardName, &n.CardGroup, &n.CallKind)
	return n, err
}

// Called by the notification worker under the same gate as final reports.
// The deadline uses the first server receipt, never the board's clock or a retry.
func (s *Store) QueueCallTimeouts(ctx context.Context, now int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,kind,state) SELECT c.id,c.card_name,coalesce(cards.group_name,'来源未确认'),'timeout',CASE WHEN coalesce(cards.notification_enabled,1)=1 THEN 'pending' ELSE 'canceled' END FROM calls c LEFT JOIN cards ON cards.id=c.card_id WHERE c.timeout_at>0 AND c.timeout_at<=? AND c.ended=0 AND c.connected_ts=0 AND c.conflict=0`, now)
	return err
}

func (s *Store) ReserveCallNotification(ctx context.Context, n Notification, now int64) (bool, error) {
	r, err := s.DB.ExecContext(ctx, `UPDATE call_notifications SET attempted_at=CASE WHEN attempted_at=0 THEN ? ELSE attempted_at END WHERE id=? AND call_id=? AND state='pending'`, now, n.ID, n.CallID)
	if err != nil {
		return false, err
	}
	count, err := r.RowsAffected()
	return count == 1, err
}

func (s *Store) callNotificationResult(ctx context.Context, n Notification, success bool, code string, now int64) error {
	if success {
		_, err := s.DB.ExecContext(ctx, `UPDATE call_notifications SET state='sent',last_error='',attempted_at=CASE WHEN attempted_at=0 THEN ? ELSE attempted_at END,accepted_at=CASE WHEN state='sent' THEN accepted_at ELSE ? END WHERE id=? AND state IN ('pending','sent')`, now, now, n.ID)
		return err
	}
	delay := min(3600, 5*(1<<min(n.Attempts, 9)))
	_, err := s.DB.ExecContext(ctx, `UPDATE call_notifications SET attempts=attempts+1,attempted_at=CASE WHEN attempted_at=0 THEN ? ELSE attempted_at END,next_at=?,last_error=? WHERE id=? AND state='pending'`, now, now+int64(delay), notificationCode(code), n.ID)
	return err
}
