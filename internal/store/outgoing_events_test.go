package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"path/filepath"
	"testing"
)

func number(n int64) *protocol.Number { v := protocol.Number(n); return &v }
func submitted(o Outgoing, refs string) protocol.Event {
	return protocol.Event{DeviceID: o.DeviceID, Type: 502, TID: o.TID, Slot: protocol.Number(o.Slot), Phone: o.Phone, Body: o.Body, SentMR: json.RawMessage(refs)}
}
func receipt(o Outgoing, mr, status int, ts int64) protocol.Event {
	return protocol.Event{DeviceID: o.DeviceID, Type: 503, Slot: protocol.Number(o.Slot), Phone: o.Phone, SentMR: json.RawMessage(stringInt(mr)), SMSStatus: number(int64(status)), SCTS: protocol.Number(o.DispatchedAt), DeliveryTS: protocol.Number(ts)}
}
func stringInt(n int) string { b, _ := json.Marshal(n); return string(b) }
func claimed(t *testing.T, s *Store, n int, now int64) Outgoing {
	o := preview(t, s, n, 1, now)
	o, ok, e := s.ClaimOutgoing(context.Background(), o.ID, o.ICCID, now, limit(now))
	if e != nil || !ok {
		t.Fatal(e)
	}
	return o
}
func view(t *testing.T, s *Store, o Outgoing, now int64) Outgoing {
	t.Helper()
	v, e := s.Outgoing(context.Background(), o.ID, now)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestOutgoingResultsOutOfOrderAndAllSegments(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := int64(1700000000)
	o := claimed(t, s, 1, now)
	ingest(t, s, receipt(o, 7, 0, now+3))
	if v := view(t, s, o, now); v.Delivery != "unconfirmed" {
		t.Fatal(v)
	}
	ingest(t, s, protocol.Event{DeviceID: o.DeviceID, Type: 402, SubType: 16, Code: number(0), TID: o.TID, Val: "7,8"})
	if v := view(t, s, o, now); v.State != "processed" || v.Delivery == "delivered" {
		t.Fatal(v)
	}
	ingest(t, s, submitted(o, "[7,8]"))
	if v := view(t, s, o, now); v.State != "submitted" || v.Delivery != "partial" || v.DeliveryReceived != 1 {
		t.Fatal(v)
	}
	ingest(t, s, receipt(o, 8, 100, now+4))
	if v := view(t, s, o, now); v.Delivery != "delivering" {
		t.Fatal(v)
	}
	ingest(t, s, receipt(o, 8, 0, now+5))
	if v := view(t, s, o, now); v.Delivery != "delivered" || v.DeliveryReceived != 2 {
		t.Fatal(v)
	}
	// Older temporary receipt, late failure and write completion cannot regress stronger proof.
	ingest(t, s, receipt(o, 8, 100, now+2))
	ingest(t, s, protocol.Event{DeviceID: o.DeviceID, Type: 401, SubType: 16, Code: number(200), TID: o.TID})
	if e := s.MarkOutgoingWrite(context.Background(), o.ID, true, now); e != nil {
		t.Fatal(e)
	}
	if v := view(t, s, o, now+100); v.State != "submitted" || v.Delivery != "delivered" {
		t.Fatal(v)
	}
}
func TestOutgoingReceiptReuseAmbiguityAndMissingEvidence(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := int64(1700000000)
	a := claimed(t, s, 1, now)
	ingest(t, s, submitted(a, "[0]"))
	r := receipt(a, 0, 0, now+3)
	r.SCTS = 0
	ingest(t, s, r)
	if v := view(t, s, a, now); v.Delivery != "unconfirmed" {
		t.Fatal("missing time matched", v)
	}
	r.SCTS = protocol.Number(now)
	ingest(t, s, r)
	if v := view(t, s, a, now); v.Delivery != "delivered" {
		t.Fatal(v)
	}
	b := claimed(t, s, 2, now+61)
	ingest(t, s, submitted(b, "[0]"))
	for _, o := range []Outgoing{a, b} {
		if v := view(t, s, o, now+62); v.Delivery != "ambiguous" {
			t.Fatal("reused ID chose an operation", v)
		}
	}
}
func TestOutgoingMismatchedResultsAndUnknownTimeout(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := int64(1700000000)
	o := claimed(t, s, 1, now)
	e := submitted(o, "[7]")
	e.Phone = "001"
	ingest(t, s, e)
	if v := view(t, s, o, now); v.State != "unknown" {
		t.Fatal("wrong target accepted")
	}
	ingest(t, s, protocol.Event{DeviceID: o.DeviceID, Type: 402, SubType: 16, Code: number(200), TID: o.TID, Val: "timeout"})
	if v := view(t, s, o, now); v.State != "unknown" {
		t.Fatal("timeout treated as certain failure")
	}
	e = submitted(o, "[7]")
	ingest(t, s, e)
	r := receipt(o, 7, 64, now+3)
	r.ICCID = "DIFFERENT"
	ingest(t, s, r)
	if v := view(t, s, o, now); v.Delivery != "unconfirmed" {
		t.Fatal("wrong card receipt")
	}
	r.ICCID = o.ICCID
	ingest(t, s, r)
	if v := view(t, s, o, now); v.Delivery != "failed" || v.State != "submitted" {
		t.Fatal(v)
	}
	e.SentMR = json.RawMessage("[8]")
	ingest(t, s, e)
	if v := view(t, s, o, now); v.Delivery != "unconfirmed" || v.ErrorCode != "reference_conflict" {
		t.Fatal("contradictory references", v)
	}
}

func TestReferenceConflictSurvivesNegativeAndLaterResults(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	now := int64(1700000000)
	o := claimed(t, s, 1, now)
	for _, val := range []string{"7", "8"} {
		ingest(t, s, protocol.Event{DeviceID: o.DeviceID, Type: 402, SubType: 16, Code: number(0), TID: o.TID, Val: val})
	}
	ingest(t, s, protocol.Event{DeviceID: o.DeviceID, Type: 402, SubType: 16, Code: number(200), TID: o.TID, Val: "timeout"})
	ingest(t, s, submitted(o, "[7,8]"))
	ingest(t, s, receipt(o, 7, 0, now+3))
	ingest(t, s, receipt(o, 8, 0, now+3))
	if v := view(t, s, o, now); v.Delivery != "unconfirmed" || v.ErrorCode != "reference_conflict" {
		t.Fatal("contradictory evidence was erased", v)
	}
}

func TestTimeoutCannotBeClearedByLateAcceptance(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	o := claimed(t, s, 1, 1700000000)
	ingest(t, s, protocol.Event{DeviceID: o.DeviceID, Type: 402, SubType: 16, Code: number(200), TID: o.TID, Val: "timeout"})
	ingest(t, s, protocol.Event{DeviceID: o.DeviceID, Type: 401, SubType: 16, Code: number(0), TID: o.TID})
	if v := view(t, s, o, o.DispatchedAt); v.State != "unknown" || v.LastPhase != "accepted" {
		t.Fatal("late weaker evidence removed uncertainty", v)
	}
}

func TestOutgoingEvidenceUsesOneReadSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	now := int64(1700000000)
	o := claimed(t, s, 1, now)
	ingest(t, s, submitted(o, "[7]"))
	ingest(t, s, receipt(o, 7, 0, now+3))
	other := open(t, path)
	tx, err := s.DB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw, err := scanOutgoing(tx.QueryRow(`SELECT `+outgoingColumns+` FROM outgoing WHERE id=?`, o.ID))
	if err != nil {
		t.Fatal(err)
	}
	// A second connection introduces contradictory references during the read.
	ingest(t, other, submitted(o, "[8]"))
	ingest(t, other, receipt(o, 8, 0, now+4))
	v, err := outgoingView(context.Background(), tx, raw, now)
	if err != nil || v.Delivery != "delivered" || v.DeliveryExpected != 1 {
		t.Fatal("mixed read snapshots", v, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if v = view(t, s, o, now); v.ErrorCode != "reference_conflict" || v.Delivery != "unconfirmed" {
		t.Fatal("new snapshot missed conflict", v)
	}
}
