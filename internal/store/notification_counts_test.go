package store

import (
	"context"
	"path/filepath"
	"testing"
)

func BenchmarkNotificationPendingCounters(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "sim.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.DB.Close()
	_, err = s.DB.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000)
 INSERT INTO messages(key,slot,phone,body,sms_ts,complete) SELECT CAST(x AS TEXT),1,'SYNTHETIC','SYNTHETIC',x,1 FROM n;
 INSERT INTO notifications(message_id,state,attempts) SELECT id,CASE WHEN id%4=0 THEN 'pending' ELSE 'sent' END,id%2 FROM messages;`)
	if err != nil {
		b.Fatal(err)
	}
	queries := []struct{ name, sql string }{
		{"history_projection", notificationRecordsProjection("everything") + `SELECT count(*),coalesce(sum(attempts>0),0) FROM records WHERE state='pending'`},
		{"direct_aggregates", notificationCountsSQL("everything", true)},
	}
	for _, q := range queries {
		b.Run(q.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				var pending, retry int
				if err := s.DB.QueryRowContext(context.Background(), q.sql).Scan(&pending, &retry); err != nil || pending != 2500 || retry != 0 {
					b.Fatal(pending, retry, err)
				}
			}
		})
	}
}
