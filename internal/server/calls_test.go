package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestTCPIncomingCallRecordAndMissedBarkNoCallControl(t *testing.T) {
	payloads := make(chan map[string]any, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		payloads <- p
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	s, l := fixture(t, 1, func(c *config.Config) {
		c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC", receiver.URL, "https://example.invalid"
	})
	board := connect(t, l)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	start := time.Now().Unix()
	send(t, board, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 601, "slot": 2, "iccId": "SYNTHETIC-ICC", "scName": "First name", "phNum": "anonymous", "telStartTs": start, "telConnectedTs": 0})
	readCalls := func() []store.Call {
		w := request(s, "GET", "http://example.com/api/calls", "", "")
		var p struct {
			Items []store.Call `json:"items"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil {
			t.Fatal("call API failed", w.Code)
		}
		return p.Items
	}
	deadline := time.Now().Add(time.Second)
	for len(readCalls()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("ringing not recorded")
		}
		time.Sleep(time.Millisecond)
	}
	if rows := readCalls(); rows[0].State != "ringing" || rows[0].RingSeconds != nil {
		t.Fatal("ringing guessed ended")
	}
	select {
	case <-payloads:
		t.Fatal("ringing sent Bark before end")
	case <-time.After(100 * time.Millisecond):
	}
	send(t, board, s.codec(), map[string]any{"type": 603, "slot": 2, "iccId": "SYNTHETIC-ICC", "scName": "Changed name", "phNum": "anonymous", "telStartTs": start, "telConnectedTs": 0, "telEndTs": start + 23})
	deadline = time.Now().Add(time.Second)
	for readCalls()[0].State != "missed" {
		if time.Now().After(deadline) {
			t.Fatal("final report not merged")
		}
		time.Sleep(time.Millisecond)
	}
	rows := readCalls()
	if len(rows) != 1 || rows[0].RingSeconds == nil || *rows[0].RingSeconds != 23 {
		t.Fatal("wrong call duration")
	}
	for _, path := range []string{"/api/calls?card=" + rows[0].CardID, "/api/calls/1", "/api/call-groups", "/api/call-groups/1", "/api/calls?group=1"} {
		w := request(s, "GET", "http://example.com"+path, "", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "SYNTHETIC-ICC") {
			t.Fatal("call route disclosed private identity")
		}
	}
	select {
	case p := <-payloads:
		if p["title"] != "First name 未接来电" || !strings.Contains(p["body"].(string), "匿名来电") || !strings.Contains(p["body"].(string), "响铃 23 秒") || !strings.HasSuffix(p["url"].(string), "/?call=1") {
			t.Fatal("wrong call notification")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("call notification absent")
	}
	send(t, board, s.codec(), map[string]any{"type": 603, "slot": 2, "iccId": "SYNTHETIC-ICC", "phNum": "anonymous", "telStartTs": start, "telConnectedTs": 0, "telEndTs": start + 23})
	select {
	case <-payloads:
		t.Fatal("replay sent a second Bark")
	case <-time.After(100 * time.Millisecond):
	}
	var jobs int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM call_notifications`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("one call created multiple jobs", jobs, err)
	}
	for _, path := range []string{"/api/call-groups?before=-1", "/api/call-groups/0", "/api/calls?group=0", "/api/calls?group=1&card=unknown"} {
		if w := request(s, "GET", "http://example.com"+path, "", ""); w.Code != 400 {
			t.Fatal("invalid group filter accepted", path, w.Code)
		}
	}
	// No ACK/control frame is needed for an incoming call. Only the synthetic
	// TCP peer is inspected; no phone is dialled anywhere in this test.
	board.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if p, err := protocol.NewReader(board).Next(); err == nil {
		t.Fatal("incoming report caused device command", len(p))
	}
	if w := action(s, "/calls/1/answer", map[string]bool{"confirmed": true}); w.Code != 404 && w.Code != 405 {
		t.Fatal("unexpected phone control route")
	}
}

func TestTCPCallTimeoutBarkAndLateEndNoSecondPush(t *testing.T) {
	payloads := make(chan map[string]any, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		payloads <- p
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	s, l := fixture(t, 1, func(c *config.Config) {
		c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC", receiver.URL, "https://example.invalid"
	})
	board := connect(t, l)
	start := time.Now().Unix()
	send(t, board, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 601, "slot": 1, "iccId": "SYNTHETIC-ICC", "scName": "Test card", "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": 0})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := s.Store.Call(context.Background(), 1, start); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("missing start")
		}
		time.Sleep(time.Millisecond)
	}
	// Fault/clock injection is confined to this isolated synthetic database.
	if _, err := s.Store.DB.Exec(`UPDATE calls SET timeout_at=? WHERE id=1`, start-1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case p := <-payloads:
		if p["title"] != "Test card 来电提示" || !strings.Contains(p["body"].(string), "曾来电，结束时间未确认") || strings.Contains(p["body"].(string), "响铃") {
			t.Fatal("timeout fabricated final duration", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fallback absent")
	}
	deadline = time.Now().Add(time.Second)
	for {
		c, _ := s.Store.Call(context.Background(), 1, start)
		if c.NotificationState == "sent" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("acceptance not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	send(t, board, s.codec(), map[string]any{"type": 603, "slot": 1, "iccId": "SYNTHETIC-ICC", "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": 0, "telEndTs": start + 23})
	deadline = time.Now().Add(time.Second)
	for {
		c, _ := s.Store.Call(context.Background(), 1, start+24)
		if c.State == "missed" {
			if c.RingSeconds == nil || *c.RingSeconds != 23 || c.NotificationKind != "timeout" {
				t.Fatal("late end lost fallback evidence")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late end not merged")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-payloads:
		t.Fatal("late end sent second notification")
	case <-time.After(1200 * time.Millisecond):
	}
}

func TestChineseAnonymousCallSearchAPI(t *testing.T) {
	s, _ := fixture(t, 1)
	now := time.Now().Unix()
	zero := protocol.Number(0)
	for i, phone := range []string{"anonymous", "SYNTHETIC-CALLER"} {
		e := protocol.Event{DeviceID: "SIM-TEST", Type: 603, Slot: 1, ICCID: "SYNTHETIC-CARD", Phone: phone, TelStartTS: protocol.Number(now + int64(i)*30), TelConnectedTS: &zero, TelEndTS: protocol.Number(now + int64(i)*30 + 5)}
		raw, _ := json.Marshal(e)
		if err := s.Store.Ingest(context.Background(), e, raw, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/api/calls?q=%E5%8C%BF%E5%90%8D", "/api/call-groups?q=%E5%8C%BF%E5%90%8D", "/api/call-groups?q=ANONYMOUS"} {
		w := request(s, "GET", "http://example.com"+path, "", "")
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || len(p.Items) != 1 {
			t.Fatal("alias API filter failed", path, w.Code)
		}
	}
}
