package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// MigrationState contains no message, phone, SIM identity or credential data.
type MigrationState struct {
	Restored        bool   `json:"restored"`
	Blocked         bool   `json:"blocked"`
	Revision        string `json:"revision"`
	RestoredAt      int64  `json:"restored_at"`
	ArchiveSHA256   string `json:"archive_sha256"`
	ConfirmedAt     int64  `json:"confirmed_at"`
	BackupSHA256    string `json:"confirmation_backup_sha256"`
	Policy          string `json:"confirmation_policy"`
	LedgerRevision  string `json:"ledger_revision"`
	UnknownOutgoing int64  `json:"unknown_outgoing"`
}

func (s *Store) MigrationState(ctx context.Context) (MigrationState, error) {
	var m MigrationState
	err := s.DB.QueryRowContext(ctx, `SELECT restore_id,restored_at,archive_sha256,confirmed_at,confirmation_backup_sha256,confirmation_policy,(SELECT coalesce(group_concat(id||':'||state||':'||rank||':'||dispatched_at||':'||updated_at,'|'),'') FROM (SELECT * FROM outgoing ORDER BY rowid)),(SELECT count(*) FROM outgoing WHERE state='unknown' OR (dispatched_at>0 AND rank<4 AND state!='failed')) FROM recovery_guard WHERE id=1`).Scan(&m.Revision, &m.RestoredAt, &m.ArchiveSHA256, &m.ConfirmedAt, &m.BackupSHA256, &m.Policy, &m.LedgerRevision, &m.UnknownOutgoing)
	if errors.Is(err, sql.ErrNoRows) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	m.LedgerRevision = hash([]byte(m.LedgerRevision))
	m.Restored = true
	m.Blocked = m.ConfirmedAt == 0
	return m, nil
}

// This only records an operator's attestation. It cannot prove another instance
// is stopped or reconstruct missing history, and never claims or retries SMS.
func (s *Store) ConfirmMigration(ctx context.Context, revision, backup, ledger string, now int64) error {
	b, err := hex.DecodeString(backup)
	if err != nil || len(b) != 32 || len(revision) != 32 || now <= 0 {
		return safety("migration_confirmation_invalid")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(group_concat(id||':'||state||':'||rank||':'||dispatched_at||':'||updated_at,'|'),'') FROM (SELECT * FROM outgoing ORDER BY rowid)`).Scan(&current); err != nil {
		return err
	}
	if hash([]byte(current)) != ledger {
		return safety("migration_state_changed")
	}
	result, err := tx.ExecContext(ctx, `UPDATE recovery_guard SET confirmed_at=?,confirmation_backup_sha256=?,confirmation_policy='complete_history_v1' WHERE id=1 AND restore_id=? AND confirmed_at=0`, now, backup, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return safety("migration_state_changed")
	}
	return tx.Commit()
}

func migrateRecoveryConfirmation(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(recovery_guard)`)
	if err != nil {
		return err
	}
	found := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		found[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range []struct{ name, decl string }{{"restore_id", "TEXT NOT NULL DEFAULT ''"}, {"confirmed_at", "INTEGER NOT NULL DEFAULT 0"}, {"confirmation_backup_sha256", "TEXT NOT NULL DEFAULT ''"}, {"confirmation_policy", "TEXT NOT NULL DEFAULT ''"}} {
		if !found[c.name] {
			if _, err = tx.Exec(fmt.Sprintf("ALTER TABLE recovery_guard ADD COLUMN %s %s", c.name, c.decl)); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(`UPDATE recovery_guard SET restore_id=lower(hex(randomblob(16))) WHERE restore_id='';DELETE FROM schema_version;INSERT INTO schema_version VALUES(15);`)
	return err
}
