package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/recording"
	"github.com/Battery233/sim-harbor/internal/store"
)

// These read-only HTTP handlers need a store, but no device TCP listener.
func durationFixture(t *testing.T, options ...func(*config.Config)) *Server {
	t.Helper()
	c := config.Config{Environment: "local", HTTPAddr: "127.0.0.1:8080", TCPAddr: "127.0.0.1:9000", DataDir: t.TempDir(), DeviceID: "SIM-TEST", DeviceCIDRs: []string{"127.0.0.1/32"}, AESMode: 1, AESKey: "0123456789abcdef", AESIV: "fedcba9876543210", Timezone: "Asia/Shanghai", StaleSeconds: 345}
	for _, option := range options {
		option(&c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(c.DataDir, "sim.db"), store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := New(c, db)
	t.Cleanup(func() {
		s.CloseBackups()
		db.DB.Close()
	})
	return s
}

func TestVoicemailDurationReadOnlyForLinkedAndUnlinkedAudio(t *testing.T) {
	s := durationFixture(t)
	audio := append([]byte(recording.Header), bytes.Repeat([]byte{0x7c}, 500)...)
	media, _, _, err := s.Store.SaveVoicemail(t.Context(), s.Config.DataDir, audio, 0, "", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/voicemails/duration/" + media
	check := func(want *int64) {
		t.Helper()
		w := request(s, "GET", path, "", "")
		var result struct {
			MS *int64 `json:"duration_ms"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("duration request failed", w.Code)
		}
		if want == nil && result.MS != nil || want != nil && (result.MS == nil || *want != *result.MS) {
			t.Fatal("wrong audio duration", result.MS)
		}
		var played, jobs, schema int64
		err := s.Store.DB.QueryRow(`SELECT played_at,(SELECT count(*) FROM call_notifications),(SELECT version FROM schema_version) FROM voicemails WHERE media_id=?`, media).Scan(&played, &jobs, &schema)
		if err != nil || played != 0 || jobs != 0 || schema != 21 {
			t.Fatal("duration read changed business state", err)
		}
	}
	ms := int64(10000)
	check(&ms)
	// Call timestamps deliberately differ from the audio's ten-second duration.
	if _, err := s.Store.DB.Exec(`INSERT INTO calls(id,key,device_id,slot,phone,start_ts,connected_ts,end_ts,ended,state,received_at) VALUES(1,'SYNTHETIC','SIM-TEST',1,'',1000,1005,1095,1,'answered',1100); UPDATE voicemails SET call_id=1 WHERE media_id=?`, media); err != nil {
		t.Fatal(err)
	}
	check(&ms)
	file := filepath.Join(s.Config.DataDir, "voicemail", media+".amr")
	// Same size, invalid frame types: no fabricated or partial duration.
	bad := append([]byte(recording.Header), bytes.Repeat([]byte{0x4c}, 500)...)
	if err := os.WriteFile(file, bad, 0600); err != nil {
		t.Fatal(err)
	}
	check(nil)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", path, "", ""); w.Code != 404 {
		t.Fatal("missing recording accepted", w.Code)
	}
	if err := s.Store.DeleteVoicemail(t.Context(), s.Config.DataDir, media); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", path, "", ""); w.Code != 404 {
		t.Fatal("deleted recording accepted", w.Code)
	}
	if w := request(s, "GET", "/api/voicemails/duration/invalid", "", ""); w.Code != 400 {
		t.Fatal("invalid media accepted", w.Code)
	}
}

func TestVoicemailDurationUsesManagementAuth(t *testing.T) {
	const token = "SYNTHETIC-DURATION-ADMIN-TOKEN"
	s := durationFixture(t, func(c *config.Config) {
		c.AdminToken, c.AdminTokenHeader, c.Environment = token, "X-SIMHarbor-Authorization", "cloud"
	})
	media, _, _, err := s.Store.SaveVoicemail(t.Context(), s.Config.DataDir, []byte(recording.Header+"\x7c"), 0, "", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		header, value, origin string
		status                int
	}{
		{"", "", "", 401},
		{"Authorization", "Bearer " + token, "", 401},
		{s.Config.AdminTokenHeader, "Bearer " + token, "https://other.invalid", 403},
		{s.Config.AdminTokenHeader, "Bearer " + token, "", 200},
	} {
		r := httptest.NewRequest("GET", "http://example.invalid/api/voicemails/duration/"+media, nil)
		if tc.header != "" {
			r.Header.Set(tc.header, tc.value)
		}
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal("management authentication changed", w.Code, tc.status)
		}
	}
}
