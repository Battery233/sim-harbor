package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

var (
	ErrSMSTotalMismatch    = errors.New("SMS total changed")
	ErrSMSFragmentInvalid  = errors.New("invalid SMS fragment")
	ErrSMSFragmentConflict = errors.New("conflicting SMS fragment")
)

// SMSGroupReference correlates reports without exposing the group, sender,
// device or SIM identity. Assembly also verifies identity, time and fragments.
func SMSGroupReference(e protocol.Event) string {
	if e.Group == "" {
		return ""
	}
	identity := any(cardIdentity(e.ICCID))
	if identity == "" {
		identity = int(e.Slot)
	}
	p, _ := json.Marshal([]any{e.DeviceID, identity, e.Phone, e.Group})
	return hash(p)[:24]
}

func migrateMultipart(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(messages)`)
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
	for name, ddl := range map[string]string{
		"sms_group_ref": `ALTER TABLE messages ADD COLUMN sms_group_ref TEXT NOT NULL DEFAULT ''`,
		"sms_total":     `ALTER TABLE messages ADD COLUMN sms_total INTEGER NOT NULL DEFAULT 0 CHECK(sms_total BETWEEN 0 AND 255)`,
	} {
		if !found[name] {
			if _, err = tx.Exec(ddl); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS messages_sms_group ON messages(sms_group_ref,sms_ts); DELETE FROM schema_version; INSERT INTO schema_version VALUES(16)`)
	return err
}

func (m *Message) setMultipart(partsJSON, group string) error {
	if m.PartsTotal < 0 || m.PartsTotal > 255 {
		return ErrSMSTotalMismatch
	}
	var parts map[string]string
	if err := json.Unmarshal([]byte(partsJSON), &parts); err != nil {
		return err
	}
	m.PartIndices = []int{}
	seen := map[int]bool{}
	for key := range parts {
		n, err := strconv.Atoi(key)
		if err != nil || n < 1 || n > 255 || (m.PartsTotal > 0 && n > m.PartsTotal) {
			return ErrSMSFragmentInvalid
		}
		if !seen[n] {
			m.PartIndices = append(m.PartIndices, n)
			seen[n] = true
		}
	}
	sort.Ints(m.PartIndices)
	// A group reference alone does not imply multiple parts. Keep the metadata,
	// but do not label a known single-part receipt multipart.
	m.Multipart = m.PartsTotal != 1 && (group != "" || m.PartsTotal > 1 || len(parts) > 0 || !m.Complete)
	if m.Complete && m.PartsTotal > 0 {
		// A complete board aggregate also attests that all expected pieces arrived.
		m.PartIndices = make([]int, m.PartsTotal)
		for i := range m.PartIndices {
			m.PartIndices[i] = i + 1
		}
	}
	m.PartsReceived = len(m.PartIndices)
	return nil
}

// Metadata lookup only; never changes ingestion, notifications or card data.
func (s *Store) SMSProgress(ctx context.Context, e protocol.Event) (int64, bool, error) {
	var id int64
	var complete bool
	key, legacy := smsBusinessKeys(e)
	err := s.DB.QueryRowContext(ctx, `SELECT id,complete FROM messages WHERE deleted_at=0 AND (key IN (?,?) OR id=(SELECT message_id FROM sms_receipts WHERE key=?)) ORDER BY (key=?) DESC,id DESC LIMIT 1`, key, legacy, key, key).Scan(&id, &complete)
	return id, complete, err
}
