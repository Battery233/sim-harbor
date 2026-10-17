package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode"
)

const cardSchema = `CREATE TABLE cards(id TEXT PRIMARY KEY,iccid TEXT UNIQUE NOT NULL,name TEXT NOT NULL,group_name TEXT NOT NULL);
ALTER TABLE messages ADD COLUMN card_id TEXT REFERENCES cards(id);
ALTER TABLE messages ADD COLUMN device_id TEXT NOT NULL DEFAULT '';
CREATE INDEX messages_card ON messages(card_id,id);
CREATE INDEX outgoing_card_limits ON outgoing(device_id,iccid,dispatched_at);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(7);`

const cardNumberSchema = `ALTER TABLE cards ADD COLUMN number TEXT NOT NULL DEFAULT '';
DELETE FROM schema_version; INSERT INTO schema_version VALUES(10);`

type Card struct {
	NotificationEnabled bool   `json:"notification_enabled"`
	Number              string `json:"number"`
	MessageCount        int    `json:"message_count"`
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Tail                string `json:"tail"`
	Slot                int    `json:"current_slot"`
}

// Never infer a card from a slot. ICCID is kept private; public IDs are random.
func cardIdentity(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 64 || strings.ContainsFunc(value, unicode.IsControl) {
		return ""
	}
	return value
}
func ValidCardFilter(value string) bool {
	if value == "" || value == "unknown" {
		return true
	}
	if len(value) != 16 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
func ensureCard(ctx context.Context, tx *sql.Tx, iccid, name string, current bool) (string, error) {
	iccid = cardIdentity(iccid)
	if iccid == "" {
		return "", nil
	}
	name = cleanCardName(name)
	var id, oldName string
	err := tx.QueryRowContext(ctx, `SELECT id,name FROM cards WHERE iccid=?`, iccid).Scan(&id, &oldName)
	if err == nil {
		if name != "" && (current || oldName == "") {
			_, err = tx.ExecContext(ctx, `UPDATE cards SET name=? WHERE id=?`, name, id)
		}
		return id, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	b := make([]byte, 8)
	if _, err = rand.Read(b); err != nil {
		return "", err
	}
	id = hex.EncodeToString(b)
	label := name
	if label == "" {
		label = "SIM"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cards(id,iccid,name,group_name) VALUES(?,?,?,?)`, id, iccid, name, label+" · "+id)
	return id, err
}
func recordStatusCards(ctx context.Context, tx *sql.Tx, info map[string]any) error {
	for slot := 1; slot <= 2; slot++ {
		n := strconv.Itoa(slot)
		iccid, _ := info["sim"+n+"_iccId"].(string)
		name, _ := info["sim"+n+"_scName"].(string)
		state, _ := info["slot"+n+"_sta"].(string)
		if state != "OK" {
			continue
		}
		id, err := ensureCard(ctx, tx, iccid, name, true)
		if err != nil {
			return err
		}
		// Missing/empty modem metadata is not an instruction to erase the
		// identity's saved contact details after removal or reinitialization.
		number, _ := info["sim"+n+"_msIsdn"].(string)
		number = cleanCardName(number)
		if number != "" && id != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE cards SET number=? WHERE id=?`, number, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Current location is usable only with a fresh status in the active session.
// A restored registry may contain any number of historical cards.
func (s *Store) Cards(ctx context.Context, info map[string]any, verified bool) ([]Card, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,c.iccid,c.name,c.number,c.notification_enabled,(SELECT count(*) FROM messages m WHERE m.card_id=c.id AND m.deleted_at=0) FROM cards c ORDER BY c.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Card{}
	for rows.Next() {
		var c Card
		var iccid string
		if err = rows.Scan(&c.ID, &iccid, &c.Name, &c.Number, &c.NotificationEnabled, &c.MessageCount); err != nil {
			return nil, err
		}
		if len(iccid) >= 4 {
			c.Tail = iccid[len(iccid)-4:]
		}
		if verified {
			for slot := 1; slot <= 2; slot++ {
				n := strconv.Itoa(slot)
				v, _ := info["sim"+n+"_iccId"].(string)
				state, _ := info["slot"+n+"_sta"].(string)
				if state == "OK" && cardIdentity(v) == iccid {
					c.Slot = slot
				}
			}
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

const messageColumns = `id,slot,phone,body,sms_ts,complete,is_read,coalesce(card_id,''),card_name,fragments,sms_total,sms_group_ref`

func scanMessage(row scanner) (Message, error) {
	var m Message
	var parts, group string
	err := row.Scan(&m.ID, &m.Slot, &m.Phone, &m.Body, &m.Timestamp, &m.Complete, &m.Read, &m.CardID, &m.CardName, &parts, &m.PartsTotal, &group)
	if err == nil {
		err = m.setMultipart(parts, group)
	}
	return m, err
}
func (s *Store) CardMessagePage(ctx context.Context, card string, slot int, q string, before int64, limit int) ([]Message, error) {
	if !ValidCardFilter(card) {
		return nil, errors.New("invalid_filter")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE deleted_at=0 AND (?='' OR (?='unknown' AND card_id IS NULL) OR card_id=?) AND (?=0 OR slot=?) AND (?='' OR instr(body,?)>0 OR instr(phone,?)>0) AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, card, card, card, slot, slot, q, q, q, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Message{}
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
