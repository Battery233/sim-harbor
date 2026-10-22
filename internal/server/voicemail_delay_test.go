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
)

func TestMissingAndLateVoicemailBarkHaveDistinctCopyAndSameCallLink(t *testing.T) {
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
	s, _ := fixture(t, 0, func(c *config.Config) {
		c.BarkKey = "SYNTHETIC"
		c.BarkBaseURL = receiver.URL
		c.PublicURL = "https://example.invalid"
		enableVoicemail(c, strings.Repeat("ab", 16), 1)
	})
	start := time.Now().Unix() - 20
	connected := protocol.Number(start + 2)
	event := protocol.Event{DeviceID: "SIM-TEST", Type: 603, Slot: 1, Phone: "anonymous", ICCID: "SYNTHETIC-CARD", TelStartTS: protocol.Number(start), TelConnectedTS: &connected, TelEndTS: protocol.Number(start + 10)}
	raw, _ := json.Marshal(event)
	if err := s.Store.Ingest(context.Background(), event, raw, true, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE calls SET voicemail_due_at=? WHERE id=1`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	receive := func() map[string]any {
		t.Helper()
		select {
		case p := <-payloads:
			return p
		case <-time.After(5 * time.Second):
			t.Fatal("no push")
			return nil
		}
	}
	first := receive()
	if !strings.Contains(first["title"].(string), "录音未收到") || !strings.Contains(first["body"].(string), "来电：匿名来电") || !strings.Contains(first["body"].(string), "3 分钟") {
		t.Fatal("missing reminder copy")
	}
	// Serialize with the worker so Bark acceptance is persisted before upload.
	s.notificationGate.Lock()
	_, _, _, err := s.Store.SaveVoicemail(context.Background(), s.Config.DataDir, []byte("#!AMR\nSYNTHETIC"), 1, "anonymous", start, time.Now().Unix())
	s.notificationGate.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	late := receive()
	if !strings.Contains(late["title"].(string), "迟到的录音已收到") || !strings.Contains(late["body"].(string), "此前未收到") || late["url"] != first["url"] {
		t.Fatal("late reminder copy/link")
	}
	select {
	case <-payloads:
		t.Fatal("extra push")
	case <-time.After(100 * time.Millisecond):
	}
}
