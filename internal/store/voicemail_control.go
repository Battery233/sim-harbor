package store

import (
	"context"
	"database/sql"
	"strings"
)

func migrateVoicemailControls(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS voicemail_controls(
  id INTEGER PRIMARY KEY,
  device_id TEXT NOT NULL,
  slot INTEGER NOT NULL,
  phone TEXT NOT NULL,
  iccid TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('mode','tts')),
  mode INTEGER NOT NULL DEFAULT -1,
  clear_tts INTEGER NOT NULL DEFAULT 0 CHECK(clear_tts IN (0,1)),
  state TEXT NOT NULL CHECK(state IN ('claimed','accepted','rejected','unknown')),
  created_at INTEGER NOT NULL,
  sent_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS voicemail_controls_slot ON voicemail_controls(slot, sent_at);
CREATE INDEX IF NOT EXISTS voicemail_controls_limit ON voicemail_controls(device_id, iccid, sent_at);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(19);`)
	return err
}

type VoicemailControl struct {
	Phone    string
	ICCID    string
	Kind     string
	Mode     int
	ClearTTS bool
	State    string
	SentAt   int64
}

// ClaimVoicemailControl persists "may have sent" before any network write.
// The row stores the destination and SIM identity, never the password or the
// command body. The latest non-rejected command for this slot and kind is one
// attempt: repeating its target does not send again, including when the result
// is still unknown after a restart. A later command with the opposite target
// is a new attempt. relaxInterval is only for a later SMS inside the same
// confirmed action; the daily quota still applies, and it is the same quota
// as ordinary outgoing SMS.
func (s *Store) ClaimVoicemailControl(ctx context.Context, device string, slot int, phone, iccid, kind string, mode int, clear bool, now int64, limits SendLimits, relaxInterval bool) (int64, error) {
	if iccid == "" || (slot != 1 && slot != 2) || (kind != "mode" && kind != "tts") {
		return 0, safety("card_identity_required")
	}
	if limits.Interval < 10 || limits.Daily < 1 || limits.DayEnd <= limits.DayStart {
		return 0, safety("invalid_send_limits")
	}
	clearBit := 0
	if clear {
		clearBit = 1
	}
	if kind == "tts" {
		mode = -1
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var priorICCID string
	var priorMode, priorClear int
	err = tx.QueryRowContext(ctx, `SELECT iccid, mode, clear_tts FROM voicemail_controls WHERE device_id=? AND slot=? AND kind=? AND state!='rejected' ORDER BY sent_at DESC, id DESC LIMIT 1`, device, slot, kind).Scan(&priorICCID, &priorMode, &priorClear)
	if err == nil {
		if priorMode == mode && priorClear == clearBit {
			if priorICCID != iccid {
				return 0, safety("card_changed")
			}
			return 0, safety("control_already_dispatched")
		}
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	if err = chargeableSendAllowed(ctx, tx, device, iccid, now, limits, relaxInterval); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO voicemail_controls(device_id,slot,phone,iccid,kind,mode,clear_tts,state,created_at,sent_at) VALUES(?,?,?,?,?,?,?,'claimed',?,?)`, device, slot, strings.TrimSpace(phone), iccid, kind, mode, clearBit, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) MarkVoicemailControl(ctx context.Context, id int64, state string) error {
	switch state {
	case "accepted", "rejected", "unknown":
	default:
		return safety("invalid_board_action")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE voicemail_controls SET state=? WHERE id=? AND state='claimed'`, state, id)
	return err
}

func (s *Store) RecentVoicemailControls(ctx context.Context, slot int, since int64) ([]VoicemailControl, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT phone, iccid, kind, mode, clear_tts, state, sent_at FROM voicemail_controls WHERE slot=? AND sent_at>=? AND sent_at>0`, slot, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VoicemailControl{}
	for rows.Next() {
		var item VoicemailControl
		var clear int
		if err = rows.Scan(&item.Phone, &item.ICCID, &item.Kind, &item.Mode, &clear, &item.State, &item.SentAt); err != nil {
			return nil, err
		}
		item.ClearTTS = clear == 1
		out = append(out, item)
	}
	return out, rows.Err()
}
