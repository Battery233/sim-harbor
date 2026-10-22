package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

func completedVoicemailCall(t *testing.T, s *Store, start int64, phone string) (int64, int64) {
	t.Helper()
	e := callEvent(601, start)
	e.Phone = phone
	ingest := func() {
		t.Helper()
		raw, _ := json.Marshal(e)
		if err := s.Ingest(context.Background(), e, raw, true, 1); err != nil {
			t.Fatal(err)
		}
	}
	ingest()
	connected := protocol.Number(start + 2)
	e.Type = 602
	e.TelConnectedTS = &connected
	ingest()
	e.Type = 603
	e.TelEndTS = protocol.Number(start + 10)
	ingest()
	var id, due int64
	if err := s.DB.QueryRow(`SELECT id,voicemail_due_at FROM calls WHERE start_ts=? AND phone=?`, start, phone).Scan(&id, &due); err != nil {
		t.Fatal(err)
	}
	return id, due
}
func takeCallNote(t *testing.T, s *Store, kind string) Notification {
	t.Helper()
	n, err := s.NextNotification(context.Background())
	if err != nil || n.CallKind != kind {
		t.Fatalf("wanted %s, got %s: %v", kind, n.CallKind, err)
	}
	return n
}
func acceptCallNote(t *testing.T, s *Store, n Notification, now int64) {
	t.Helper()
	ctx := context.Background()
	if ok, err := s.ReserveCallNotification(ctx, n, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.NotificationResultCodeAt(ctx, n, true, "accepted", now); err != nil {
		t.Fatal(err)
	}
}

func TestVoicemailMissingAndLateOnceAcrossReportsAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	dir := t.TempDir()
	start := time.Now().Unix() - 20
	id, due := completedVoicemailCall(t, s, start, "anonymous")
	if delta := due - time.Now().Unix(); delta < 179 || delta > 180 {
		t.Fatal("deadline used board time", delta)
	}
	if err := s.QueueVoicemailTimeouts(ctx, due-1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("early reminder", err)
	}
	// Replayed reports do not reset a persisted deadline.
	_, again := completedVoicemailCall(t, s, start, "anonymous")
	if again != due {
		t.Fatal("deadline reset")
	}
	for i := 0; i < 2; i++ {
		if err := s.QueueVoicemailTimeouts(ctx, due, 1); err != nil {
			t.Fatal(err)
		}
	}
	missing := takeCallNote(t, s, "voicemail_missing")
	acceptCallNote(t, s, missing, due)
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	if err := s.QueueVoicemailTimeouts(ctx, due+3600, 1); err != nil {
		t.Fatal(err)
	}
	if notificationCount(t, s, id) != 1 {
		t.Fatal("restart duplicated missing reminder")
	}
	audio := []byte("#!AMR\nSYNTHETIC-LATE")
	media, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, "anonymous", start, due+3600)
	if err != nil {
		t.Fatal(err)
	}
	late := takeCallNote(t, s, "voicemail_late")
	if late.ID == missing.ID || late.CallID != id {
		t.Fatal("follow-up identity")
	}
	acceptCallNote(t, s, late, due+3601)
	if _, _, dup, err := s.SaveVoicemail(ctx, dir, audio, 1, "anonymous", start, due+3602); err != nil || !dup {
		t.Fatal(dup, err)
	}
	completedVoicemailCall(t, s, start, "anonymous")
	if notificationCount(t, s, id) != 2 {
		t.Fatal("duplicate follow-up")
	}
	c, err := s.Call(ctx, id, due+3602)
	if err != nil || c.NotificationKind != "voicemail_late" || c.NotificationState != "sent" || c.VoicemailMediaID != media {
		t.Fatal(c, err)
	}
}

func TestVoicemailBeforeMissingAttemptCollapsesToNormal(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	start := time.Now().Unix() - 20
	id, due := completedVoicemailCall(t, s, start, "SYNTHETIC-CALLER")
	if err := s.QueueVoicemailTimeouts(ctx, due, 1); err != nil {
		t.Fatal(err)
	}
	missing := takeCallNote(t, s, "voicemail_missing")
	if _, _, _, err := s.SaveVoicemail(ctx, t.TempDir(), []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER", start, due+1); err != nil {
		t.Fatal(err)
	}
	normal := takeCallNote(t, s, "voicemail")
	if normal.ID != missing.ID || notificationCount(t, s, id) != 1 {
		t.Fatal("unattempted missing was pushed separately")
	}
}

func TestVoicemailLostMissingResponseNeverRetriesStaleMessage(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	dir := t.TempDir()
	start := time.Now().Unix() - 20
	id, due := completedVoicemailCall(t, s, start, "SYNTHETIC-CALLER")
	s.QueueVoicemailTimeouts(ctx, due, 1)
	missing := takeCallNote(t, s, "voicemail_missing")
	if ok, err := s.ReserveCallNotification(ctx, missing, due); err != nil || !ok {
		t.Fatal(err)
	}
	if err := s.NotificationResultCodeAt(ctx, missing, false, "transport_failed", due); err != nil {
		t.Fatal(err)
	}
	media, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER", start, due+1)
	if err != nil {
		t.Fatal(err)
	}
	late := takeCallNote(t, s, "voicemail_late")
	var state, reason string
	s.DB.QueryRow(`SELECT state,last_error FROM call_notifications WHERE id=?`, missing.ID).Scan(&state, &reason)
	if state != "canceled" || reason != "voicemail_arrived" || notificationCount(t, s, id) != 2 {
		t.Fatal(state, reason)
	}
	if err = s.DeleteVoicemail(ctx, dir, media); err != nil {
		t.Fatal(err)
	}
	s.DB.QueryRow(`SELECT state,last_error FROM call_notifications WHERE id=?`, late.ID).Scan(&state, &reason)
	if state != "canceled" || reason != "voicemail_deleted" {
		t.Fatal(state, reason)
	}
	s.QueueVoicemailTimeouts(ctx, due+100, 1)
	if _, err = s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted audio triggered a reminder", err)
	}
}

func TestVoicemailWaitHonorsMuteDisableRestoreAndTimelyDeletion(t *testing.T) {
	for _, mode := range []string{"mute", "disabled", "restore", "timely-deleted"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := open(t, filepath.Join(t.TempDir(), "sim.db"))
			start := time.Now().Unix() - 20
			id, due := completedVoicemailCall(t, s, start, "SYNTHETIC-CALLER")
			c, err := s.Call(ctx, id, due)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "mute":
				if err = s.SetCardNotifications(ctx, c.CardID, false, true); err != nil {
					t.Fatal(err)
				}
				if err = s.SetCardNotifications(ctx, c.CardID, true, false); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if err = s.QueueVoicemailTimeouts(ctx, due); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err = s.HoldRestoredData(ctx, "SYNTHETIC", due-1); err != nil {
					t.Fatal(err)
				}
			case "timely-deleted":
				dir := t.TempDir()
				media, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER", start, due-1)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.DeleteVoicemail(ctx, dir, media); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.QueueVoicemailTimeouts(ctx, due+10, 1); err != nil {
				t.Fatal(err)
			}
			if _, err = s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("suppressed call notified", err)
			}
		})
	}
}

func TestSchemaTwentyPreservesOldJobsAndDoesNotArmHistoricalCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ctx := context.Background()
	missed := callEvent(603, 1000)
	missed.TelEndTS = 1002
	recordCall(t, s, missed, true)
	if _, err := s.DB.Exec(`UPDATE call_notifications SET state='sent',accepted_at=1200,attempts=2,last_error='';
 CREATE TABLE call_notifications_19 AS SELECT id,call_id,card_name,group_name,state,attempts,next_at,last_error,accepted_at,kind,attempted_at FROM call_notifications;
 DROP TABLE call_notifications;ALTER TABLE call_notifications_19 RENAME TO call_notifications;
 DROP INDEX calls_voicemail_due; ALTER TABLE calls DROP COLUMN voicemail_due_at;
 UPDATE schema_version SET version=19;`); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	var due, accepted int64
	var attempts, version int
	var state string
	if err := s.DB.QueryRow(`SELECT voicemail_due_at FROM calls WHERE id=1`).Scan(&due); err != nil || due != -1 {
		t.Fatal("historical call armed", due, err)
	}
	if err := s.DB.QueryRow(`SELECT state,attempts,accepted_at FROM call_notifications WHERE id=1 AND phase=0`).Scan(&state, &attempts, &accepted); err != nil || state != "sent" || attempts != 2 || accepted != 1200 {
		t.Fatal(state, attempts, accepted, err)
	}
	s.DB.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	if version != SchemaVersion {
		t.Fatal(version)
	}
	if err := s.QueueVoicemailTimeouts(ctx, 2000000000, 1); err != nil || notificationCount(t, s, 0) != 1 {
		t.Fatal("migration backfilled", err)
	}
}
