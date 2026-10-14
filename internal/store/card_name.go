package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
)

func cleanCardName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	runes := []rune(strings.TrimSpace(name))
	if len(runes) > 64 {
		runes = runes[:64]
	}
	return string(runes)
}

// CardNameFromStatus is only a fallback for a current-session status. The
// caller supplies its freshness window; an exact nonempty ICCID match is required.
func (s *Store) CardNameFromStatus(ctx context.Context, device string, slot int, iccid string, earliest, latest int64) (string, error) {
	if (slot != 1 && slot != 2) || iccid == "" {
		return "", nil
	}
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT status FROM device WHERE id=? AND status_received_at BETWEEN ? AND ?`, device, earliest, latest).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var status struct {
		SlotInfo map[string]any `json:"slotInfo"`
	}
	if err = json.Unmarshal([]byte(raw), &status); err != nil {
		return "", err
	}
	n := strconv.Itoa(slot)
	card, _ := status.SlotInfo["sim"+n+"_iccId"].(string)
	state, _ := status.SlotInfo["slot"+n+"_sta"].(string)
	name, _ := status.SlotInfo["sim"+n+"_scName"].(string)
	if card != iccid || state != "OK" {
		return "", nil
	}
	return cleanCardName(name), nil
}
