package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func fixture(t *testing.T, mode int, options ...func(*config.Config)) (*Server, net.Listener) {
	t.Helper()
	c := config.Config{Environment: "local", HTTPAddr: "127.0.0.1:8080", TCPAddr: "127.0.0.1:9000", DataDir: t.TempDir(), DeviceID: "SIM-TEST", DeviceCIDRs: []string{"127.0.0.1/32"}, AESMode: mode, AESKey: "0123456789abcdef", AESIV: "fedcba9876543210", Timezone: "Asia/Shanghai", StaleSeconds: 345}
	for _, option := range options {
		option(&c)
	}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(c.DataDir, "sim.db"))
	if e != nil {
		t.Fatal(e)
	}
	s := New(c, db)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ServeTCP(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("TCP shutdown stalled")
		}
		db.DB.Close()
	})
	return s, l
}
func connect(t *testing.T, l net.Listener) net.Conn {
	t.Helper()
	c, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(3 * time.Second))
	return c
}
func send(t *testing.T, c net.Conn, codec protocol.Codec, v any, wrapped ...bool) {
	t.Helper()
	p, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	p, e = codec.EncodeUpload(p)
	if e != nil {
		t.Fatal(e)
	}
	if len(wrapped) > 0 && wrapped[0] {
		p, e = json.Marshal(map[string]string{"p": string(p)})
		if e != nil {
			t.Fatal(e)
		}
	}
	p = append(p, protocol.Delimiter...)
	for _, b := range p {
		if _, e = c.Write([]byte{b}); e != nil {
			t.Fatal(e)
		}
	}
}
func read(t *testing.T, r *protocol.Reader, codec protocol.Codec) map[string]any {
	t.Helper()
	p, e := r.Next()
	if e != nil {
		t.Fatal(e)
	}
	if codec.Mode == 2 {
		p, e = codec.Open(p)
		if e != nil {
			t.Fatal(e)
		}
	}
	var v map[string]any
	if e = json.Unmarshal(p, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func request(s *Server, method, path, token, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if s.Config.AdminToken == "" {
		r.Host = "127.0.0.1:8080" // Model the credential-free loopback entrypoint.
	}
	if token != "" {
		r.Header.Set("Authorization", token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestTCPToInboxAndStatusQuery(t *testing.T) {
	for _, tc := range []struct {
		mode    int
		env     string
		wrapped bool
	}{{0, "local", false}, {1, "local", false}, {2, "local", false}, {2, "cloud", false}, {1, "local", true}, {2, "local", true}, {2, "cloud", true}} {
		name := tc.env + string(rune('0'+tc.mode))
		if tc.wrapped {
			name += "_wrapped"
		}
		t.Run(name, func(t *testing.T) {
			s, l := fixture(t, tc.mode, func(c *config.Config) {
				c.Environment = tc.env
				if tc.env == "cloud" {
					c.AdminToken = strings.Repeat("t", 24)
				}
			})
			auth := ""
			if s.Config.AdminToken != "" {
				auth = "Bearer " + s.Config.AdminToken
			}
			c := connect(t, l)
			codec := s.codec()
			reader := protocol.NewReader(c)
			upload := func(v any) { send(t, c, codec, v, tc.wrapped) }
			upload(map[string]any{"devId": "SIM-TEST", "type": 100})
			now := read(t, reader, codec)
			if now["cmd"] != "now" || len(now["p1"].(string)) != 14 {
				t.Fatal("handshake response")
			}
			upload(map[string]any{"type": 999, "code": 0, "tid": now["tid"]}) // Bound replies may omit devId.
			for _, slot := range []int{1, 2, 1} {
				upload(map[string]any{"devId": "SIM-TEST", "type": 501, "slot": slot, "phNum": "TEST-SENDER", "smsTs": 1700000000, "smsBd": "合成消息"})
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- request(s, "POST", "http://example.com/api/state/query", auth, "") }()
			cmd := read(t, reader, codec)
			if cmd["cmd"] != "stat" {
				t.Fatal("unexpected business ACK or command")
			}
			upload(map[string]any{"type": 401, "code": 0, "tid": cmd["tid"]})
			upload(map[string]any{"type": 402, "subType": 46, "code": 0, "tid": cmd["tid"], "wifi": map[string]any{"rssi": -45}})
			select {
			case w := <-done:
				if w.Code != 200 {
					t.Fatalf("stat result: %d %s", w.Code, w.Body)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("query stalled")
			}
			w := request(s, "GET", "http://example.com/api/messages", auth, "")
			var items struct{ Items []store.Message }
			json.Unmarshal(w.Body.Bytes(), &items)
			if w.Code != 200 || len(items.Items) != 2 {
				t.Fatalf("inbox: %s", w.Body)
			}
			w = request(s, "GET", "http://example.com/api/state", auth, "")
			var state map[string]any
			json.Unmarshal(w.Body.Bytes(), &state)
			if state["online"] != true || state["reported"].(map[string]any)["wifi"] == nil {
				t.Fatal("online/state response")
			}
			c.Close()
		})
	}
}

func TestDeviceIDAndSourceRejected(t *testing.T) {
	s, l := fixture(t, 0)
	c := connect(t, l)
	send(t, c, s.codec(), map[string]any{"devId": "OTHER", "type": 100})
	if _, e := protocol.NewReader(c).Next(); e == nil {
		t.Fatal("wrong device accepted")
	}
	var count int
	s.Store.DB.QueryRow("SELECT count(*) FROM device").Scan(&count)
	if count != 0 {
		t.Fatal("rejected device persisted")
	}
	// Source matching is exercised on the production listener, not just Config.Allows.
	c.Close()
	_, l2 := fixture(t, 0, func(c *config.Config) { c.DeviceCIDRs = []string{"192.0.2.1/32"} })
	c2 := connect(t, l2)
	if _, e := protocol.NewReader(c2).Next(); e == nil {
		t.Fatal("unapproved source accepted")
	}
}

func TestManagementAccessAndOffline(t *testing.T) {
	s, _ := fixture(t, 0)
	s.Config.AdminToken = strings.Repeat("t", 24)
	for _, tc := range []struct {
		token, origin string
		want          int
	}{{"", "", 401}, {s.Config.AdminToken, "", 401}, {"Bearer " + s.Config.AdminToken, "https://evil.invalid", 403}, {"Bearer " + s.Config.AdminToken, "http://example.com", 200}} {
		w := request(s, "GET", "http://example.com/api/state", tc.token, tc.origin)
		if w.Code != tc.want {
			t.Fatalf("access: %d want %d", w.Code, tc.want)
		}
	}
	if w := request(s, "POST", "http://example.com/api/state/query", "Bearer "+s.Config.AdminToken, ""); w.Code != 409 {
		t.Fatal("offline query not rejected")
	}
	if w := request(s, "GET", "http://example.com/api/messages/999", "Bearer "+s.Config.AdminToken, ""); w.Code != 404 {
		t.Fatal("unknown message")
	}
}

func TestBarkResponseAndRedirect(t *testing.T) {
	s, _ := fixture(t, 0)
	s.Config.BarkKey = "SYNTHETIC-KEY"
	s.Config.PublicURL = "https://example.invalid"
	for _, tc := range []struct {
		status int
		body   string
		want   bool
	}{{200, `{"code":200}`, true}, {200, `{"code":400}`, false}, {500, `{"code":200}`, false}, {302, `{"code":200}`, false}, {200, `{`, false}} {
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/push" || r.Method != "POST" {
				t.Error("Bark request shape")
			}
			p, _ := io.ReadAll(r.Body)
			var v map[string]any
			json.Unmarshal(p, &v)
			if v["url"] != "https://example.invalid/?message=7" || v["device_key"] != "SYNTHETIC-KEY" {
				t.Error("Bark payload")
			}
			w.Header().Set("Location", "https://example.invalid")
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		s.Config.BarkBaseURL = receiver.URL
		if got := s.push(context.Background(), store.Notification{MessageID: 7, Slot: 2, Body: "合成"}); got != tc.want {
			t.Fatalf("push %d: %v", tc.status, got)
		}
		receiver.Close()
	}
}

func TestNotificationWorkerPersistsAcceptedResult(t *testing.T) {
	s, _ := fixture(t, 0)
	received := make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"code":200}`)); received <- struct{}{} }))
	defer receiver.Close()
	s.Config.BarkKey = "SYNTHETIC-KEY"
	s.Config.BarkBaseURL = receiver.URL
	s.Config.PublicURL = "http://127.0.0.1:8080"
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, Phone: "TEST-SENDER", Body: "合成", SMSTS: 1700000000}
	p, _ := json.Marshal(e)
	if err := s.Store.Ingest(context.Background(), e, p, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not push")
	}
	deadline := time.Now().Add(time.Second)
	for {
		var state string
		if err := s.Store.DB.QueryRow(`SELECT state FROM notifications LIMIT 1`).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "sent" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("accepted result not persisted")
		}
		time.Sleep(time.Millisecond)
	}
}
