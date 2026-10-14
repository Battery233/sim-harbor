package server

import (
	"encoding/json"
	"github.com/Battery233/sim/internal/config"
	"github.com/Battery233/sim/internal/protocol"
	"github.com/Battery233/sim/internal/store"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func action(s *Server, path string, v any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(v)
	r := httptest.NewRequest("POST", "http://example.com/api"+path, strings.NewReader(string(b)))
	if s.Config.AdminToken == "" {
		r.Host = "127.0.0.1:8080"
	}
	r.Header.Set("Content-Type", "application/json")
	if s.Config.AdminToken != "" {
		r.Header.Set("Authorization", "Bearer "+s.Config.AdminToken)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func result(t *testing.T, w *httptest.ResponseRecorder) store.Outgoing {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("action %d: %s", w.Code, w.Body)
	}
	var o store.Outgoing
	if e := json.Unmarshal(w.Body.Bytes(), &o); e != nil {
		t.Fatal(e)
	}
	return o
}
func mockBoard(t *testing.T, s *Server, l net.Listener, changed *atomic.Bool) (*atomic.Int32, net.Conn) {
	c := connect(t, l)
	c.SetDeadline(time.Time{})
	codec := s.codec()
	reader := protocol.NewReader(c)
	send(t, c, codec, map[string]any{"devId": "SIM-TEST", "type": 100})
	read(t, reader, codec)
	count := new(atomic.Int32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			p, e := reader.Next()
			if e != nil {
				return
			}
			if codec.Mode == 2 {
				p, e = codec.Open(p)
				if e != nil {
					return
				}
			}
			var cmd map[string]any
			if json.Unmarshal(p, &cmd) != nil {
				return
			}
			switch cmd["cmd"] {
			case "stat":
				icc := "SYNTHETIC-ICC-0001"
				if changed.Load() {
					icc = "SYNTHETIC-ICC-0002"
				}
				send(t, c, codec, map[string]any{"type": 402, "subType": 46, "code": 0, "tid": cmd["tid"], "slotInfo": map[string]any{"slot1_sta": "OK", "sim1_iccId": icc}})
			case "sendsms":
				count.Add(1)
				if cmd["p1"] != "1" || cmd["p2"] != "000" || cmd["p3"] != "SYNTHETIC\n正文" {
					t.Error("wrong sendsms parameters")
				}
				send(t, c, codec, map[string]any{"type": 502, "slot": 1, "phNum": cmd["p2"], "smsBd": cmd["p3"], "tid": cmd["tid"], "smsSendMr": []int{7}})
				send(t, c, codec, map[string]any{"type": 503, "slot": 1, "phNum": cmd["p2"], "smsSendMr": 7, "smsSt": 0, "smsScts": time.Now().Unix(), "smsDt": time.Now().Unix()})
			}
		}
	}()
	t.Cleanup(func() {
		c.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("mock stalled")
		}
	})
	return count, c
}
func TestOutgoingAPIOnceLocalAndCloud(t *testing.T) {
	for _, env := range []string{"local", "cloud"} {
		t.Run(env, func(t *testing.T) {
			mode := 0
			if env == "cloud" {
				mode = 2
			}
			s, l := fixture(t, mode, func(c *config.Config) {
				c.Environment = env
				c.OutboundEnabled = true
				if env == "cloud" {
					c.AdminToken = strings.Repeat("t", 24)
				}
			})
			changed := new(atomic.Bool)
			count, c := mockBoard(t, s, l, changed)
			id := strings.Repeat("a", 32)
			in := map[string]any{"id": id, "slot": 1, "phone": "000", "body": "SYNTHETIC\n正文"}
			o := result(t, action(s, "/outgoing/prepare", in))
			if o.State != "prepared" || o.CardTail != "0001" || count.Load() != 0 {
				t.Fatal("preview sent", o)
			}
			wrong := action(s, "/outgoing/"+id+"/confirm", map[string]any{"confirmed": true, "phone": "001", "slot": 1})
			if wrong.Code != 409 {
				t.Fatal("wrong target confirmed")
			}
			confirm := map[string]any{"confirmed": true, "phone": "000", "slot": 1}
			var wg sync.WaitGroup
			for i := 0; i < 12; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					w := action(s, "/outgoing/"+id+"/confirm", confirm)
					if w.Code != 200 && w.Code != 409 {
						t.Errorf("concurrent confirm %d %s", w.Code, w.Body)
					}
				}()
			}
			wg.Wait()
			deadline := time.Now().Add(2 * time.Second)
			for {
				o, e := s.Store.Outgoing(t.Context(), id, time.Now().Unix())
				if e != nil {
					t.Fatal(e)
				}
				if o.Delivery == "delivered" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("callbacks not correlated", o)
				}
				time.Sleep(time.Millisecond)
			}
			if count.Load() != 1 {
				t.Fatal("duplicate physical send", count.Load())
			}
			c.Close()
			disabled := s.Config
			disabled.OutboundEnabled = false
			s = New(disabled, s.Store)
			for i := 0; i < 3; i++ {
				o = result(t, action(s, "/outgoing/"+id+"/confirm", confirm))
				if o.DispatchedAt == 0 {
					t.Fatal("lost-response recovery")
				}
			}
			if count.Load() != 1 {
				t.Fatal("resend after disconnect")
			}
			in["body"] = "CHANGED"
			if w := action(s, "/outgoing/prepare", in); w.Code != 409 {
				t.Fatal("ID reused with changed content")
			}
		})
	}
}
func TestOutgoingDefaultOffAndCardChange(t *testing.T) {
	s, _ := fixture(t, 0)
	id := strings.Repeat("b", 32)
	in := map[string]any{"id": id, "slot": 1, "phone": "000", "body": "SYNTHETIC\n正文"}
	if w := action(s, "/outgoing/prepare", in); w.Code != 409 || !strings.Contains(w.Body.String(), "outbound_disabled") {
		t.Fatal(w.Body)
	}
	s, l := fixture(t, 0, func(c *config.Config) { c.OutboundEnabled = true })
	changed := new(atomic.Bool)
	count, _ := mockBoard(t, s, l, changed)
	result(t, action(s, "/outgoing/prepare", in))
	changed.Store(true)
	w := action(s, "/outgoing/"+id+"/confirm", map[string]any{"confirmed": true, "phone": "000", "slot": 1})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "card_changed") || count.Load() != 0 {
		t.Fatal("changed card sent", w.Body)
	}
}

type brokenConn struct {
	net.Conn
	calls  int
	closed bool
	zero   bool
	bytes  int
}

func (c *brokenConn) SetWriteDeadline(time.Time) error { return nil }
func (c *brokenConn) Close() error                     { c.closed = true; return nil }
func (c *brokenConn) Write(p []byte) (int, error) {
	c.calls++
	if c.zero {
		return 0, nil
	}
	n := min(4, len(p))
	c.bytes += n
	return n, io.ErrUnexpectedEOF
}
func TestOutgoingWriteNeverRetriesPartialOrZero(t *testing.T) {
	s := New(config.Config{}, nil)
	for _, zero := range []bool{false, true} {
		c := &brokenConn{zero: zero}
		if e := s.write(&session{conn: c}, map[string]string{"cmd": "sendsms"}); e == nil || c.calls != 1 || !c.closed {
			t.Fatal("write retried or stalled", c)
		}
	}
}
