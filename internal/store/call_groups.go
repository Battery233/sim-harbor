package store

import (
	"context"
	"database/sql"
	"strings"
)

// ID is the first call in a group, so another call/slot/name cannot change it.
// Unknown identities and withheld numbers never identify a shared caller.
type CallGroup struct {
	ID                 int64 `json:"id"`
	Latest             Call  `json:"latest"`
	Count              int64 `json:"count"`
	MissedCount        int64 `json:"missed_count"`
	UnplayedRecordings int64 `json:"unplayed_recordings"`
	RecordingCount     int64 `json:"recording_count"`
}

const callGroupScope = ` (?=0 OR c.id=? OR EXISTS(SELECT 1 FROM calls a WHERE a.id=? AND a.card_id IS NOT NULL AND trim(a.phone)<>'' AND lower(a.phone)<>'anonymous' AND c.card_id=a.card_id AND c.phone=a.phone)) `

func (s *Store) CallGroups(ctx context.Context, card, q string, before, now int64) ([]CallGroup, error) {
	return s.callGroups(ctx, card, q, before, 0, now)
}
func (s *Store) CallGroup(ctx context.Context, id, now int64) (CallGroup, error) {
	if id <= 0 {
		return CallGroup{}, safety("invalid_id")
	}
	rows, err := s.callGroups(ctx, "", "", 0, id, now)
	if err != nil {
		return CallGroup{}, err
	}
	if len(rows) == 0 {
		return CallGroup{}, sql.ErrNoRows
	}
	return rows[0], nil
}
func (s *Store) callGroups(ctx context.Context, card, q string, before, anchor, now int64) ([]CallGroup, error) {
	q = strings.TrimSpace(q)
	if !ValidCardFilter(card) || before < 0 || len(q) > 128 {
		return nil, safety("invalid_filter")
	}
	rows, err := s.DB.QueryContext(ctx, `WITH grouped AS (
 SELECT min(c.id) first_id,max(c.id) last_id,count(*) total,sum(CASE WHEN c.state='missed' THEN 1 ELSE 0 END) missed,count(vg.id) recordings,sum(CASE WHEN vg.stored_name<>'' AND vg.played_at=0 THEN 1 ELSE 0 END) unplayed
 FROM calls c LEFT JOIN voicemails vg ON vg.call_id=c.id AND vg.stored_name<>'' WHERE (?='' OR (?='unknown' AND c.card_id IS NULL) OR c.card_id=?) AND `+callSearchFilter+` AND `+callGroupScope+`
 GROUP BY c.card_id,c.phone,CASE WHEN c.card_id IS NULL OR trim(c.phone)='' OR lower(c.phone)='anonymous' THEN c.id ELSE 0 END
 ) SELECT `+callColumns+`,g.first_id,g.total,g.missed,g.recordings,g.unplayed`+callJoin+` JOIN grouped g ON g.last_id=c.id WHERE (?=0 OR g.last_id<?) ORDER BY g.last_id DESC LIMIT 51`, card, card, card, q, q, q, anchor, anchor, anchor, before, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CallGroup{}
	for rows.Next() {
		var g CallGroup
		g.Latest, err = scanCall(rows, now, &g.ID, &g.Count, &g.MissedCount, &g.RecordingCount, &g.UnplayedRecordings)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) CallsInGroup(ctx context.Context, id, before, now int64) ([]Call, error) {
	if id <= 0 || before < 0 {
		return nil, safety("invalid_filter")
	}
	// Check the anchor even when a cursor would otherwise yield an empty page.
	if _, err := s.Call(ctx, id, now); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+callColumns+callJoin+` WHERE `+callGroupScope+` AND (?=0 OR c.id<?) ORDER BY c.id DESC LIMIT 51`, id, id, id, before, before)
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
