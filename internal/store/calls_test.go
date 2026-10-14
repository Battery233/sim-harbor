package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim/internal/protocol"
)

func callEvent(kind protocol.Number, start int64) protocol.Event {
	zero, direction := protocol.Number(0), protocol.Number(1)
	return protocol.Event{DeviceID: "SIM-TEST", Type: kind, Slot: 1, Phone: "SYNTHETIC-CALLER", ICCID: "SYNTHETIC-CARD", CardName: "First card name", TelStartTS: protocol.Number(start), TelConnectedTS: &zero, TelDir: &direction}
}
func recordCall(t *testing.T, s *Store, e protocol.Event, notify bool) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Ingest(context.Background(), e, raw, notify); err != nil {
		t.Fatal(err)
	}
}

func TestIncomingCallMergeDurationIdentityAndReplay(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	start := time.Now().Unix()
	e := callEvent(601, start)
	recordCall(t, s, e, true)
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("ringing queued missed notification", err)
	}
	e.Type, e.TelEndTS, e.CardName = 603, protocol.Number(start+23), "Changed name"
	recordCall(t, s, e, true)
	e.MessageTS++ // A retry is not required to be byte-identical.
	e.Slot = 2
	recordCall(t, s, e, true)
	e.Type, e.TelEndTS = 601, 0
	recordCall(t, s, e, true)
	rows, err := s.Calls(ctx, "", "", 0, start+24)
	if err != nil || len(rows) != 1 || rows[0].State != "missed" || rows[0].RingSeconds == nil || *rows[0].RingSeconds != 23 || rows[0].Slot != 1 || rows[0].CardName != "First card name" || rows[0].CardID == "" {
		t.Fatal("call was split or downgraded", rows, err)
	}
	filtered, err := s.Calls(ctx, rows[0].CardID, "CALLER", 0, start+24)
	if err != nil || len(filtered) != 1 {
		t.Fatal("identity/search filter", err)
	}
	n, err := s.NextNotification(ctx)
	if err != nil || n.CallID != rows[0].ID || n.MessageID != 0 || n.RingSeconds != 23 || n.CardName != "First card name" {
		t.Fatal("wrong frozen call notification", err)
	}
	if err = s.NotificationResultCodeAt(ctx, n, false, "transport_failed", start+24); err != nil {
		t.Fatal(err)
	}
	if _, err = s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failure immediately retried", err)
	}
	s.NotificationResultCodeAt(ctx, n, true, "accepted", start+25)
	s.NotificationResultCodeAt(ctx, n, false, "transport_failed", start+26)
	s.NotificationResultCodeAt(ctx, n, true, "accepted", start+100)
	stats, err := s.NotificationSuccess(ctx, time.Unix(start+110, 0), time.UTC)
	if err != nil || stats.Today != 1 || stats.Last30Days != 1 {
		t.Fatal("call acceptance counted repeatedly", stats, err)
	}
	got, _ := s.Call(ctx, rows[0].ID, start+110)
	if got.AcceptedAt != start+25 || got.NotificationState != "sent" {
		t.Fatal("first acceptance changed")
	}
}

func TestIncomingCallAnsweredMissingAndContradictoryReports(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	start := time.Now().Unix()
	e := callEvent(603, start)
	connected := protocol.Number(start + 5)
	e.TelConnectedTS, e.TelEndTS = &connected, protocol.Number(start+20)
	recordCall(t, s, e, true) // A complete final report does not need 601.
	e.Type, e.TelEndTS = 602, 0
	recordCall(t, s, e, true)
	got, _ := s.Call(ctx, 1, start+21)
	if got.State != "answered" || got.RingSeconds == nil || *got.RingSeconds != 5 {
		t.Fatal("answered call downgraded or ringing/connected durations confused")
	}
	missing := callEvent(603, start+100)
	missing.ICCID, missing.CardName, missing.TelConnectedTS, missing.TelEndTS = "", "", nil, protocol.Number(start+123)
	recordCall(t, s, missing, true)
	got, _ = s.Call(ctx, 2, start+124)
	if got.State != "unknown" || got.RingSeconds != nil || got.CardID != "" {
		t.Fatal("missing identity/connection guessed")
	}
	missing = callEvent(603, 0)
	recordCall(t, s, missing, true)
	got, _ = s.Call(ctx, 3, start)
	if got.State != "unknown" || got.RingSeconds != nil || got.ReceivedAt <= 0 {
		t.Fatal("missing times guessed")
	}
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("non-missed queued", err)
	}
	missed := callEvent(603, start+200)
	missed.TelEndTS = protocol.Number(start + 223)
	recordCall(t, s, missed, true)
	missed.Type, missed.TelConnectedTS = 602, &connected
	recordCall(t, s, missed, true)
	got, _ = s.Call(ctx, 4, start+224)
	if got.State != "unknown" || got.NotificationState != "canceled" {
		t.Fatal("contradictory evidence not held")
	}
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("contradictory report pushed", err)
	}
}

func TestUnfinishedCallRestartAndDirection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	start := time.Now().Unix()
	e := callEvent(601, start)
	recordCall(t, s, e, true)
	old, _ := s.Call(context.Background(), 1, start+301)
	if old.State != "unknown" || old.RingSeconds != nil {
		t.Fatal("missing end inferred as finished")
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	got, _ := s.Call(context.Background(), 1, start+1)
	if got.State != "unknown" {
		t.Fatal("restart claimed call still ringing")
	}
	e.Type, e.TelEndTS = 603, protocol.Number(start+23)
	recordCall(t, s, e, true)
	got, _ = s.Call(context.Background(), 1, start+24)
	if got.State != "missed" {
		t.Fatal("late valid end could not complete call")
	}
	outboundDirection := protocol.Number(0)
	e.TelStartTS++
	e.TelDir = &outboundDirection
	recordCall(t, s, e, true)
	rows, _ := s.Calls(context.Background(), "", "", 0, start+24)
	if len(rows) != 1 {
		t.Fatal("historical direction interpretation mixed with real-time")
	}
}

func TestCallReplayDoesNotBackfillDisabledPushes(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	start := time.Now().Unix()
	e := callEvent(603, start)
	e.TelEndTS = protocol.Number(start + 23)
	recordCall(t, s, e, false)
	e.MessageTS++
	recordCall(t, s, e, true)
	if _, err := s.NextNotification(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("enabling Bark backfilled old call on replay", err)
	}
}
