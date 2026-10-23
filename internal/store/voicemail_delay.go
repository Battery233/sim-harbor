package store

import (
	"context"
	"database/sql"
	"strings"
)

const VoicemailWaitSeconds int64 = 180

func migrateVoicemailDelay(tx *sql.Tx) error {
	var found int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('calls') WHERE name='voicemail_due_at'`).Scan(&found); err != nil {
		return err
	}
	if found == 0 {
		if _, err := tx.Exec(`ALTER TABLE calls ADD COLUMN voicemail_due_at INTEGER NOT NULL DEFAULT 0 CHECK(voicemail_due_at>=-1); UPDATE calls SET voicemail_due_at=-1;`); err != nil {
			return err
		}
	}
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('call_notifications') WHERE name='phase'`).Scan(&found); err != nil {
		return err
	}
	if found == 0 {
		if _, err := tx.Exec(`CREATE TABLE call_notifications_v20(
   id INTEGER PRIMARY KEY,
   call_id INTEGER NOT NULL REFERENCES calls(id),
   card_name TEXT NOT NULL,
   group_name TEXT NOT NULL,
   state TEXT NOT NULL DEFAULT 'pending',
   attempts INTEGER NOT NULL DEFAULT 0,
   next_at INTEGER NOT NULL DEFAULT 0,
   last_error TEXT NOT NULL DEFAULT '',
   accepted_at INTEGER NOT NULL DEFAULT 0 CHECK(accepted_at>=0),
   kind TEXT NOT NULL DEFAULT 'missed' CHECK(kind IN ('missed','timeout','voicemail','voicemail_missing','voicemail_late')),
   attempted_at INTEGER NOT NULL DEFAULT 0,
   phase INTEGER NOT NULL DEFAULT 0 CHECK(phase IN (0,1)),
   created_at INTEGER NOT NULL DEFAULT 0,
   UNIQUE(call_id,phase)
  );
  INSERT INTO call_notifications_v20(id,call_id,card_name,group_name,state,attempts,next_at,last_error,accepted_at,kind,attempted_at,created_at)
  SELECT n.id,n.call_id,n.card_name,n.group_name,n.state,n.attempts,n.next_at,n.last_error,n.accepted_at,n.kind,n.attempted_at,c.start_ts FROM call_notifications n JOIN calls c ON c.id=n.call_id;
  DROP TABLE call_notifications;
  ALTER TABLE call_notifications_v20 RENAME TO call_notifications;
  CREATE INDEX call_notifications_success_time ON call_notifications(state,accepted_at);`); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS calls_voicemail_due ON calls(voicemail_due_at) WHERE voicemail_due_at>0; DELETE FROM schema_version;INSERT INTO schema_version VALUES(20);`)
	return err
}

// The worker holds notificationGate, also used by uploads and preference edits.
// Only newly completed, confirmed answered calls arm this persisted deadline.
func (s *Store) QueueVoicemailTimeouts(ctx context.Context, now int64, slots ...int) error {
	selected := []string{}
	for _, slot := range []int{1, 2} {
		for _, enabled := range slots {
			if slot == enabled {
				if slot == 1 {
					selected = append(selected, "1")
				} else {
					selected = append(selected, "2")
				}
				break
			}
		}
	}
	scope := "0"
	if len(selected) > 0 {
		scope = strings.Join(selected, ",")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	blocked, err := restoreBlocked(ctx, tx)
	if err != nil || blocked {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE calls SET voicemail_due_at=-1 WHERE voicemail_due_at>0 AND slot NOT IN (`+scope+`);
 UPDATE call_notifications SET state='canceled',last_error='voicemail_wait_canceled' WHERE kind='voicemail_missing' AND state='pending' AND call_id IN (SELECT id FROM calls WHERE state<>'answered' OR slot NOT IN (`+scope+`));`); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,state,last_error,kind,created_at)
 SELECT c.id,c.card_name,coalesce(card.group_name,'来源未确认'),CASE WHEN coalesce(card.notification_enabled,1)=1 THEN 'pending' ELSE 'canceled' END,CASE WHEN coalesce(card.notification_enabled,1)=1 THEN '' ELSE 'card_notifications_disabled' END,'voicemail_missing',?
 FROM calls c LEFT JOIN cards card ON card.id=c.card_id WHERE c.voicemail_due_at>0 AND c.voicemail_due_at<=? AND c.state='answered' AND c.slot IN (`+scope+`) AND NOT EXISTS(SELECT 1 FROM voicemails v WHERE v.call_id=c.id)`, now, now)
	if err != nil {
		return err
	}
	// Finished queueing: keep the job as the persistent dedup evidence, and
	// retire its timer so each worker tick scans only genuinely waiting calls.
	if _, err = tx.ExecContext(ctx, `UPDATE calls SET voicemail_due_at=-1 WHERE voicemail_due_at>0 AND voicemail_due_at<=? AND EXISTS(SELECT 1 FROM call_notifications n WHERE n.call_id=calls.id AND n.phase=0)`, now); err != nil {
		return err
	}
	return tx.Commit()
}

// If no missing-audio push was attempted yet, collapse it into the normal
// voicemail push. Otherwise enqueue one distinct late-audio follow-up. A lost
// Bark response counts as attempted, so it cannot be rewritten as "no audio"
// after the audio arrives. The unique phase persists dedup across restarts.
func queueLinkedVoicemail(ctx context.Context, tx *sql.Tx, callID, now int64, held bool) error {
	var id, attempted int64
	var kind, state string
	err := tx.QueryRowContext(ctx, `SELECT id,kind,state,attempted_at FROM call_notifications WHERE call_id=? AND phase=0`, callID).Scan(&id, &kind, &state, &attempted)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE calls SET voicemail_due_at=-1 WHERE id=?`, callID); err != nil {
		return err
	}
	hold := 0
	if held {
		hold = 1
	}
	if kind == "voicemail_missing" && (state == "sent" || (state == "pending" && attempted > 0)) {
		if _, err = tx.ExecContext(ctx, `UPDATE call_notifications SET state='canceled',last_error='voicemail_arrived' WHERE id=? AND state='pending'`, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,state,last_error,kind,phase,created_at)
   SELECT c.id,c.card_name,coalesce(card.group_name,'来源未确认'),CASE WHEN coalesce(card.notification_enabled,1)=0 THEN 'canceled' WHEN ?=1 THEN 'restore_held' ELSE 'pending' END,CASE WHEN coalesce(card.notification_enabled,1)=0 THEN 'card_notifications_disabled' WHEN ?=1 THEN 'restored_notification_held' ELSE '' END,'voicemail_late',1,?
   FROM calls c LEFT JOIN cards card ON card.id=c.card_id WHERE c.id=?`, hold, hold, now, callID)
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE call_notifications SET kind='voicemail',created_at=?,state=CASE WHEN ?=1 THEN 'restore_held' ELSE state END,last_error=CASE WHEN ?=1 THEN 'restored_notification_held' ELSE last_error END WHERE call_id=? AND phase=0 AND state='pending' AND attempted_at=0 AND kind IN ('missed','timeout','voicemail_missing')`, now, hold, hold, callID)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil || changed > 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO call_notifications(call_id,card_name,group_name,state,last_error,kind,created_at)
 SELECT c.id,c.card_name,coalesce(card.group_name,'来源未确认'),CASE WHEN coalesce(card.notification_enabled,1)=0 THEN 'canceled' WHEN ?=1 THEN 'restore_held' ELSE 'pending' END,CASE WHEN ?=1 AND coalesce(card.notification_enabled,1)=1 THEN 'restored_notification_held' ELSE '' END,'voicemail',?
 FROM calls c LEFT JOIN cards card ON card.id=c.card_id WHERE c.id=?`, hold, hold, now, callID)
	return err
}
