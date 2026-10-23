package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestRecordingDuringMissingPushPreservesAcceptanceAndLatePhase(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{true: "accepted", false: "failed"}[accepted], func(t *testing.T) {
			dir := t.TempDir()
			s := open(t, filepath.Join(dir, "sim.db"))
			ctx := t.Context()
			start := time.Now().Unix() - 20
			id, due := completedVoicemailCall(t, s, start, "SYNTHETIC-CALLER")
			if err := s.QueueVoicemailTimeouts(ctx, due, 1); err != nil {
				t.Fatal(err)
			}
			n := takeCallNote(t, s, "voicemail_missing")
			if ok, err := s.ReserveCallNotification(ctx, n, due); err != nil || !ok {
				t.Fatal("reserve", err)
			}
			if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER", start, due+1); err != nil {
				t.Fatal(err)
			}
			if err := s.NotificationResultCodeAt(ctx, n, accepted, "transport_failed", due+2); err != nil {
				t.Fatal(err)
			}
			var state string
			var at int64
			if err := s.DB.QueryRow(`SELECT state,accepted_at FROM call_notifications WHERE id=?`, n.ID).Scan(&state, &at); err != nil {
				t.Fatal(err)
			}
			if accepted && (state != "sent" || at != due+2) || !accepted && (state != "canceled" || at != 0) {
				t.Fatal("in-flight evidence", state, at)
			}
			late := takeCallNote(t, s, "voicemail_late")
			if late.CallID != id || notificationCount(t, s, id) != 2 {
				t.Fatal("late phase missing or duplicated")
			}
		})
	}
}

func TestAttemptedMissedPushIsNotRelabeledWhenAudioArrives(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	ctx := t.Context()
	start := time.Now().Unix() - 20
	e := callEvent(603, start)
	e.TelEndTS = protocol.Number(start + 10)
	recordCall(t, s, e, true)
	n := takeCallNote(t, s, "missed")
	if ok, err := s.ReserveCallNotification(ctx, n, start+12); err != nil || !ok {
		t.Fatal(err)
	}
	if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER", start, start+13); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := s.DB.QueryRow(`SELECT kind FROM call_notifications WHERE id=?`, n.ID).Scan(&kind); err != nil || kind != "missed" {
		t.Fatal("rewrote already dispatched payload", kind, err)
	}
}

func TestContradictoryCallReportDuringPushPreservesAcceptedResult(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{true: "accepted", false: "failed"}[accepted], func(t *testing.T) {
			s := open(t, filepath.Join(t.TempDir(), "sim.db"))
			ctx := t.Context()
			start := time.Now().Unix() - 20
			e := callEvent(603, start)
			e.TelEndTS = protocol.Number(start + 10)
			recordCall(t, s, e, true)
			n := takeCallNote(t, s, "missed")
			if ok, err := s.ReserveCallNotification(ctx, n, start+12); err != nil || !ok {
				t.Fatal("reserve", err)
			}
			e.Type = 602
			connected := protocol.Number(start + 5)
			e.TelConnectedTS = &connected
			recordCall(t, s, e, true)
			if err := s.NotificationResultCodeAt(ctx, n, accepted, "transport_failed", start+14); err != nil {
				t.Fatal(err)
			}
			var state string
			var at int64
			if err := s.DB.QueryRow(`SELECT state,accepted_at FROM call_notifications WHERE id=?`, n.ID).Scan(&state, &at); err != nil {
				t.Fatal(err)
			}
			if accepted && (state != "sent" || at != start+14) || !accepted && (state != "canceled" || at != 0) {
				t.Fatal("contradictory report lost in-flight evidence or revived obsolete task", state, at)
			}
		})
	}
}
