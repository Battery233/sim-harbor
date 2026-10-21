package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Battery233/sim-harbor/internal/config"
)

func TestUnlinkedVoicemailManagementAuthAndDownloadWhileDisabled(t *testing.T) {
	const token = "SYNTHETIC-MANAGEMENT-ACCESS-TOKEN"
	s, _ := fixture(t, 1, func(c *config.Config) {
		c.AdminToken, c.AdminTokenHeader, c.Environment = token, "X-SIMHarbor-Authorization", "cloud"
	})
	audio := []byte("#!AMR\nSYNTHETIC-AUDIO")
	if _, _, _, err := s.Store.SaveVoicemail(context.Background(), s.Config.DataDir, audio, 0, "TEST-CALLER", 0, 1000); err != nil {
		t.Fatal(err)
	}
	authRequest := func(path, header, value, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://example.invalid"+path, nil)
		if header != "" {
			r.Header.Set(header, value)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/voicemails/unlinked", "/api/voicemails/unlinked/1/audio"} {
		if w := authRequest(path, "", "", ""); w.Code != 401 {
			t.Fatal("anonymous access", w.Code)
		}
		if w := authRequest(path, "Authorization", "Bearer "+token, ""); w.Code != 401 {
			t.Fatal("ignored configured auth header", w.Code)
		}
		if w := authRequest(path, s.Config.AdminTokenHeader, "Bearer "+token, "https://other.invalid"); w.Code != 403 {
			t.Fatal("cross-origin access", w.Code)
		}
	}
	get := func(path string) *httptest.ResponseRecorder {
		return authRequest(path, s.Config.AdminTokenHeader, "Bearer "+token, "")
	}
	w := get("/api/voicemails/unlinked")
	var result struct {
		Items []unlinkedVoicemailView `json:"items"`
		Next  int64                   `json:"next_before"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 1 || result.Next != 0 {
		t.Fatal("missing unlinked metadata", w.Code)
	}
	item := result.Items[0]
	if item.Slot != 0 || item.StartTS != 0 || item.Phone != "TEST-CALLER" || item.CreatedAt != 1000 || item.FileState != "available" || item.Size != int64(len(audio)) {
		t.Fatal("metadata was guessed or lost")
	}
	for _, private := range []string{"stored_name", "sha256", "media_id", s.Config.DataDir, token} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private internal metadata disclosed")
		}
	}
	w = get("/api/voicemails/unlinked/1/audio")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), audio) || w.Header().Get("Content-Type") != "audio/amr" || w.Header().Get("Content-Disposition") != `attachment; filename="unlinked-voicemail-1.amr"` {
		t.Fatal("wrong authenticated audio response", w.Code)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO calls(id,key,device_id,slot,phone,start_ts,state,received_at) VALUES(1,'SYNTHETIC-CALL','SIM-TEST',1,'TEST-CALLER',1000,'answered',1000); UPDATE voicemails SET call_id=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if w = get("/api/voicemails/unlinked/1/audio"); w.Code != 404 || !strings.Contains(w.Body.String(), "voicemail_unavailable") {
		t.Fatal("stale unlinked download not retired", w.Code)
	}
	w = get("/api/voicemails/unlinked")
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 0 {
		t.Fatal("linked audio stayed in list")
	}
	if w = get("/api/calls/1/voicemail"); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), audio) {
		t.Fatal("existing call playback regressed", w.Code)
	}
	for _, path := range []string{"/api/voicemails/unlinked?before=-1", "/api/voicemails/unlinked?before=bad", "/api/voicemails/unlinked/0/audio"} {
		if w = get(path); w.Code != 400 {
			t.Fatal("invalid cursor or id accepted", w.Code)
		}
	}
	var notifications int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM call_notifications`).Scan(&notifications); err != nil || notifications != 0 {
		t.Fatal("view generated notifications", err)
	}
}

func TestUnlinkedVoicemailMissingUnsafeFilesAndBoundedPagination(t *testing.T) {
	s, _ := fixture(t, 1)
	dir := filepath.Join(s.Config.DataDir, "voicemail")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 53; id++ {
		name := strings.Repeat("a", 30) + strconv.FormatInt(int64(id), 16)
		name = strings.Repeat("0", 32-len(name)) + name + ".amr"
		if id == 52 {
			name = "../../SYNTHETIC-PRIVATE"
		}
		if _, err := s.Store.DB.Exec(`INSERT INTO voicemails(id,media_id,slot,phone,start_ts,sha256,size,stored_name,created_at) VALUES(?,?,0,'',0,?,10,?,1000)`, id, "media-"+strconv.Itoa(id), "hash-"+strconv.Itoa(id), name); err != nil {
			t.Fatal(err)
		}
		if id == 51 {
			outside := filepath.Join(s.Config.DataDir, "SYNTHETIC-PRIVATE")
			if err := os.WriteFile(outside, []byte("PRIVATE"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	w := request(s, "GET", "http://127.0.0.1/api/voicemails/unlinked", "", "")
	var result struct {
		Items []unlinkedVoicemailView `json:"items"`
		Next  int64                   `json:"next_before"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 50 || result.Next != 4 {
		t.Fatal("unbounded or invalid first page", w.Code)
	}
	if result.Items[0].FileState != "missing" || result.Items[1].FileState != "unavailable" || result.Items[1].FileName != "" || result.Items[2].FileState != "unavailable" {
		t.Fatal("missing or unsafe file shown as playable")
	}
	if strings.Contains(w.Body.String(), "SYNTHETIC-PRIVATE") {
		t.Fatal("unsafe filename disclosed")
	}
	for _, id := range []string{"51", "52", "53"} {
		if w = request(s, "GET", "http://127.0.0.1/api/voicemails/unlinked/"+id+"/audio", "", ""); w.Code != 404 || strings.Contains(w.Body.String(), "PRIVATE") {
			t.Fatal("unsafe or missing audio returned", w.Code)
		}
	}
	w = request(s, "GET", "http://127.0.0.1/api/voicemails/unlinked?before=4", "", "")
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 3 || result.Next != 0 || result.Items[0].ID != 3 {
		t.Fatal("invalid older page")
	}
}
