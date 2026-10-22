package store

import (
	"context"
	"time"
)

const notificationStatsSchema = `ALTER TABLE notifications ADD COLUMN accepted_at INTEGER NOT NULL DEFAULT 0 CHECK(accepted_at>=0);
CREATE INDEX notifications_success_time ON notifications(state,accepted_at);
DELETE FROM schema_version; INSERT INTO schema_version VALUES(8);`

type NotificationSuccessStats struct {
	Today      int    `json:"today"`
	Last30Days int    `json:"last_30_days"`
	Undated    int    `json:"undated"`
	Timezone   string `json:"timezone"`
}

// Count all persisted push tasks once, by first Bark acceptance, never by event time
// or next retry time. Calendar dates use the configured zone (including DST).
func (s *Store) NotificationSuccess(ctx context.Context, now time.Time, loc *time.Location) (NotificationSuccessStats, error) {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	start := today.AddDate(0, 0, -29)
	result := NotificationSuccessStats{Timezone: loc.String()}
	err := s.DB.QueryRowContext(ctx, notificationRecordsCTE+`SELECT
 (SELECT count(*) FROM records WHERE state='sent' AND accepted_at>=? AND accepted_at<=?),
 (SELECT count(*) FROM records WHERE state='sent' AND accepted_at>=? AND accepted_at<=?),
 (SELECT count(*) FROM records WHERE state='sent' AND accepted_at=0)`, today.Unix(), now.Unix(), start.Unix(), now.Unix()).Scan(&result.Today, &result.Last30Days, &result.Undated)
	return result, err
}
