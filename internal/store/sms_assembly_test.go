package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

func multipartEvent() protocol.Event {
	e := sms()
	no := false
	e.ICCID, e.Group, e.Max, e.Body = "SYNTHETIC-CARD", "SYNTHETIC-GROUP", 2, ""
	e.Complete, e.Fragments = &no, []string{"1,A"}
	return e
}

func TestSMSAssemblyOutOfOrderPersistsReceiptKeysAndDeletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := multipartEvent()
	e.Fragments, e.CardName = []string{"2,B"}, "Original"
	ingest(t, s, e)
	first := e
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	e.SMSTS += 6
	e.Slot, e.CardName, e.Fragments = 2, "Changed", []string{"1,A"}
	ingest(t, s, e)
	m, err := s.Message(ctx, 1)
	if err != nil || !m.Complete || m.Body != "AB" || m.Timestamp != int64(first.SMSTS) || m.Slot != 1 || m.CardName != "Original" || count(t, s, "messages") != 1 || count(t, s, "notifications") != 1 {
		t.Fatal("out-of-order assembly changed receipt ownership or duplicated", err)
	}
	for _, report := range []protocol.Event{first, e} {
		id, complete, err := s.SMSProgress(ctx, report)
		if err != nil || id != 1 || !complete {
			t.Fatal("diagnostics did not resolve the accepted timestamp", err)
		}
	}
	n, err := s.NextNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.NotificationResult(ctx, n, true); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	ingest(t, s, first)
	ingest(t, s, e)
	if count(t, s, "messages") != 1 || count(t, s, "notifications") != 1 || count(t, s, "sms_receipts") != 2 {
		t.Fatal("accepted reports duplicated after restart")
	}
	if err = s.DeleteMessage(ctx, 1, 1700001000); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, first)
	ingest(t, s, e)
	rows, err := s.Messages(ctx, 0, "")
	if err != nil || len(rows) != 0 || count(t, s, "messages") != 1 || count(t, s, "notifications") != 1 {
		t.Fatal("an accepted timestamp resurrected deleted history", err)
	}
	if _, _, err = s.SMSProgress(ctx, e); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("diagnostics exposed deleted SMS", err)
	}
}

func TestSMSAssemblySeparatesUnsafeCandidates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*protocol.Event)
	}{
		{"different group", func(e *protocol.Event) { e.Group = "OTHER-GROUP" }},
		{"different SIM", func(e *protocol.Event) { e.ICCID = "OTHER-CARD" }},
		{"different sender", func(e *protocol.Event) { e.Phone = "OTHER-SENDER" }},
		{"different device", func(e *protocol.Event) { e.DeviceID = "OTHER-BOARD" }},
		{"unknown identity", func(e *protocol.Event) { e.ICCID = "" }},
		{"different total", func(e *protocol.Event) { e.Max = 3 }},
		{"outside window", func(e *protocol.Event) { e.SMSTS += protocol.Number(smsAssemblyWindow) }},
		{"overlapping content conflict", func(e *protocol.Event) { e.Fragments = []string{"1,OTHER", "2,B"} }},
		{"aggregate without indexed pieces", func(e *protocol.Event) { yes := true; e.Complete = &yes; e.Body = "AB"; e.Fragments = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t, filepath.Join(t.TempDir(), "sim.db"))
			e := multipartEvent()
			ingest(t, s, e)
			e.SMSTS++
			e.Fragments = []string{"2,B"}
			tc.change(&e)
			ingest(t, s, e)
			m, err := s.Message(t.Context(), 1)
			if err != nil || m.Complete || m.Body != "A" || count(t, s, "messages") != 2 {
				t.Fatal("unsafe candidate modified earlier SMS", err)
			}
		})
	}
	// Two reports with an unknown SIM must stay separate even in the same slot.
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := multipartEvent()
	e.ICCID = ""
	ingest(t, s, e)
	e.SMSTS += 6
	e.Fragments = []string{"2,B"}
	ingest(t, s, e)
	if count(t, s, "messages") != 2 {
		t.Fatal("slot was used as proof of SIM identity")
	}
}

func TestSMSAssemblyWindowBoundsWholeGroup(t *testing.T) {
	for _, order := range [][]int64{{0, 120, -1}, {0, -120, 1}, {0, 120, 121}} {
		s := open(t, filepath.Join(t.TempDir(), "sim.db"))
		e := multipartEvent()
		e.Max = 3
		start := e.SMSTS
		for i, delta := range order {
			e.SMSTS = start + protocol.Number(delta)
			e.Fragments = []string{[]string{"1,A", "2,B", "3,C"}[i]}
			ingest(t, s, e)
		}
		m, err := s.Message(t.Context(), 1)
		if err != nil || m.Complete || m.PartsReceived != 2 || count(t, s, "messages") != 2 || count(t, s, "notifications") != 0 {
			t.Fatal("timestamp window expanded by chaining fragments", err)
		}
	}
}

func TestSMSAssemblyRefusesMultipleCandidatesAndCompletedGroups(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := multipartEvent()
	ingest(t, s, e)
	// Same group reference and nearby timestamp, but a conflicting first part:
	// this starts an independent candidate rather than modifying the first.
	e.SMSTS += 2
	e.Fragments = []string{"1,X"}
	ingest(t, s, e)
	e.SMSTS += 2
	e.Fragments = []string{"2,B"}
	ingest(t, s, e)
	if count(t, s, "messages") != 3 || count(t, s, "notifications") != 0 {
		t.Fatal("selected one of multiple plausible groups")
	}
	s = open(t, filepath.Join(t.TempDir(), "sim.db"))
	e = multipartEvent()
	e.Fragments = []string{"1,A", "2,B"}
	ingest(t, s, e)
	e.SMSTS += 6
	e.Fragments = []string{"1,NEW"}
	ingest(t, s, e)
	m, err := s.Message(t.Context(), 1)
	if err != nil || m.Body != "AB" || !m.Complete || count(t, s, "messages") != 2 || count(t, s, "notifications") != 1 {
		t.Fatal("completed SMS consumed a new message", err)
	}
}

func TestSMSAssemblyDoesNotGuessLegacyOrMergeExistingRows(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := multipartEvent()
	ingest(t, s, e)
	if _, err := s.DB.Exec(`UPDATE messages SET sms_total=0,sms_group_ref=''; DELETE FROM sms_receipts`); err != nil {
		t.Fatal(err)
	}
	e.SMSTS += 6
	e.Fragments = []string{"2,B"}
	ingest(t, s, e)
	if count(t, s, "messages") != 2 || count(t, s, "notifications") != 0 {
		t.Fatal("guessed legacy grouping from card/sender/time alone")
	}
	// A replay that exactly matches each historical row must retain both rows.
	ingest(t, s, e)
	e.SMSTS -= 6
	e.Fragments = []string{"1,A"}
	ingest(t, s, e)
	if count(t, s, "messages") != 2 || count(t, s, "notifications") != 0 {
		t.Fatal("exact replay merged existing historical rows")
	}
}

func TestSMSAssemblyInvalidReportRollsBackReceiptAndFragments(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := multipartEvent()
	ingest(t, s, e)
	e.SMSTS += 6
	e.Fragments = []string{"2,B", "02,OTHER"}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(t.Context(), e, raw, true); !errors.Is(err, ErrSMSFragmentConflict) {
		t.Fatal("conflicting duplicate index accepted", err)
	}
	if count(t, s, "sms_receipts") != 1 || count(t, s, "messages") != 1 || count(t, s, "events") != 1 {
		t.Fatal("rejected report persisted partial state")
	}
}

func TestSMSAssemblyAcceptedTimestampStillRejectsConflictingReplay(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := multipartEvent()
	e.Max = 3
	ingest(t, s, e)
	e.SMSTS += 6
	e.Fragments = []string{"2,B"}
	ingest(t, s, e)
	e.Fragments = []string{"2,CHANGED"}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(t.Context(), e, raw, true); !errors.Is(err, ErrSMSFragmentConflict) {
		t.Fatal("accepted timestamp bypassed content conflict checks", err)
	}
	e.Fragments, e.Max = []string{"3,C"}, 4
	raw, _ = json.Marshal(e)
	if err := s.Ingest(t.Context(), e, raw, true); !errors.Is(err, ErrSMSTotalMismatch) {
		t.Fatal("accepted timestamp bypassed total consistency checks", err)
	}
	m, err := s.Message(t.Context(), 1)
	if err != nil || m.Complete || m.PartsReceived != 2 || count(t, s, "sms_receipts") != 2 || count(t, s, "events") != 2 || count(t, s, "notifications") != 0 {
		t.Fatal("conflicting replay changed saved state", err)
	}
}

func TestSMSAssemblyRepeatedPartAtAnotherTimestampDoesNotCountTwice(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	e := multipartEvent()
	ingest(t, s, e)
	e.SMSTS += 6
	ingest(t, s, e)
	m, err := s.Message(t.Context(), 1)
	if err != nil || m.Complete || m.PartsReceived != 1 || count(t, s, "messages") != 1 || count(t, s, "notifications") != 0 {
		t.Fatal("same part at another timestamp falsely completed SMS", err)
	}
	e.SMSTS += 6
	e.Fragments = []string{"2,B"}
	ingest(t, s, e)
	m, err = s.Message(t.Context(), 1)
	if err != nil || !m.Complete || m.Body != "AB" || count(t, s, "sms_receipts") != 3 || count(t, s, "notifications") != 1 {
		t.Fatal("missing part could not finish the deduplicated assembly", err)
	}
}

func TestVersionSixteenUpgradeAddsNoGuessedReceiptLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	e := multipartEvent()
	ingest(t, s, e)
	if _, err := s.DB.Exec(`DROP TABLE sms_receipts; DELETE FROM schema_version; INSERT INTO schema_version VALUES(16)`); err != nil {
		t.Fatal(err)
	}
	before, err := s.Message(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	after, err := s.Message(t.Context(), 1)
	if err != nil || before.Body != after.Body || before.Timestamp != after.Timestamp || before.Complete != after.Complete || count(t, s, "sms_receipts") != 0 || count(t, s, "notifications") != 0 {
		t.Fatal("migration rewrote history or invented receipt links", err)
	}
	e.SMSTS += 6
	e.Fragments = []string{"2,B"}
	ingest(t, s, e)
	after, err = s.Message(t.Context(), 1)
	if err != nil || !after.Complete || after.Body != "AB" || count(t, s, "messages") != 1 {
		t.Fatal("genuine follow-up could not continue a schema-16 partial", err)
	}
}
