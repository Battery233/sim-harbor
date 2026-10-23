package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

type waitingPushTransport func(*http.Request) (*http.Response, error)

func (f waitingPushTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSlowPushDoesNotBlockCallOrRecordingIngress(t *testing.T) {
	for _, capacity := range []bool{false, true} {
		name := "message"
		if capacity {
			name = "capacity"
		}
		t.Run(name, func(t *testing.T) {
			token := strings.Repeat("a", 32)
			s, _ := fixture(t, 0, func(c *config.Config) {
				enableVoicemail(c, token, 1)
				c.AdminToken = "SYNTHETIC-INGRESS-MANAGEMENT-TOKEN"
				c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC-NO-NETWORK", "https://bark.example.invalid", "https://example.invalid"
				if capacity {
					c.VoicemailUploadMode, c.VoicemailMaxStorageMiB = "public", 16
					c.VoicemailAMRURL = "https://upload.example.invalid/api/voicemail/amr/" + token
				}
			})
			entered, release := make(chan struct{}), make(chan struct{})
			var arrived, released sync.Once
			s.client.Transport = waitingPushTransport(func(*http.Request) (*http.Response, error) {
				arrived.Do(func() { close(entered) })
				<-release
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":200}`))}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if capacity {
				dir := filepath.Join(s.Config.DataDir, "voicemail")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"one.partial", "two.partial", "three.partial", "four.partial"} {
					f, err := os.Create(filepath.Join(dir, name))
					if err != nil {
						t.Fatal(err)
					}
					err = f.Truncate(2 << 20)
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				e := protocol.Event{DeviceID: s.Config.DeviceID, Type: 501, Slot: 1, Phone: "SYNTHETIC-SENDER", Body: "SYNTHETIC", SMSTS: 1000}
				raw, _ := json.Marshal(e)
				if err := s.Store.Ingest(ctx, e, raw, true); err != nil {
					t.Fatal(err)
				}
			}
			worker := make(chan struct{})
			go func() { defer close(worker); s.RunNotifications(ctx) }()
			defer func() { released.Do(func() { close(release) }); cancel(); <-worker }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("synthetic push did not start")
			}
			for _, canceled := range []bool{false, false, true} {
				r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/voicemail/amr/"+token, bytes.NewBufferString("#!AMR\nSYNTHETIC"))
				r.RemoteAddr = "127.0.0.1:9"
				if canceled {
					c, stop := context.WithCancel(ctx)
					stop()
					r = r.WithContext(c)
				}
				w, done := httptest.NewRecorder(), make(chan struct{})
				go func() { defer close(done); s.Handler().ServeHTTP(w, r) }()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("recording ingress waits for Bark")
				}
				want := 200
				if canceled {
					want = 503
				}
				if w.Code != want {
					t.Fatal("upload response", w.Code, want)
				}
			}
			var recordings int
			if err := s.Store.DB.QueryRow(`SELECT count(*) FROM voicemails`).Scan(&recordings); err != nil || recordings != 1 {
				t.Fatal("duplicate upload created another recording", recordings, err)
			}
			a, b := net.Pipe()
			handled := make(chan struct{})
			go func() { defer close(handled); s.handle(ctx, a) }()
			defer func() { b.Close(); a.Close(); <-handled }()
			p, _ := json.Marshal(map[string]any{"devId": s.Config.DeviceID, "type": 603, "slot": 1, "phNum": "anonymous", "telStartTs": 1000, "telEndTs": 1002})
			if _, err := b.Write(append(p, protocol.Delimiter...)); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for {
				var count int
				if err := s.Store.DB.QueryRow(`SELECT count(*) FROM calls`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("call report waits for Bark")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			released.Do(func() { close(release) })
			select {
			case <-worker:
			case <-time.After(3 * time.Second):
				t.Fatal("notification worker did not stop")
			}
			table := "notifications"
			if capacity {
				table = "system_notifications"
			}
			var accepted int
			if err := s.Store.DB.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE state='sent' AND accepted_at>0`).Scan(&accepted); err != nil || accepted != 1 {
				t.Fatal("shutdown lost accepted push result", accepted, err)
			}
		})
	}
}
