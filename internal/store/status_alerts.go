package store

import (
	"context"
	"time"
)

const statusAlertSchema = `CREATE TABLE status_alert_monitors(key TEXT PRIMARY KEY CHECK(key IN ('board','sim1','sim2')),bad_since INTEGER NOT NULL DEFAULT 0,good_since INTEGER NOT NULL DEFAULT 0,active INTEGER NOT NULL DEFAULT 0,episode INTEGER NOT NULL DEFAULT 0);
CREATE TABLE status_alert_jobs(id INTEGER PRIMARY KEY,key TEXT NOT NULL REFERENCES status_alert_monitors(key),episode INTEGER NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('fault','recovery')),label TEXT NOT NULL DEFAULT '',state TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',accepted_at INTEGER NOT NULL DEFAULT 0,UNIQUE(key,episode,kind));
DELETE FROM schema_version;INSERT INTO schema_version VALUES(14);`
const OfflineAlertSeconds int64 = 900
const SIMAlertSeconds int64 = 1800
const AlertRecoverySeconds int64 = 300

type AlertObservation struct {
	Key     string
	Healthy *bool
	Label   string
}

// Restart never guesses continuity through time when the app was not observing.
func (s *Store) ResetAlertObservation(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE status_alert_monitors SET bad_since=0,good_since=0;UPDATE status_alert_jobs SET state='canceled',last_error='observation_restarted' WHERE state='pending'; UPDATE status_alert_monitors SET active=0 WHERE NOT EXISTS(SELECT 1 FROM status_alert_jobs j WHERE j.key=status_alert_monitors.key AND j.episode=status_alert_monitors.episode AND j.kind='fault' AND j.state='sent');`)
	return err
}
func (s *Store) ObserveAlerts(ctx context.Context, input []AlertObservation, now int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, o := range input {
		if o.Key != "board" && o.Key != "sim1" && o.Key != "sim2" {
			return &SafetyError{Code: "invalid_filter"}
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO status_alert_monitors(key) VALUES(?)`, o.Key); err != nil {
			return err
		}
		var bad, good, episode int64
		var active bool
		if err = tx.QueryRowContext(ctx, `SELECT bad_since,good_since,active,episode FROM status_alert_monitors WHERE key=?`, o.Key).Scan(&bad, &good, &active, &episode); err != nil {
			return err
		}
		if o.Healthy == nil {
			bad = 0
			good = 0
			if o.Key != "board" {
				if _, err = tx.ExecContext(ctx, `UPDATE status_alert_jobs SET state='canceled',last_error='status_unconfirmed' WHERE key=? AND state='pending'`, o.Key); err != nil {
					return err
				}
				var sent int
				if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM status_alert_jobs WHERE key=? AND episode=? AND kind='fault' AND state='sent'`, o.Key, episode).Scan(&sent); err != nil {
					return err
				}
				if sent == 0 {
					active = false
				}
			}
		} else if !*o.Healthy {
			good = 0
			if bad == 0 || now < bad {
				bad = now
			}
			threshold := SIMAlertSeconds
			if o.Key == "board" {
				threshold = OfflineAlertSeconds
			}
			if !active && now-bad >= threshold {
				episode++
				active = true
				if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO status_alert_jobs(key,episode,kind,label,created_at) VALUES(?,?,'fault',?,?)`, o.Key, episode, cleanCardName(o.Label), now); err != nil {
					return err
				}
			}
		} else {
			bad = 0
			if active {
				if good == 0 || now < good {
					good = now
				}
				if now-good >= AlertRecoverySeconds {
					var sent int
					if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM status_alert_jobs WHERE key=? AND episode=? AND kind='fault' AND state='sent'`, o.Key, episode).Scan(&sent); err != nil {
						return err
					}
					if _, err = tx.ExecContext(ctx, `UPDATE status_alert_jobs SET state='canceled',last_error='recovered_before_acceptance' WHERE key=? AND episode=? AND state='pending'`, o.Key, episode); err != nil {
						return err
					}
					if sent > 0 {
						if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO status_alert_jobs(key,episode,kind,label,created_at) VALUES(?,?,'recovery',?,?)`, o.Key, episode, cleanCardName(o.Label), now); err != nil {
							return err
						}
					}
					active = false
					good = 0
				}
			} else {
				good = 0
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE status_alert_monitors SET bad_since=?,good_since=?,active=?,episode=? WHERE key=?`, bad, good, active, episode, o.Key); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) nextStatusAlert(ctx context.Context) (Notification, error) {
	var n Notification
	err := s.DB.QueryRowContext(ctx, `SELECT id,key,kind,label,attempts FROM status_alert_jobs WHERE state='pending' AND next_at<=? ORDER BY id LIMIT 1`, time.Now().Unix()).Scan(&n.AlertID, &n.AlertKey, &n.AlertKind, &n.AlertLabel, &n.Attempts)
	return n, err
}
func (s *Store) statusAlertResult(ctx context.Context, n Notification, success bool, code string, now int64) error {
	if success {
		_, err := s.DB.ExecContext(ctx, `UPDATE status_alert_jobs SET state='sent',accepted_at=CASE WHEN state='sent' THEN accepted_at ELSE ? END,last_error='' WHERE id=? AND state IN ('pending','sent')`, now, n.AlertID)
		return err
	}
	delay := min(3600, 5*(1<<min(n.Attempts, 9)))
	_, err := s.DB.ExecContext(ctx, `UPDATE status_alert_jobs SET attempts=attempts+1,next_at=?,last_error=? WHERE id=? AND state='pending'`, now+int64(delay), notificationCode(code), n.AlertID)
	return err
}
func (s *Store) StatusAlertSummary(ctx context.Context) (map[string]any, error) {
	var active, pending, held int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM status_alert_monitors WHERE active=1),(SELECT count(*) FROM status_alert_jobs WHERE state='pending'),(SELECT count(*) FROM status_alert_jobs WHERE state='restore_held')`).Scan(&active, &pending, &held)
	return map[string]any{"active": active, "pending": pending, "restore_held": held}, err
}
