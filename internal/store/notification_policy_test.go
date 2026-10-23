package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUnconfiguredRecordingAssociationDoesNotQueueOrReplay(t *testing.T) {
	for _, path := range []string{"upload", "call", "restart", "duplicate"} {
		t.Run(path, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			database := filepath.Join(dir, "sim.db")
			s := open(t, database, OpenOptions{NotificationsEnabled: false})
			start := time.Now().Unix() - 20
			e := callEvent(601, start)
			audio := []byte("#!AMR\nSYNTHETIC-NO-BARK")
			if path == "upload" {
				recordCall(t, s, e, false)
			}
			media, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, e.Phone, start, start+10)
			if err != nil {
				t.Fatal(err)
			}
			if path == "call" {
				recordCall(t, s, e, false)
			} else if path != "upload" {
				// Model an existing call that was not linked before interruption.
				if _, err := s.DB.Exec(`INSERT INTO calls(key,device_id,slot,phone,start_ts,state,received_at) VALUES('SYNTHETIC','SIM-TEST',1,?,?,'ringing',?)`, e.Phone, start, start); err != nil {
					t.Fatal(err)
				}
				if path == "restart" {
					s.DB.Close()
					s = open(t, database, OpenOptions{NotificationsEnabled: false})
				} else if _, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, e.Phone, start, start+11); err != nil {
					t.Fatal(err)
				}
			}
			var callID int64
			if err := s.DB.QueryRow(`SELECT call_id FROM voicemails WHERE media_id=?`, media).Scan(&callID); err != nil || callID <= 0 {
				t.Fatal("recording was not associated", err)
			}
			if file, err := s.VoicemailFile(ctx, callID); err != nil || file.Name == "" || count(t, s, "call_notifications") != 0 {
				t.Fatal("unconfigured recording was lost or queued", err)
			}
			s.DB.Close()
			s = open(t, database, OpenOptions{NotificationsEnabled: true})
			if _, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, e.Phone, start, start+12); err != nil || count(t, s, "call_notifications") != 0 {
				t.Fatal("enabling Bark replayed associated history", err)
			}
			e.TelStartTS += 100
			recordCall(t, s, e, true)
			if _, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, e.Phone, start+100, start+110); err != nil || count(t, s, "call_notifications") != 1 {
				t.Fatal("new recording did not notify after enabling Bark", err)
			}
		})
	}
}

func TestUnconfiguredBarkPreservesExistingTasksWithoutNewLatePush(t *testing.T) {
	for _, phase := range []string{"pending", "attempted", "sent"} {
		t.Run(phase, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			database := filepath.Join(dir, "sim.db")
			s := open(t, database)
			start := time.Now().Unix() - 20
			_, due := completedVoicemailCall(t, s, start, "SYNTHETIC-CALLER")
			if err := s.QueueVoicemailTimeouts(ctx, due, 1); err != nil {
				t.Fatal(err)
			}
			n := takeCallNote(t, s, "voicemail_missing")
			if phase != "pending" {
				if ok, err := s.ReserveCallNotification(ctx, n, due); err != nil || !ok {
					t.Fatal(err)
				}
				if phase == "sent" {
					if err := s.NotificationResultCodeAt(ctx, n, true, "accepted", due+1); err != nil {
						t.Fatal(err)
					}
				}
			}
			ingest(t, s, sms())
			s.DB.Close()
			s = open(t, database, OpenOptions{NotificationsEnabled: false})
			if count(t, s, "call_notifications") != 1 || count(t, s, "notifications") != 1 {
				t.Fatal("opening without Bark cleared existing tasks")
			}
			if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nSYNTHETIC-LATE"), 1, "SYNTHETIC-CALLER", start, due+2); err != nil {
				t.Fatal(err)
			}
			state, kind := notificationState(t, s, n.CallID)
			if count(t, s, "call_notifications") != 1 || count(t, s, "notifications") != 1 ||
				phase == "pending" && (state != "pending" || kind != "voicemail") ||
				phase == "attempted" && state != "canceled" || phase == "sent" && state != "sent" {
				t.Fatal("existing task was lost, made inaccurate or duplicated", state, kind)
			}
		})
	}
}
