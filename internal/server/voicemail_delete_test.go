package server

import (
	"context"
	"encoding/json"
	"github.com/Battery233/sim-harbor/internal/config"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoicemailDeleteAuthConfirmationAndDisabledService(t *testing.T) {
	s, _ := fixture(t, 1, func(c *config.Config) { c.AdminToken = "SYNTHETIC-MANAGEMENT-ACCESS-TOKEN" })
	media, _, _, err := s.Store.SaveVoicemail(context.Background(), s.Config.DataDir, []byte("#!AMR\nSYNTHETIC"), 0, "", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	attempt := func(id, auth, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("DELETE", "http://example.invalid/api/voicemails/"+id, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	auth := "Bearer " + s.Config.AdminToken
	for _, tc := range []struct {
		auth, origin, body string
		code               int
	}{{"", "", `{"confirmed":true}`, 401}, {auth, "https://other.invalid", `{"confirmed":true}`, 403}, {auth, "", `{"confirmed":false}`, 400}} {
		if w := attempt(media, tc.auth, tc.origin, tc.body); w.Code != tc.code {
			t.Fatal("deletion protection", w.Code, tc.code)
		}
	}
	if w := attempt(media, auth, "", `{"confirmed":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := attempt(media, auth, "", `{"confirmed":true}`); w.Code != 200 {
		t.Fatal("idempotent delete", w.Code)
	}
	r := httptest.NewRequest("GET", "http://example.invalid/api/voicemails/unlinked", nil)
	r.Header.Set("Authorization", auth)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var data struct {
		Items []json.RawMessage `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data.Items) != 0 {
		t.Fatal("deleted audio still listed", w.Code)
	}
}
