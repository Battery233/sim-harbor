package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestNavigationBadgesAndPlaybackSyncAcrossClients(t *testing.T) {
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = "SYNTHETIC-NAVIGATION-TOKEN" })
	ctx := t.Context()
	e := protocol.Event{DeviceID: s.Config.DeviceID, Type: 501, Slot: 1, Phone: "SYNTHETIC-SENDER", Body: "SYNTHETIC", SMSTS: 1000}
	raw, _ := json.Marshal(e)
	if err := s.Store.Ingest(ctx, e, raw, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO calls(key,device_id,slot,phone,start_ts,state,received_at) VALUES('NAV','SIM-TEST',1,'SYNTHETIC-CALLER',1000,'ringing',1000)`); err != nil {
		t.Fatal(err)
	}
	media, _, _, err := s.Store.SaveVoicemail(ctx, s.Config.DataDir, []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER", 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	// A missing file or an unlinked file must not produce an unreachable call badge.
	missing, _, _, err := s.Store.SaveVoicemail(ctx, s.Config.DataDir, []byte("#!AMR\nMISSING"), 0, "", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(s.Config.DataDir, "voicemail", missing+".amr"))
	if w := backupAPI(s, "GET", "/navigation-counts", "", ""); w.Code != 401 {
		t.Fatal("badge auth", w.Code)
	}
	counts := func(app *Server) store.NavigationCounts {
		t.Helper()
		w := backupAPI(app, "GET", "/navigation-counts", "", s.Config.AdminToken)
		var result store.NavigationCounts
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return result
	}
	if c := counts(s); c.UnreadMessages != 1 || c.UnplayedRecordings != 1 {
		t.Fatal(c)
	}
	if w := backupAPI(s, "GET", "/calls/1/voicemail", "", s.Config.AdminToken); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if c := counts(s); c.UnplayedRecordings != 1 {
		t.Fatal("download marked played", c)
	}
	path := "/voicemails/" + media + "/played"
	if w := backupAPI(s, "POST", path, `{}`, ""); w.Code != 401 {
		t.Fatal("play auth", w.Code)
	}
	r := httptest.NewRequest("POST", "http://example.com/api"+path, nil)
	r.Header.Set("Authorization", "Bearer "+s.Config.AdminToken)
	r.Header.Set("Origin", "https://other.invalid")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross origin mark", w.Code)
	}
	w = backupAPI(s, "POST", path, `{}`, s.Config.AdminToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var stamp struct {
		PlayedAt int64 `json:"played_at"`
	}
	json.Unmarshal(w.Body.Bytes(), &stamp)
	if stamp.PlayedAt <= 0 {
		t.Fatal("no persisted stamp")
	}
	other := New(s.Config, s.Store)
	defer other.CloseBackups()
	if c := counts(other); c.UnplayedRecordings != 0 || c.UnreadMessages != 1 {
		t.Fatal("other client not synced", c)
	}
	c, err := s.Store.Call(ctx, 1, 2000)
	if err != nil || c.VoicemailPlayedAt != stamp.PlayedAt {
		t.Fatal(c, err)
	}
	groups, err := s.Store.CallGroups(ctx, "", "", 0, 2000)
	if err != nil || groups[0].UnplayedRecordings != 0 {
		t.Fatal(groups, err)
	}
	w = backupAPI(other, "POST", path, `{}`, s.Config.AdminToken)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var again struct {
		PlayedAt int64 `json:"played_at"`
	}
	json.Unmarshal(w.Body.Bytes(), &again)
	if again.PlayedAt != stamp.PlayedAt {
		t.Fatal("repeat reset timestamp")
	}
	if err = s.Store.Read(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if c := counts(other); c.UnreadMessages != 0 {
		t.Fatal(c)
	}
	if err = s.Store.DeleteVoicemail(ctx, s.Config.DataDir, media); err != nil {
		t.Fatal(err)
	}
	if w = backupAPI(other, "POST", path, `{}`, s.Config.AdminToken); w.Code != 404 {
		t.Fatal("deleted mark", w.Code, fmt.Sprint(w.Body))
	}
}
