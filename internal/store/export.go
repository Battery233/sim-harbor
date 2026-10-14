package store

import (
	"context"
	"database/sql"
	"errors"
)

const ExportLimit = 10000
const ExportBytes = 32 << 20

var ErrExportLimit = errors.New("export_limit_exceeded")

type MessageFilter struct {
	Card  string `json:"card"`
	Slot  int    `json:"slot"`
	Phone string `json:"phone"`
	From  int64  `json:"from"`
	Until int64  `json:"until"`
}

func (f MessageFilter) Valid() bool {
	return ValidCardFilter(f.Card) && (f.Card == "" || f.Slot == 0) && f.Slot >= 0 && f.Slot <= 2 && len(f.Phone) <= 64 && f.From >= 0 && f.Until >= 0 && f.From <= 4102444800 && f.Until <= 4102444800 && (f.Until == 0 || f.From < f.Until)
}

// One SELECT in a read transaction includes all matching inbox rows, rather
// than an API-page loop which could mix concurrent database states.
func (s *Store) ExportMessages(ctx context.Context, f MessageFilter) ([]Message, error) {
	if !f.Valid() {
		return nil, errors.New("invalid_filter")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE deleted_at=0 AND (?='' OR (?='unknown' AND card_id IS NULL) OR card_id=?) AND (?=0 OR slot=?) AND (?='' OR phone=?) AND (?=0 OR sms_ts>=?) AND (?=0 OR sms_ts<?) ORDER BY id DESC LIMIT ?`, f.Card, f.Card, f.Card, f.Slot, f.Slot, f.Phone, f.Phone, f.From, f.From, f.Until, f.Until, ExportLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Message{}
	size := 0
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			err = e
			return nil, err
		}
		size += len(m.Body) + len(m.Phone) + len(m.CardName) + 192
		if len(items) >= ExportLimit || size > ExportBytes {
			return nil, ErrExportLimit
		}
		items = append(items, m)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
