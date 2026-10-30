package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/recording"
)

func accessSettingsFixture(t *testing.T, options ...func(*config.Config)) *Server {
	t.Helper()
	s := httpFixture(t, append([]func(*config.Config){func(c *config.Config) {
		c.AdminToken = "SYNTHETIC-MANAGEMENT-TOKEN-ONLY"
	}}, options...)...)
	var err error
	s.Settings, err = config.OpenSettings(s.Config)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func reloadAccessSettings(t *testing.T, s *Server) {
	t.Helper()
	var err error
	s.Config, err = config.Load(filepath.Join(s.Config.DataDir, "system.private.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Settings, err = config.OpenSettings(s.Config)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSkipAdminTokenRequiresSavedAndAppliedOptInAndCanBeDisabled(t *testing.T) {
	s := accessSettingsFixture(t)
	token := s.Config.AdminToken
	snap, _ := s.Settings.Snapshot()
	snap.Values.SkipAdminToken = true
	body := map[string]any{"values": snap.Values, "revision": snap.Revision, "confirmed": true}
	if w := settingsRequest(s, "PUT", "/api/settings", body, false); w.Code != 401 {
		t.Fatal("anonymous caller could disable token checking", w.Code)
	}
	if w := settingsRequest(s, "PUT", "/api/settings", body, true); w.Code != 200 {
		t.Fatal("could not save explicit skip policy", w.Code)
	}
	if w := request(s, "GET", "http://example.com/api/state", "", ""); w.Code != 401 {
		t.Fatal("pending skip policy became active before restart", w.Code)
	}
	w := request(s, "GET", "http://example.com/api/auth", "", "")
	var policy map[string]any
	if json.Unmarshal(w.Body.Bytes(), &policy) != nil || policy["skip_admin_token"] != false {
		t.Fatal("discovery exposed pending skip policy")
	}
	reloadAccessSettings(t, s)
	for _, path := range []string{"/api/state", "/api/settings", "/api/navigation-counts", "/api/health"} {
		if w := request(s, "GET", "http://example.com"+path, "", ""); w.Code != 200 {
			t.Fatal("explicit external authentication still required token", path, w.Code)
		}
	}
	w = request(s, "GET", "http://example.com/api/auth", "", "")
	if json.Unmarshal(w.Body.Bytes(), &policy) != nil || policy["skip_admin_token"] != true || containsPrivate(w.Body.String(), token, s.Config.AESKey, s.Config.AESIV) {
		t.Fatal("active policy unavailable or exposed credentials")
	}
	snap, _ = s.Settings.Snapshot()
	snap.Values.SkipAdminToken = false
	body = map[string]any{"values": snap.Values, "revision": snap.Revision}
	if w := settingsRequest(s, "PUT", "/api/settings", body, false); w.Code != 400 {
		t.Fatal("skipping token bypassed settings confirmation", w.Code)
	}
	body["confirmed"] = true
	if w := settingsRequest(s, "PUT", "/api/settings", body, false); w.Code != 200 {
		t.Fatal("could not restore panel authentication", w.Code)
	}
	if w := settingsRequest(s, "PUT", "/api/settings", body, false); w.Code != 409 {
		t.Fatal("skipping token bypassed revision conflict", w.Code)
	}
	reloadAccessSettings(t, s)
	if s.Config.AdminToken != token {
		t.Fatal("switching policy lost original token")
	}
	if w := request(s, "GET", "http://example.com/api/state", "", ""); w.Code != 401 {
		t.Fatal("panel authentication did not resume", w.Code)
	}
	if w := request(s, "GET", "http://example.com/api/state", "Bearer "+token, ""); w.Code != 200 {
		t.Fatal("retained credential no longer works", w.Code)
	}
}

func TestSkipAdminTokenKeepsOriginUploadAndRecordingStateBoundaries(t *testing.T) {
	s := accessSettingsFixture(t, func(c *config.Config) {
		c.SkipAdminToken = true
		c.AdminToken = ""
		c.Environment = "cloud"
		c.VoicemailUploadMode = "public"
	})
	for _, path := range []string{"/api/state", "/api/settings"} {
		if w := request(s, "GET", "https://management.example.com"+path, "", ""); w.Code != 200 {
			t.Fatal("skip policy without stored token blocked management host", path, w.Code)
		}
		if w := request(s, "GET", "https://management.example.com"+path, "", "https://other.invalid"); w.Code != 403 {
			t.Fatal("foreign origin accepted without panel token", w.Code)
		}
	}
	r := httptest.NewRequest("GET", "https://management.example.com/api/state", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site browser request without Origin accepted", w.Code)
	}
	// Upload ingress keeps its own enabled/source/path-token checks.
	if w := request(s, "POST", "https://upload.example.com/api/voicemail/amr/"+string(bytes.Repeat([]byte{'a'}, 32)), "", ""); w.Code < 400 {
		t.Fatal("skipping panel token enabled unconfigured recording ingress", w.Code)
	}
	audio := append([]byte(recording.Header), bytes.Repeat([]byte{0x7c}, 50)...)
	media, _, _, err := s.Store.SaveVoicemail(t.Context(), s.Config.DataDir, audio, 0, "", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM voicemails WHERE media_id=?`, media).Scan(&id); err != nil {
		t.Fatal(err)
	}
	w = request(s, "GET", "https://management.example.com/api/voicemails/unlinked/"+strconv.FormatInt(id, 10)+"/audio", "", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), audio) {
		t.Fatal("skip policy did not cover raw recording downloads", w.Code)
	}
	mark := "/api/voicemails/" + media + "/played"
	if w := settingsRequest(s, "POST", "https://management.example.com"+mark, map[string]any{}, false); w.Code != 200 {
		t.Fatal("explicit playback marking failed without panel token", w.Code)
	}
	var played, jobs int64
	if err := s.Store.DB.QueryRow(`SELECT played_at,(SELECT count(*) FROM call_notifications) FROM voicemails WHERE media_id=?`, media).Scan(&played, &jobs); err != nil || played <= 0 || jobs != 0 {
		t.Fatal("playback marking did not persist or created a notification", err)
	}
	r = httptest.NewRequest(http.MethodPost, "https://management.example.com"+mark, bytes.NewBufferString(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://other.invalid")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin state change accepted", w.Code)
	}
}
