package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestDeleteMessageRequiresConfirmationAuthAndOrigin(t *testing.T) {
	token := strings.Repeat("t", 24)
	s, _ := fixture(t, 0, func(c *config.Config) { c.AdminToken = token })
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, Phone: "TEST", Body: "SYNTHETIC", SMSTS: 1700000000}
	raw, _ := json.Marshal(e)
	if err := s.Store.Ingest(context.Background(), e, raw, true); err != nil {
		t.Fatal(err)
	}
	call := func(id, body, auth, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://example.com/api/messages/"+id+"/delete", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", auth)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		id, body, auth, origin string
		code                   int
	}{{"1", `{"confirmed":true}`, "", "", 401}, {"1", `{"confirmed":true}`, "Bearer " + token, "http://other.invalid", 403}, {"1", `{}`, "Bearer " + token, "", 400}, {"1", `{"confirmed":false}`, "Bearer " + token, "", 400}, {"1", `{"confirmed":true,"extra":1}`, "Bearer " + token, "", 400}, {"0", `{"confirmed":true}`, "Bearer " + token, "", 400}, {"999", `{"confirmed":true}`, "Bearer " + token, "", 404}} {
		if w := call(tc.id, tc.body, tc.auth, tc.origin); w.Code != tc.code {
			t.Fatal(tc, w.Code)
		}
	}
	if _, err := s.Store.Message(context.Background(), 1); err != nil {
		t.Fatal("unconfirmed operation removed SMS")
	}
	for i := 0; i < 2; i++ {
		if w := call("1", `{"confirmed":true}`, "Bearer "+token, ""); w.Code != 200 {
			t.Fatal("idempotent delete", w.Code)
		}
	}
	if w := request(s, "GET", "http://example.com/api/messages/1", "Bearer "+token, ""); w.Code != 404 {
		t.Fatal("deleted deep link", w.Code)
	}
	for _, route := range []string{"/api/messages", "/api/messages/export?format=json"} {
		w := request(s, "GET", "http://example.com"+route, "Bearer "+token, "")
		var v struct{ Items []any }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || len(v.Items) != 0 {
			t.Fatal("deleted list/export", route, w.Code)
		}
	}
	var outgoing int
	s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&outgoing)
	if outgoing != 0 {
		t.Fatal("delete created outbound command")
	}
}

func TestDeleteSerializesWithAlreadySendingNotification(t *testing.T) {
	s, _ := fixture(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	defer once.Do(func() { close(release) })
	s.Config.BarkKey = "SYNTHETIC-KEY"
	s.Config.BarkBaseURL = receiver.URL
	s.Config.PublicURL = "https://example.invalid"
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, Phone: "TEST", Body: "SYNTHETIC", SMSTS: 1700000000}
	raw, _ := json.Marshal(e)
	if err := s.Store.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("push did not start")
	}
	deleted := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "http://localhost/api/messages/1/delete", bytes.NewBufferString(`{"confirmed":true}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		deleted <- w.Code
	}()
	select {
	case <-deleted:
		t.Fatal("delete bypassed in-flight notification")
	case <-time.After(25 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case code := <-deleted:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delete stuck after push result")
	}
	var state string
	var at int64
	s.Store.DB.QueryRow(`SELECT state,accepted_at FROM notifications WHERE message_id=1`).Scan(&state, &at)
	if state != "sent" || at <= 0 {
		t.Fatal("in-flight success evidence lost")
	}
	cancel()
	<-done
}
