package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
)

func TestUnconfiguredBarkStillStoresMessagesCallsAndUploadedAudio(t *testing.T) {
	token := strings.Repeat("ab", 16)
	s, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2) })
	board := connect(t, listener)
	start := time.Now().Unix() - 20
	send(t, board, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	send(t, board, s.codec(), map[string]any{"type": 501, "slot": 2, "smsTs": start, "phNum": "SYNTHETIC-CALLER", "smsBd": "SYNTHETIC-MESSAGE"})
	send(t, board, s.codec(), map[string]any{"type": 603, "slot": 2, "telStartTs": start, "telConnectedTs": start + 2, "telEndTs": start + 15, "phNum": "SYNTHETIC-CALLER"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		var records int
		if err := s.Store.DB.QueryRow(`SELECT (SELECT count(*) FROM messages)+(SELECT count(*) FROM calls)`).Scan(&records); err == nil && records == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("message or call was not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	if w := postVoicemail(s, token, []byte("#!AMR\nSYNTHETIC"), start, "127.0.0.1:9"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var tasks int
	if err := s.Store.DB.QueryRow(`SELECT (SELECT count(*) FROM notifications)+(SELECT count(*) FROM call_notifications)`).Scan(&tasks); err != nil || tasks != 0 {
		t.Fatal("unconfigured Bark created a task", tasks, err)
	}
	if file, err := s.Store.VoicemailFile(context.Background(), 1); err != nil || file.Name == "" {
		t.Fatal("recording was not saved and linked", err)
	}
}
