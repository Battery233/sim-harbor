package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
)

type gatedVoicemailBody struct {
	*strings.Reader
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (b *gatedVoicemailBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.release })
	return b.Reader.Read(p)
}
func (b *gatedVoicemailBody) Close() error { return nil }

func TestVoicemailPublicConcurrencyAndBodyLimit(t *testing.T) {
	s, _ := fixture(t, 1, publicRecording)
	path := "http://internal/api/voicemail/amr/" + strings.Repeat("ab", 16)
	var done []chan int
	var bodies []*gatedVoicemailBody
	defer func() {
		for _, body := range bodies {
			close(body.release)
		}
		for _, finished := range done {
			select {
			case code := <-finished:
				if code != 200 {
					t.Error("active upload failed", code)
				}
			case <-time.After(3 * time.Second):
				t.Error("upload did not release")
			}
		}
	}()
	for range 2 {
		body := &gatedVoicemailBody{Reader: strings.NewReader("#!AMR\nSYNTHETIC"), started: make(chan struct{}), release: make(chan struct{})}
		bodies = append(bodies, body)
		r := httptest.NewRequest("POST", path, nil)
		r.RemoteAddr = "127.0.0.1:9"
		r.Body = body
		finished := make(chan int, 1)
		done = append(done, finished)
		go func() { w := httptest.NewRecorder(); s.Handler().ServeHTTP(w, r); finished <- w.Code }()
		select {
		case <-body.started:
		case <-time.After(time.Second):
			t.Fatal("upload did not read body")
		}
	}
	if w := postVoicemail(s, strings.Repeat("ab", 16), []byte("#!AMR\nSYNTHETIC"), 10, "127.0.0.1:9"); w.Code != 429 {
		t.Fatal("third active body accepted", w.Code)
	}
	// The deferred drain verifies release before the store is closed.
}

func TestVoicemailPublicRejectsOversizeAndUnauthenticatedBodies(t *testing.T) {
	s, _ := fixture(t, 1, publicRecording)
	for _, peer := range []string{"203.0.113.1:9", "127.0.0.1:9"} {
		body := &gatedVoicemailBody{Reader: strings.NewReader("#!AMR\nSYNTHETIC"), started: make(chan struct{}), release: make(chan struct{})}
		close(body.release)
		r := httptest.NewRequest("POST", "http://internal/api/voicemail/amr/"+strings.Repeat("cd", 16), nil)
		r.Body = body
		r.RemoteAddr = peer
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		select {
		case <-body.started:
			t.Fatal("unauthenticated request body was read")
		default:
		}
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("invalid request accepted", w.Code)
		}
	}
	audio := append([]byte("#!AMR\n"), bytes.Repeat([]byte("X"), 2<<20)...)
	if w := postVoicemail(s, strings.Repeat("ab", 16), audio, 10, "127.0.0.1:9"); w.Code != 400 {
		t.Fatal("oversized recording accepted", w.Code)
	}
}

func publicRecording(c *config.Config) {
	enableVoicemail(c, strings.Repeat("ab", 16), 1)
	c.VoicemailUploadMode = "public"
	c.VoicemailAMRURL = "https://upload.example.com/api/voicemail/amr/" + strings.Repeat("ab", 16)
	c.Environment = "cloud"
	c.AdminToken = strings.Repeat("T", 32)
}

func TestVoicemailPublicProxyTokenAndMetadata(t *testing.T) {
	s, _ := fixture(t, 1, publicRecording)
	upload := func(peer, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://internal/api/voicemail/amr/"+token+"?slot=1&start=1700000000&phone=SYNTHETIC", strings.NewReader("#!AMR\nSYNTHETIC"))
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("CF-Connecting-IP", "127.0.0.1")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := upload("203.0.113.1:9", strings.Repeat("ab", 16)); w.Code != 403 {
		t.Fatal("spoofed proxy headers accepted", w.Code)
	}
	if w := upload("127.0.0.1:9", strings.Repeat("cd", 16)); w.Code != 401 {
		t.Fatal("bad token accepted", w.Code)
	}
	off := false
	s.Config.DeviceAllowlistEnabled = &off
	if w := upload("127.0.0.1:9", strings.Repeat("ab", 16)); w.Code != 200 {
		t.Fatal("TCP allowlist affected public upload", w.Code, w.Body)
	}
	var slot int
	var start int64
	var phone string
	if err := s.Store.DB.QueryRow(`SELECT slot,start_ts,phone FROM voicemails`).Scan(&slot, &start, &phone); err != nil || slot != 1 || start != 1700000000 || phone != "SYNTHETIC" {
		t.Fatal("metadata lost", err)
	}
	if w := request(s, "GET", "http://example.com/api/voicemails/unlinked", "", ""); w.Code != 401 {
		t.Fatal("upload credential opened management", w.Code)
	}
	s.Config.VoicemailProxyCIDRs = []string{"172.18.0.1/32"}
	if w := upload("127.0.0.1:9", strings.Repeat("ab", 16)); w.Code != 403 {
		t.Fatal("default proxy still trusted")
	}
	if w := upload("172.18.0.1:9", strings.Repeat("ab", 16)); w.Code != 200 {
		t.Fatal("configured connector rejected", w.Code)
	}
	rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 5000)
	encoded, _ := json.Marshal(rows)
	if bytes.Contains(encoded, []byte(strings.Repeat("ab", 16))) || bytes.Contains(encoded, []byte("SYNTHETIC")) {
		t.Fatal("upload secrets logged")
	}
}

func TestVoicemailPublicReverseProxyPreservesPathAndQuery(t *testing.T) {
	s, _ := fixture(t, 1, publicRecording)
	origin := httptest.NewServer(s.Handler())
	defer origin.Close()
	target, _ := url.Parse(origin.URL)
	proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(target))
	defer proxy.Close()
	resp, err := http.Post(proxy.URL+"/api/voicemail/amr/"+strings.Repeat("ab", 16)+"?slot=2&start=1700000001&phone=SYNTHETIC-PROXY", "audio/amr", strings.NewReader("#!AMR\nSYNTHETIC"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatal("proxied upload rejected", resp.StatusCode)
	}
	var slot int
	var phone string
	if err = s.Store.DB.QueryRow(`SELECT slot,phone FROM voicemails`).Scan(&slot, &phone); err != nil || slot != 2 || phone != "SYNTHETIC-PROXY" {
		t.Fatal("proxy metadata lost", err)
	}
}

func TestVoicemailPublicLimitsAndCapacity(t *testing.T) {
	s, _ := fixture(t, 1, publicRecording)
	for i := range 10 {
		if w := postVoicemail(s, strings.Repeat("ef", 16), []byte("#!AMR\nSYNTHETIC"), int64(i), "127.0.0.1:9"); w.Code != 401 {
			t.Fatal("unexpected token response", w.Code)
		}
	}
	if w := postVoicemail(s, strings.Repeat("ab", 16), []byte("#!AMR\nSYNTHETIC"), 10, "127.0.0.1:9"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("public flood not bounded", w.Code)
	}
	var limiter voicemailIngress
	now := time.Now()
	if !limiter.admit(now) || !limiter.admit(now) || limiter.admit(now) {
		t.Fatal("concurrency cap missing")
	}
	limiter.release()
	limiter.release()
	for range 8 {
		if !limiter.admit(now) {
			t.Fatal("burst smaller than configured")
		}
		limiter.release()
	}
	if limiter.admit(now) || !limiter.admit(now.Add(time.Second)) {
		t.Fatal("rate refill incorrect")
	}
	limiter.release()
	s.voicemailIngress = voicemailIngress{}
	s.Config.VoicemailMaxStorageMiB = 16
	dir := filepath.Join(s.Config.DataDir, "voicemail")
	os.MkdirAll(dir, 0700)
	f, err := os.Create(filepath.Join(dir, "SYNTHETIC.partial"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(16 << 20)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if w := postVoicemail(s, strings.Repeat("ab", 16), []byte("#!AMR\nSYNTHETIC"), 10, "127.0.0.1:9"); w.Code != 507 {
		t.Fatal("capacity overflow accepted", w.Code)
	}
	var count int
	s.Store.DB.QueryRow(`SELECT count(*) FROM voicemails`).Scan(&count)
	if count != 0 {
		t.Fatal("rejected upload was indexed")
	}
}

func TestVoicemailLANEnableBlockedWithoutAllowlistButRestoreRemainsAvailable(t *testing.T) {
	s := settingsFixture(t)
	snap, _ := s.Settings.Snapshot()
	off := false
	snap.Values.DeviceAllowlistEnabled = &off
	enableVoicemail(&snap.Values, strings.Repeat("a", 32), 1)
	body := map[string]any{"values": snap.Values, "revision": snap.Revision, "confirmed": true, "voicemail_confirmed": true}
	if w := settingsRequest(s, "PUT", "/api/settings", body, true); w.Code != 409 || !strings.Contains(w.Body.String(), "voicemail_allowlist_required") {
		t.Fatal("new LAN enable accepted", w.Code)
	}
	s.Config.DeviceAllowlistEnabled = &off
	enableVoicemail(&s.Config, strings.Repeat("a", 32), 1)
	if w := action(s, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "enable", "confirmed": true, "expected": idleVoicemail()}); w.Code != 409 {
		t.Fatal("board enable accepted without reception")
	}
	if w := action(s, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "restore", "confirmed": true, "expected": idleVoicemail()}); strings.Contains(w.Body.String(), "voicemail_allowlist_required") {
		t.Fatal("restore blocked by upload source policy")
	}
}
