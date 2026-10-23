package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordingReferencesAreSanitizedAndFilterable(t *testing.T) {
	r := New("info", 100, nil)
	ref := Reference("SYNTHETIC-CALL-ONE")
	r.Record("call_report", Fields{CallReference: ref, Slot: 1, Type: 602})
	r.Record("voicemail_stored", Fields{CallReference: "SYNTHETIC-PRIVATE-NUMBER", MediaReference: "https://example.invalid/private-token"})
	rows, _ := r.Snapshot(Filter{Category: "call", Reference: ref}, 10)
	if len(rows) != 1 || rows[0].Type != 602 {
		t.Fatal("call filter lost correlation", rows)
	}
	all, _ := r.Snapshot(Filter{}, 100)
	data, _ := json.Marshal(all)
	if strings.Contains(string(data), "SYNTHETIC-PRIVATE") || strings.Contains(string(data), "private-token") {
		t.Fatal("unsafe reference survived sanitization")
	}
}
