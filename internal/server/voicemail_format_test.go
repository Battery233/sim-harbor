package server

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/maintenance"
	"github.com/Battery233/sim-harbor/internal/recording"
)

func TestVoicemailUploadFormatMatchesRecordingBackup(t *testing.T) {
	for _, form := range []bool{false, true} {
		name := "raw"
		if form {
			name = "multipart"
		}
		t.Run(name, func(t *testing.T) {
			token := strings.Repeat("ab", 16)
			s, _ := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2) })
			for _, tc := range []struct {
				audio  string
				status int
			}{{"#!AMR", 400}, {"#!AMRX\nSYNTHETIC", 400}, {"#!AMR-WB\nSYNTHETIC", 400}, {recording.Header + "SYNTHETIC", 200}} {
				audio := tc.audio
				body := bytes.NewBufferString(audio)
				contentType := "application/octet-stream"
				if form {
					body = new(bytes.Buffer)
					writer := multipart.NewWriter(body)
					part, err := writer.CreateFormFile("media", "synthetic.amr")
					if err != nil {
						t.Fatal(err)
					}
					if _, err = part.Write([]byte(audio)); err != nil {
						t.Fatal(err)
					}
					if err = writer.Close(); err != nil {
						t.Fatal(err)
					}
					contentType = writer.FormDataContentType()
				}
				r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/voicemail/amr/"+token, body)
				r.RemoteAddr = "127.0.0.1:9"
				r.Header.Set("Content-Type", contentType)
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				if w.Code != tc.status {
					t.Fatal("unexpected upload format response", w.Code, tc.status)
				}
			}
			var rows int
			if err := s.Store.DB.QueryRow(`SELECT count(*) FROM voicemails`).Scan(&rows); err != nil || rows != 1 {
				t.Fatal("invalid uploads left indexes", rows, err)
			}
			a, err := maintenance.Create(t.Context(), s.Store, t.TempDir(), maintenance.BackupOptions{IncludeRecordings: true, DataDirectory: s.Config.DataDir})
			if err != nil {
				t.Fatal("accepted NB recording cannot be backed up", err)
			}
			defer a.Close()
			preview, err := maintenance.Inspect(t.Context(), a.Path)
			if err != nil || !preview.Manifest.IncludesRecordings || len(preview.Manifest.Recordings) != 1 {
				t.Fatal("recording backup did not round-trip", err)
			}
		})
	}
}
