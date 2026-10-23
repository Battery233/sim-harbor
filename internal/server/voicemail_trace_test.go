package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestRecordingTraceSeparatesCallsUnscopedReportsAndPrivateData(t *testing.T) {
	const access = "SYNTHETIC-TRACE-MANAGEMENT-ACCESS"
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = access })
	ctx := t.Context()
	connected := protocol.Number(1002)
	e := protocol.Event{DeviceID: s.Config.DeviceID, Type: 603, Slot: 1, Phone: "SYNTHETIC-CALLER-ONE", TelStartTS: 1000, TelConnectedTS: &connected, TelEndTS: 1010}
	sess := &session{diagnosticID: diagnostics.Reference("SYNTHETIC-SESSION")}
	for i := 0; i < 2; i++ {
		if i == 1 {
			e.Phone = "SYNTHETIC-CALLER-TWO"
		}
		raw, _ := json.Marshal(e)
		if err := s.Store.Ingest(ctx, e, raw, true, 1); err != nil {
			t.Fatal(err)
		}
		s.recordProtocolEvent(ctx, sess, e, raw)
		audio := e
		audio.Type = 695
		s.recordCallProgress(ctx, sess, audio, nil)
	}
	// This event contains no call identity; it must not appear under either call.
	s.recordCallProgress(ctx, sess, protocol.Event{DeviceID: s.Config.DeviceID, Type: 696, Slot: 1}, nil)
	media, _, _, err := s.Store.SaveVoicemail(ctx, s.Config.DataDir, []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER-ONE", 1000, 1011)
	if err != nil {
		t.Fatal(err)
	}
	s.recordRecordingStored(ctx, media, 1, false)
	for id := 1; id <= 2; id++ {
		path := "http://example.com/api/voicemails/trace/" + string(rune('0'+id))
		if w := request(s, "GET", path, "", ""); w.Code != 401 {
			t.Fatal("trace lacks auth", w.Code)
		}
		w := request(s, "GET", path, "Bearer "+access, "")
		var out struct {
			Call   store.CallTrace     `json:"call"`
			Events []diagnostics.Event `json:"events"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Call.CallID != int64(id) {
			t.Fatal(w.Code)
		}
		if id == 1 && out.Call.Recording != "available" || id == 2 && out.Call.Recording != "unlinked" {
			t.Fatal(out.Call.Recording)
		}
		for _, event := range out.Events {
			if event.Code == "voicemail_upload_failed" {
				t.Fatal("unscoped report attached to a call")
			}
		}
		if len(out.Events) < 2 {
			t.Fatal("call/upload evidence missing")
		}
		for _, private := range []string{media, e.Phone, "SYNTHETIC-CALLER-ONE", access, "sha256", "stored_name"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("trace exposed private data")
			}
		}
	}
	s.Logs = diagnostics.New("error", 100, nil)
	if w := request(s, "GET", "http://example.com/api/voicemails/trace/1", "Bearer "+access, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"events":[]`) {
		t.Fatal("missing diagnostics changed business state", w.Code)
	}
}

func TestRecordingRejectionsAreBoundedAndDoNotTrustUploadIdentity(t *testing.T) {
	token := strings.Repeat("a", 32)
	s, _ := fixture(t, 0, func(c *config.Config) { enableVoicemail(c, token, 1) })
	for i := 0; i < 20; i++ {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/voicemail/amr/"+strings.Repeat("b", 32)+"?slot=1&start=1000&phone=SYNTHETIC-PRIVATE", bytes.NewBufferString("#!AMR\nSYNTHETIC"))
		r.RemoteAddr = "127.0.0.1:9"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	rows, _ := s.Logs.Snapshot(diagnostics.Filter{Category: "call"}, 100)
	if len(rows) != 1 || rows[0].Outcome != "token_rejected" || rows[0].CallReference != "" || rows[0].Slot != 0 {
		t.Fatal(rows)
	}
	p, _ := json.Marshal(rows)
	if bytes.Contains(p, []byte(token)) || bytes.Contains(p, []byte("SYNTHETIC-PRIVATE")) {
		t.Fatal("rejected request leaked identity")
	}
	if w := request(s, "GET", "http://127.0.0.1:8080/api/voicemails/storage", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"index_count":0`) {
		t.Fatal("rejected upload consumed recording quota", w.Code)
	}
}

func TestRecordingTraceRetainsLateAssociationEvidenceAndChecksFileState(t *testing.T) {
	s, _ := fixture(t, 0)
	ctx := t.Context()
	media, _, _, err := s.Store.SaveVoicemail(ctx, s.Config.DataDir, []byte("#!AMR\nSYNTHETIC-LATE"), 1, "SYNTHETIC-CALLER", 1000, 1011)
	if err != nil {
		t.Fatal(err)
	}
	// The upload predates the call report, so its diagnostic has only a media ref.
	s.recordRecordingStored(ctx, media, 1, false)
	connected := protocol.Number(1002)
	e := protocol.Event{DeviceID: s.Config.DeviceID, Type: 603, Slot: 1, Phone: "SYNTHETIC-CALLER", TelStartTS: 1000, TelConnectedTS: &connected, TelEndTS: 1010}
	raw, _ := json.Marshal(e)
	if err = s.Store.Ingest(ctx, e, raw, true, 1); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		w := request(s, "GET", "http://example.com/api/voicemails/trace/1", "", "")
		var out struct {
			Call   store.CallTrace     `json:"call"`
			Events []diagnostics.Event `json:"events"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Call.Recording != want {
			t.Fatal("wrong file state", w.Code, out.Call.Recording)
		}
		if len(out.Events) != 1 || out.Events[0].Code != "voicemail_stored" || out.Events[0].CallReference != "" {
			t.Fatal("late-associated recording lost original evidence", out.Events)
		}
	}
	check("available")
	if err = os.Remove(filepath.Join(s.Config.DataDir, "voicemail", media+".amr")); err != nil {
		t.Fatal(err)
	}
	check("missing")
	if err = s.Store.DeleteVoicemail(ctx, s.Config.DataDir, media); err != nil {
		t.Fatal(err)
	}
	check("deleted")
}
