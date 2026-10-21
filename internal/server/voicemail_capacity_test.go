package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
)

func capacityFixture(t *testing.T, handler http.HandlerFunc) *Server {
	t.Helper()
	receiver := httptest.NewServer(handler)
	t.Cleanup(receiver.Close)
	s, _ := fixture(t, 1, func(c *config.Config) {
		publicRecording(c)
		c.Environment = "local"
		c.BarkKey = "SYNTHETIC-CAPACITY-KEY"
		c.BarkBaseURL = receiver.URL
		c.PublicURL = "https://example.invalid"
		off := false
		c.StatusAlertsEnabled = &off
	})
	return s
}

func capacityFile(t *testing.T, s *Server, size int64) {
	t.Helper()
	dir := filepath.Join(s.Config.DataDir, "voicemail")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "SYNTHETIC.amr"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(size)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal("synthetic size setup failed", err, closeErr)
	}
}

func TestVoicemailCapacityThresholdsPersistAndCoalesce(t *testing.T) {
	var pushes atomic.Int32
	s := capacityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			t.Error("invalid synthetic payload")
		}
		if p["title"] != "录音存储空间提醒" || p["group"] != "SIM 运行提醒" || strings.Contains(p["body"].(string), "SYNTHETIC.amr") {
			t.Error("wrong capacity payload")
		}
		pushes.Add(1)
		w.Write([]byte(`{"code":200}`))
	})
	if s.Config.VoicemailStorageLimit() != 512<<20 {
		t.Fatal("wrong default capacity")
	}
	now := time.Unix(1800000000, 0)
	observe := func(mib int64, expected int32) {
		t.Helper()
		capacityFile(t, s, mib<<20)
		if err := s.observeVoicemailCapacity(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		if pushes.Load() != expected {
			t.Fatal("unexpected capacity reminder count", mib, pushes.Load(), expected)
		}
		now = now.Add(time.Minute)
	}
	observe(255, 0)
	observe(256, 1)
	observe(287, 1)
	observe(288, 2)
	// A replacement server reads the same checkpoint after restart.
	restarted := New(s.Config, s.Store)
	restarted.client = s.client
	s = restarted
	observe(288, 2)
	observe(400, 3)
	observe(415, 3)
	observe(416, 4)
	observe(512, 5)
	// A shrink above half does not replay old bands. Below half rearms reminders.
	observe(288, 5)
	observe(200, 5)
	observe(256, 6)
	s.Config.VoicemailMaxStorageMiB = 1024
	observe(256, 6)
	observe(512, 7)
	path := filepath.Join(s.Config.DataDir, "voicemail-capacity.private.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("checkpoint not private", err)
	}
}

func TestVoicemailCapacityRetryIsPersistentAndProtected(t *testing.T) {
	var pushes atomic.Int32
	var succeed atomic.Bool
	s := capacityFixture(t, func(w http.ResponseWriter, r *http.Request) {
		pushes.Add(1)
		if !succeed.Load() {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"code":200}`))
	})
	capacityFile(t, s, 256<<20)
	now := time.Unix(1800000000, 0)
	if err := s.observeVoicemailCapacity(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.Config, s.Store)
	restarted.client = s.client
	s = restarted
	if err := s.observeVoicemailCapacity(context.Background(), now.Add(29*time.Second)); err != nil {
		t.Fatal(err)
	}
	if pushes.Load() != 1 {
		t.Fatal("restart bypassed backoff")
	}
	succeed.Store(true)
	if err := s.observeVoicemailCapacity(context.Background(), now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.observeVoicemailCapacity(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if pushes.Load() != 2 {
		t.Fatal("retry was lost or sent twice")
	}
	capacityFile(t, s, 288<<20)
	if err := s.Store.HoldRestoredData(context.Background(), strings.Repeat("a", 64), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := s.observeVoicemailCapacity(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if pushes.Load() != 2 {
		t.Fatal("restore protection bypassed")
	}
}

func TestVoicemailCapacityWorkerAndDisabledModes(t *testing.T) {
	payloads := make(chan struct{}, 4)
	s := capacityFixture(t, func(w http.ResponseWriter, r *http.Request) { payloads <- struct{}{}; w.Write([]byte(`{"code":200}`)) })
	capacityFile(t, s, 256<<20)
	for _, edit := range []func(){func() { s.Config.VoicemailEnabled = false }, func() { s.Config.VoicemailEnabled = true; s.Config.VoicemailUploadMode = "lan" }, func() { s.Config.VoicemailUploadMode = "public"; s.Config.BarkKey = "" }} {
		edit()
		if err := s.observeVoicemailCapacity(context.Background(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(payloads) != 0 {
		t.Fatal("disabled mode pushed")
	}
	s.Config.BarkKey = "SYNTHETIC-CAPACITY-KEY"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-payloads:
	case <-time.After(3 * time.Second):
		t.Fatal("background worker did not notify")
	}
}

func TestVoicemailCapacityCheckpointFailureDoesNotSend(t *testing.T) {
	var pushes atomic.Int32
	s := capacityFixture(t, func(w http.ResponseWriter, r *http.Request) { pushes.Add(1); w.Write([]byte(`{"code":200}`)) })
	capacityFile(t, s, 256<<20)
	path := filepath.Join(s.Config.DataDir, "voicemail-capacity.private.json")
	if err := os.WriteFile(path, []byte("corrupt synthetic checkpoint"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.observeVoicemailCapacity(context.Background(), time.Now()); err == nil || pushes.Load() != 0 {
		t.Fatal("corrupt state replayed reminder")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.observeVoicemailCapacity(context.Background(), time.Now()); err == nil || pushes.Load() != 0 {
		t.Fatal("checkpoint persistence not required")
	}
}
