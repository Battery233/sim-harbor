package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

// This bounds the span of the SMS timestamps, not network arrival times.
const smsAssemblyWindow int64 = 120

const smsReceiptSchema = `CREATE TABLE IF NOT EXISTS sms_receipts(key TEXT PRIMARY KEY,message_id INTEGER NOT NULL REFERENCES messages(id),sms_ts INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS sms_receipts_message ON sms_receipts(message_id);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(17);`

// Keep the exact business key compatible with old databases. A separate receipt
// key remembers each timestamp accepted into an assembly, including after deletion.
func smsBusinessKeys(e protocol.Event) (string, string) {
	iccid := cardIdentity(e.ICCID)
	identity := any(e.Slot)
	if iccid != "" {
		identity = "card"
	}
	key := func(location any) string {
		values := []any{e.DeviceID, location, iccid, e.MD5}
		if e.Group != "" {
			values = []any{e.DeviceID, location, iccid, "group", e.Group, e.Phone, e.SMSTS}
		} else if e.MD5 == "" {
			values = []any{e.DeviceID, location, iccid, e.Phone, e.SMSTS, e.Body}
		}
		p, _ := json.Marshal(values)
		return hash(p)
	}
	return key(identity), key(e.Slot)
}

func parseSMSFragments(fragments []string, total int) (map[string]string, error) {
	parts := map[string]string{}
	for _, fragment := range fragments {
		key, body, ok := strings.Cut(fragment, ",")
		n, err := strconv.Atoi(key)
		if !ok || err != nil || n < 1 || n > total {
			return nil, ErrSMSFragmentInvalid
		}
		key = strconv.Itoa(n)
		if old, exists := parts[key]; exists && old != body {
			return nil, ErrSMSFragmentConflict
		}
		parts[key] = body
	}
	return parts, nil
}

func resolveSMSKey(ctx context.Context, tx *sql.Tx, e protocol.Event, cardID string, incoming map[string]string) (string, error) {
	key, legacy := smsBusinessKeys(e)
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT key FROM messages WHERE key IN (?,?) ORDER BY (key=?) DESC LIMIT 1`, key, legacy, key).Scan(&existing)
	if err == nil {
		if existing != key {
			_, err = tx.ExecContext(ctx, `UPDATE messages SET key=? WHERE key=?`, key, existing)
		}
		return key, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	err = tx.QueryRowContext(ctx, `SELECT m.key FROM sms_receipts r JOIN messages m ON m.id=r.message_id WHERE r.key=?`, key).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// Unindexed aggregates, unknown SIMs and plain/single-part receipts retain
	// exact matching. Do not infer identity from a name, slot or body similarity.
	if cardID == "" || e.DeviceID == "" || e.Phone == "" || e.Group == "" || e.Max <= 1 || len(incoming) == 0 || (e.Complete != nil && *e.Complete) {
		return key, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,key,complete,sms_total,fragments,sms_ts FROM messages WHERE sms_group_ref=? AND sms_ts BETWEEN ? AND ? AND device_id=? AND card_id=? AND phone=? AND deleted_at=0 LIMIT 2`, SMSGroupReference(e), int64(e.SMSTS)-smsAssemblyWindow, int64(e.SMSTS)+smsAssemblyWindow, e.DeviceID, cardID, e.Phone)
	if err != nil {
		return "", err
	}
	var id, timestamp int64
	var complete bool
	var total, count int
	var fragments string
	for rows.Next() {
		count++
		if err = rows.Scan(&id, &existing, &complete, &total, &fragments, &timestamp); err != nil {
			rows.Close()
			return "", err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	// Even an incompatible nearby record is evidence of group reuse. Do not
	// choose a convenient candidate out of several, or add data to a closed SMS.
	if count != 1 || complete || total != int(e.Max) {
		return key, nil
	}
	var saved map[string]string
	if err = json.Unmarshal([]byte(fragments), &saved); err != nil {
		return "", err
	}
	if len(saved) == 0 {
		return key, nil
	}
	for index, body := range saved {
		n, err := strconv.Atoi(index)
		if err != nil || n < 1 || n > total {
			return key, nil
		}
		if value, ok := incoming[index]; ok && value != body {
			return key, nil
		}
	}
	var earliest, latest sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT min(sms_ts),max(sms_ts) FROM sms_receipts WHERE message_id=?`, id).Scan(&earliest, &latest); err != nil {
		return "", err
	}
	low, high := timestamp, timestamp
	if earliest.Valid {
		low = min(low, earliest.Int64)
		high = max(high, latest.Int64)
	}
	low, high = min(low, int64(e.SMSTS)), max(high, int64(e.SMSTS))
	if high-low > smsAssemblyWindow {
		return key, nil
	}
	return existing, nil
}
