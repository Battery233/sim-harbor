package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func voicemailURL(token string) string {
	return "http://192.168.31.10:18880/api/voicemail/amr/" + token + "?slot={{slot}}&start={{telStartTs}}&phone={{phNum|$urlEncode()}}"
}

func enableVoicemail(c *config.Config, token string, slots ...int) {
	c.VoicemailEnabled = true
	c.VoicemailSlots = slots
	c.VoicemailTTS = "请在提示音后留言"
	c.VoicemailAMRURL = voicemailURL(token)
}

func TestVoicemailUnansweredCallSendsNoCommandsAndSkipsMissedBark(t *testing.T) {
	payloads := make(chan map[string]any, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		payloads <- p
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	s, l := fixture(t, 1, func(c *config.Config) {
		c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC", receiver.URL, "https://example.invalid"
		enableVoicemail(c, strings.Repeat("ab", 16), 2)
	})
	board := connect(t, l)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	start := time.Now().Unix()
	send(t, board, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 601, "slot": 2, "iccId": "SYNTHETIC-ICC", "scName": "First name", "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": 0})
	send(t, board, s.codec(), map[string]any{"type": 603, "slot": 2, "iccId": "SYNTHETIC-ICC", "scName": "First name", "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": 0, "telEndTs": start + 8})
	deadline := time.Now().Add(time.Second)
	for {
		call, err := s.Store.Call(context.Background(), 1, start)
		if err == nil && call.State == "missed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("call not recorded", err)
		}
		time.Sleep(time.Millisecond)
	}
	var deadlineAt int64
	var jobs int
	if err := s.Store.DB.QueryRow(`SELECT timeout_at FROM calls WHERE id=1`).Scan(&deadlineAt); err != nil || deadlineAt != 0 {
		t.Fatal("voicemail slot armed missed timeout", deadlineAt, err)
	}
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM call_notifications`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("missed notification queued", jobs, err)
	}
	select {
	case p := <-payloads:
		t.Fatal("missed bark on voicemail slot", p["title"])
	case <-time.After(200 * time.Millisecond):
	}
	board.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if p, err := protocol.NewReader(board).Next(); err == nil {
		t.Fatal("unanswered voicemail call sent a device command", len(p))
	}
	if w := action(s, "/calls/1/answer", map[string]bool{"confirmed": true}); w.Code != 404 && w.Code != 405 {
		t.Fatal("call control route", w.Code)
	}
}

func TestVoicemailConnectedSendsTTSOnce(t *testing.T) {
	s, l := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, strings.Repeat("ab", 16), 2) })
	board := connect(t, l)
	send(t, board, s.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, s)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	start := time.Now().Unix()
	send(t, board, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 601, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": 0})
	expectNoBoardCommand(t, board)
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": 0})
	expectNoBoardCommand(t, board)
	connected := start + 3
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": connected})
	cmd := read(t, protocol.NewReader(board), s.codec())
	assertGreeting(t, cmd, s.Config.VoicemailTTS)
	send(t, board, s.codec(), map[string]any{"type": 402, "subType": 26, "code": 0, "tid": cmd["tid"]})
	send(t, board, s.codec(), map[string]any{"type": 401, "subType": 26, "code": 0, "tid": cmd["tid"]})
	waitVoicemailLog(t, s, "voicemail_greeting")
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start, "telConnectedTs": connected})
	expectNoBoardCommand(t, board)
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 1, "phNum": "SYNTHETIC-CALLER", "telStartTs": start + 30, "telConnectedTs": start + 31})
	expectNoBoardCommand(t, board)
	send(t, board, s.codec(), map[string]any{"type": 602, "slot": 2, "phNum": "SYNTHETIC-CALLER", "telStartTs": start + 40, "telConnectedTs": start + 41})
	cmd = read(t, protocol.NewReader(board), s.codec())
	assertGreeting(t, cmd, s.Config.VoicemailTTS)
	send(t, board, s.codec(), map[string]any{"type": 401, "subType": 26, "code": 200, "tid": cmd["tid"], "note": "No call in progress"})
	waitVoicemailLog(t, s, "voicemail_greeting_failed")
	expectNoBoardCommand(t, board)
}

func assertGreeting(t *testing.T, cmd map[string]any, text string) {
	t.Helper()
	if cmd["cmd"] != "telstarttts" || cmd["p1"] != float64(2) || cmd["p2"] != text || cmd["p3"] != float64(1) || cmd["p5"] != float64(0) || cmd["p4"] != nil {
		t.Fatal("greeting command", cmd)
	}
	for _, name := range []string{"telanswer", "telstartrecord", "teldial", "telhangup"} {
		if cmd["cmd"] == name {
			t.Fatal(name)
		}
	}
}

func expectNoBoardCommand(t *testing.T, board net.Conn) {
	t.Helper()
	board.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if p, err := protocol.NewReader(board).Next(); err == nil {
		t.Fatal("unexpected device command", len(p))
	}
	board.SetDeadline(time.Now().Add(20 * time.Second))
}

func waitVoicemailLog(t *testing.T, s *Server, code string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 100)
		for _, row := range rows {
			if row.Code == code {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("missing diagnostic", code)
}

func TestVoicemailUploadStoresLinksAndBarksOnce(t *testing.T) {
	payloads := make(chan map[string]any, 3)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		payloads <- p
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	token := strings.Repeat("cd", 16)
	s, l := fixture(t, 1, func(c *config.Config) {
		c.AdminToken = strings.Repeat("T", 32)
		c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC", receiver.URL, "https://example.invalid"
		enableVoicemail(c, token, 2)
	})
	board := connect(t, l)
	start := time.Now().Unix()
	send(t, board, s.codec(), map[string]any{"devId": "SIM-TEST", "type": 601, "slot": 2, "iccId": "SYNTHETIC-ICC", "scName": "First name", "phNum": "SYNTHETIC-CALLER", "telStartTs": start})
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := s.Store.Call(context.Background(), 1, start); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("missing call")
		}
		time.Sleep(time.Millisecond)
	}
	audio := []byte("#!AMR\nSYNTHETIC")
	if w := postVoicemail(s, token, audio, start, "192.0.2.1:9"); w.Code != 403 {
		t.Fatal("outside source accepted", w.Code, w.Body)
	}
	if w := postVoicemail(s, strings.Repeat("ee", 16), audio, start, "127.0.0.1:9"); w.Code != 401 {
		t.Fatal("wrong token", w.Code)
	}
	w := postVoicemail(s, token, audio, start, "127.0.0.1:9")
	var saved map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved["errcode"] != float64(0) || saved["media_id"] == "" || saved["type"] != "amr" {
		t.Fatal(w.Code, w.Body)
	}
	again := postVoicemail(s, token, audio, start, "127.0.0.1:9")
	var duplicate map[string]any
	json.Unmarshal(again.Body.Bytes(), &duplicate)
	if again.Code != 200 || duplicate["media_id"] != saved["media_id"] {
		t.Fatal("duplicate changed media id", again.Code, again.Body)
	}
	var rows int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM voicemails`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("duplicate stored twice", rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunNotifications(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case p := <-payloads:
		body, _ := p["body"].(string)
		if p["title"] != "First name 有留言" || !strings.Contains(body, "有留言") || strings.Contains(body, "未接") || strings.Contains(body, "响铃") || !strings.HasSuffix(p["url"].(string), "/?call=1") {
			t.Fatal("wrong voicemail bark", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("voicemail bark absent")
	}
	select {
	case p := <-payloads:
		t.Fatal("second bark", p["title"])
	case <-time.After(200 * time.Millisecond):
	}
	down := request(s, "GET", "http://example.com/api/calls/1/voicemail", "Bearer "+s.Config.AdminToken, "")
	if down.Code != 200 || down.Header().Get("Content-Type") != "audio/amr" || !strings.Contains(down.Header().Get("Content-Disposition"), "attachment;") || down.Body.String() != string(audio) {
		t.Fatal("download", down.Code, down.Header(), down.Body)
	}
	if open := request(s, "GET", "http://example.com/api/calls/1/voicemail", "", ""); open.Code != 401 {
		t.Fatal("download without credential", open.Code)
	}
	board.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if p, err := protocol.NewReader(board).Next(); err == nil {
		t.Fatal("upload path sent a device command", len(p))
	}
}

func TestVoicemailUploadRejectsWhenDisabledOrAllowlistOff(t *testing.T) {
	token := strings.Repeat("ab", 16)
	s, _ := fixture(t, 1)
	if w := postVoicemail(s, token, []byte("#!AMR\nSYNTHETIC"), 1700000000, "127.0.0.1:9"); w.Code != 404 {
		t.Fatal("disabled upload", w.Code)
	}
	off := false
	s, _ = fixture(t, 1, func(c *config.Config) {
		enableVoicemail(c, token, 1)
		c.DeviceAllowlistEnabled = &off
	})
	if w := postVoicemail(s, token, []byte("#!AMR\nSYNTHETIC"), 1700000000, "127.0.0.1:9"); w.Code != 403 {
		t.Fatal("allowlist off accepted upload", w.Code)
	}
}

func TestVoicemailSettingsConfirmAndBoardWhitelist(t *testing.T) {
	s := settingsFixture(t)
	snap, err := s.Settings.Snapshot()
	if err != nil || snap.Values.VoicemailEnabled {
		t.Fatal("default on", err)
	}
	token := strings.Repeat("ab", 16)
	values := snap.Values
	values.VoicemailEnabled = true
	values.VoicemailSlots = []int{1}
	values.VoicemailTTS = "请留言"
	values.VoicemailAMRURL = "http://lab.example.nip.io:18880/api/voicemail/amr/" + token
	body := map[string]any{"values": values, "secrets": map[string]any{}, "revision": snap.Revision, "confirmed": true, "voicemail_confirmed": true}
	if w := settingsRequest(s, "PUT", "/api/settings", body, true); w.Code != 422 {
		t.Fatal("public name accepted", w.Code, w.Body)
	}
	values.VoicemailAMRURL = voicemailURL(token)
	body["values"] = values
	body["voicemail_confirmed"] = false
	if w := settingsRequest(s, "PUT", "/api/settings", body, true); w.Code != 400 || !strings.Contains(w.Body.String(), "voicemail_confirmation_required") {
		t.Fatal("enable without secondary confirm", w.Code, w.Body)
	}
	body["voicemail_confirmed"] = true
	w := settingsRequest(s, "PUT", "/api/settings", body, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var saved config.SettingsSnapshot
	json.Unmarshal(w.Body.Bytes(), &saved)
	body["revision"] = saved.Revision
	body["voicemail_confirmed"] = false
	if w = settingsRequest(s, "PUT", "/api/settings", body, true); w.Code != 200 {
		t.Fatal("already enabled required another confirm", w.Code, w.Body)
	}
	if s.Config.VoicemailEnabled {
		t.Fatal("save changed runtime before restart")
	}

	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 1) })
	if w := action(runtime, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "enable", "confirmed": true, "expected": map[string]any{"amr_url": "", "slots": []any{}}}); w.Code != 409 {
		t.Fatal("offline apply", w.Code)
	}
	disabled, boardListener := fixture(t, 1)
	board := connect(t, boardListener)
	send(t, board, disabled.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, disabled)
	if w := action(disabled, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "enable", "confirmed": true, "expected": idleVoicemail()}); w.Code != 409 || !strings.Contains(w.Body.String(), "voicemail_disabled") {
		t.Fatal("disabled apply", w.Code, w.Body)
	}
	board.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if p, err := protocol.NewReader(board).Next(); err == nil {
		t.Fatal("disabled apply sent a command", len(p))
	}

	board = connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	reader := protocol.NewReader(board)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- action(runtime, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "enable", "confirmed": true, "expected": driftedVoicemail()})
	}()
	for i := 0; i < 4; i++ {
		cmd := read(t, reader, runtime.codec())
		if cmd["cmd"] == "telanswer" || cmd["cmd"] == "telstartrecord" || cmd["cmd"] == "teldial" || cmd["cmd"] == "telhangup" {
			t.Fatal("call control command", cmd["cmd"])
		}
		val := ""
		if cmd["cmd"] == "slotcim" {
			val = "1:1;2:0"
		}
		replyBoard(t, board, runtime, cmd, val)
	}
	if w := <-done; w.Code != 409 {
		t.Fatal("drift accepted", w.Code, w.Body)
	}
	board.SetDeadline(time.Now().Add(20 * time.Second))
	board.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := reader.Next(); err == nil {
		t.Fatal("mismatch continued into a write")
	}
	board.SetDeadline(time.Now().Add(20 * time.Second))

	go func() {
		done <- action(runtime, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "enable", "confirmed": true, "expected": idleVoicemail()})
	}()
	seen := []string{}
	for i := 0; i < 4; i++ {
		cmd := read(t, reader, runtime.codec())
		seen = append(seen, cmd["cmd"].(string))
		if cmd["cmd"] == "sendsms" || cmd["cmd"] == "telanswer" || cmd["cmd"] == "telstartrecord" {
			t.Fatal("query sent a write", cmd["cmd"])
		}
		if cmd["p2"] != nil || (cmd["cmd"] == "slotcim" && cmd["p1"] != nil) {
			t.Fatal("query included a write field", cmd)
		}
		replyBoard(t, board, runtime, cmd, idleReply(cmd))
	}
	if seen[0] != "askamrurl" || seen[1] != "slotcim" {
		t.Fatal(seen)
	}
	for i := 0; i < 2; i++ {
		cmd := read(t, reader, runtime.codec())
		seen = append(seen, cmd["cmd"].(string))
		if cmd["cmd"] == "slottts" || cmd["cmd"] == "sendsms" || cmd["cmd"] == "telstarttts" {
			t.Fatal("enable wrote a greeting preset", cmd["cmd"])
		}
		if cmd["cmd"] == "setamrurl" && cmd["p1"] != runtime.Config.VoicemailAMRURL {
			t.Fatal("upload url", cmd["p1"])
		}
		if cmd["cmd"] == "slotcim" && (cmd["p1"] != "1" || cmd["p2"] != "2") {
			t.Fatal("mode write", cmd)
		}
		if cmd["cmd"] == "slotcim" && cmd["p1"] == "2" {
			t.Fatal("untouched slot written")
		}
		replyBoard(t, board, runtime, cmd, "ok")
	}
	for i := 0; i < 4; i++ {
		cmd := read(t, reader, runtime.codec())
		seen = append(seen, "readback:"+cmd["cmd"].(string))
		replyBoard(t, board, runtime, cmd, enabledReply(cmd, runtime.Config))
	}
	w = <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verified":true`) {
		t.Fatal(w.Code, w.Body, seen)
	}
	for _, cmd := range seen {
		if strings.Contains(cmd, "tel") {
			t.Fatal(cmd)
		}
	}
}

func idleVoicemail() map[string]any {
	return map[string]any{"channel": "tcp", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": 0, "tts": ""}, {"slot": 2, "mode": 0, "tts": ""}}}
}
func driftedVoicemail() map[string]any {
	return idleVoicemail()
}
func idleReply(cmd map[string]any) string {
	switch cmd["cmd"] {
	case "slotcim":
		return "0;0"
	default:
		return ""
	}
}
func enabledReply(cmd map[string]any, cfg config.Config) string {
	switch cmd["cmd"] {
	case "slotcim":
		return "2;0"
	case "slottts":
		return ""
	case "askamrurl":
		return cfg.VoicemailAMRURL
	default:
		return ""
	}
}
func subFor(cmd map[string]any) int {
	switch cmd["cmd"] {
	case "setamrurl":
		return 55
	case "askamrurl":
		return 56
	default:
		return 7
	}
}
func replyBoard(t *testing.T, board net.Conn, s *Server, cmd map[string]any, val string) {
	t.Helper()
	send(t, board, s.codec(), map[string]any{"type": 402, "subType": subFor(cmd), "code": 0, "tid": cmd["tid"], "val": val})
}

func waitActive(t *testing.T, s *Server) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		active := s.active != nil
		s.mu.Unlock()
		if active {
			return
		}
		if time.Now().After(until) {
			t.Fatal("not bound")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestVoicemailSMSFallbackWhenSlotCommandsRejected(t *testing.T) {
	const phone, password = "13800138000", "000000"
	token := strings.Repeat("cd", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2); c.OutboundEnabled = true })
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	reader := protocol.NewReader(board)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- action(runtime, "/board/parameters", map[string]any{"action": "voicemail_query"})
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"channel":"sms"`) || !strings.Contains(w.Body.String(), `"mode":null`) {
		t.Fatal(w.Code, w.Body)
	}
	board.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := reader.Next(); err == nil {
		t.Fatal("query continued after slotcim was rejected")
	}
	board.SetDeadline(time.Now().Add(20 * time.Second))

	unknown := map[string]any{"channel": "sms", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	go func() {
		done <- action(runtime, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "enable", "confirmed": true, "expected": unknown})
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	w = <-done
	if w.Code != 409 || !strings.Contains(w.Body.String(), "voicemail_sms_required") || strings.Contains(w.Body.String(), password) {
		t.Fatal(w.Code, w.Body)
	}
	board.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := reader.Next(); err == nil {
		t.Fatal("missing sms credentials still wrote")
	}
	board.SetDeadline(time.Now().Add(20 * time.Second))

	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "setamrurl" || cmd["p1"] != runtime.Config.VoicemailAMRURL {
		t.Fatal("upload url", cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p1"] != "2" || cmd["p2"] != phone || cmd["p3"] != "*#slot2cim#"+password+"#2" {
		t.Fatal("sms command", cmd)
	}
	if strings.Contains(cmd["p3"].(string), "slottts") || strings.Contains(cmd["p3"].(string), "slot1") {
		t.Fatal("enable sent a greeting sms", cmd["p3"])
	}
	replyBoard(t, board, runtime, cmd, "sent")
	send(t, board, runtime.codec(), map[string]any{"type": 501, "slot": 2, "phNum": phone, "smsTs": time.Now().Unix(), "smsBd": "设备(SYNTHETIC) 卡槽2 设置为来电 2声后自动挂断"})
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal("readback", cmd["cmd"])
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	w = <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verified":true`) || !strings.Contains(w.Body.String(), `"sms_submitted":false`) || strings.Contains(w.Body.String(), password) || strings.Contains(w.Body.String(), phone) {
		t.Fatal(w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"mode":2`) || strings.Contains(w.Body.String(), runtime.Config.VoicemailTTS) {
		t.Fatal("enable stored the greeting on the board snapshot", w.Body)
	}
	var stored int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM messages WHERE instr(body, ?) > 0 OR instr(body, ?) > 0 OR instr(body, ?) > 0`, password, "已经设置完成", "自动挂断").Scan(&stored); err != nil || stored != 0 {
		t.Fatal("control sms stored", stored, err)
	}
}

func TestVoicemailSMSSucceedsWithoutControlReceipt(t *testing.T) {
	const phone, password = "+8613800138000", "000000"
	token := strings.Repeat("ab", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2); c.OutboundEnabled = true })
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	reader := protocol.NewReader(board)
	done := make(chan *httptest.ResponseRecorder, 1)
	unknown := map[string]any{"channel": "sms", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "setamrurl" {
		t.Fatal(cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p2"] != phone || cmd["p3"] != "*#slot2cim#"+password+"#2" {
		t.Fatal(cmd)
	}
	send(t, board, runtime.codec(), map[string]any{"type": 401, "subType": 16, "code": 0, "tid": cmd["tid"]})
	send(t, board, runtime.codec(), map[string]any{"type": 502, "code": 0, "tid": cmd["tid"], "slot": 2, "phNum": phone})
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal("missing 501 blocked enable", cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sms_submitted":true`) || !strings.Contains(w.Body.String(), `"verified":false`) || !strings.Contains(w.Body.String(), `"submission":"accepted"`) || !strings.Contains(w.Body.String(), `"url_verified":true`) || strings.Contains(w.Body.String(), `"mode":2`) || strings.Contains(w.Body.String(), phone) || strings.Contains(w.Body.String(), password) {
		t.Fatal(w.Code, w.Body)
	}
	rows, _ := runtime.Logs.Snapshot(diagnostics.Filter{}, 100)
	found := false
	for _, row := range rows {
		if row.Code == "voicemail_sms_submitted" && row.Outcome == "accepted" && row.Slot == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("missing submit diagnostic")
	}

	if _, err := runtime.Store.DB.Exec(`UPDATE voicemail_controls SET sent_at=sent_at-120 WHERE sent_at>0`); err != nil {
		t.Fatal(err)
	}
	queried := map[string]any{"channel": "sms", "amr_url": runtime.Config.VoicemailAMRURL, "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", queried, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p3"] != "*#slot2cim#"+password+"#0" {
		t.Fatal("restore mode", cmd)
	}
	send(t, board, runtime.codec(), map[string]any{"type": 401, "subType": 16, "code": 0, "tid": cmd["tid"]})
	send(t, board, runtime.codec(), map[string]any{"type": 502, "code": 0, "tid": cmd["tid"], "slot": 2, "phNum": phone})
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p3"] != "*#slot2tts#"+password+"#" {
		t.Fatal("restore clear", cmd)
	}
	send(t, board, runtime.codec(), map[string]any{"type": 503, "tid": cmd["tid"], "slot": 2, "phNum": phone, "smsSt": 0, "smsScts": 1700000000, "smsDt": 1700000001})
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "setamrurl" || cmd["p1"] != "clear" {
		t.Fatal("restore url", cmd)
	}
	replyBoard(t, board, runtime, cmd, "")
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal("missing 501 blocked restore", cmd)
	}
	replyBoard(t, board, runtime, cmd, "")
	w = <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sms_submitted":true`) || !strings.Contains(w.Body.String(), `"verified":false`) || strings.Contains(w.Body.String(), `"mode":0`) || strings.Contains(w.Body.String(), "请在提示音后留言") || strings.Contains(w.Body.String(), "已确认") {
		t.Fatal(w.Code, w.Body)
	}
}

func TestVoicemailSMSTimeoutStillSubmits(t *testing.T) {
	const phone, password = "13800138000", "000000"
	token := strings.Repeat("cd", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2); c.OutboundEnabled = true })
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	reader := protocol.NewReader(board)
	done := make(chan *httptest.ResponseRecorder, 1)
	unknown := map[string]any{"channel": "sms", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	cmd := read(t, reader, runtime.codec())
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" {
		t.Fatal(cmd)
	}
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal("silent sendsms failed the apply", cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sms_submitted":true`) || !strings.Contains(w.Body.String(), `"verified":false`) || !strings.Contains(w.Body.String(), `"submission":"submitted"`) || strings.Contains(w.Body.String(), `"mode":2`) {
		t.Fatal(w.Code, w.Body)
	}
	rows, _ := runtime.Logs.Snapshot(diagnostics.Filter{}, 100)
	found := false
	for _, row := range rows {
		if row.Code == "voicemail_sms_submitted" && row.Outcome == "device_result_unknown" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing optimistic diagnostic")
	}
}

func TestVoicemailSMSRejectionStillFails(t *testing.T) {
	const phone, password = "13800138000", "000000"
	token := strings.Repeat("ef", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2); c.OutboundEnabled = true })
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	reader := protocol.NewReader(board)
	done := make(chan *httptest.ResponseRecorder, 1)
	unknown := map[string]any{"channel": "sms", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	cmd := read(t, reader, runtime.codec())
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd = read(t, reader, runtime.codec())
	replyBoard(t, board, runtime, cmd, "sent")
	send(t, board, runtime.codec(), map[string]any{"type": 501, "slot": 2, "phNum": phone, "smsTs": time.Now().Unix(), "smsBd": "用户密码不正确"})
	w := <-done
	if w.Code != 502 || !strings.Contains(w.Body.String(), "voicemail_sms_rejected") || strings.Contains(w.Body.String(), password) {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)
}

func TestVoicemailUnreadSnapshotAgrees(t *testing.T) {
	mode := func(n int) *int { return &n }
	text := func(s string) *string { return &s }
	sms := func(url string, left, right voicemailSlotState) voicemailBoardState {
		return voicemailBoardState{Channel: "sms", AMRURL: url, Slots: []voicemailSlotState{left, right}}
	}
	unread := sms("", voicemailSlotState{Slot: 1}, voicemailSlotState{Slot: 2})
	restored := sms("", voicemailSlotState{Slot: 1, Mode: mode(0), TTS: text("")}, voicemailSlotState{Slot: 2, Mode: mode(0), TTS: text("")})
	enabled := sms("http://192.168.31.10:18880/api/voicemail/amr/"+strings.Repeat("ab", 16), voicemailSlotState{Slot: 1}, voicemailSlotState{Slot: 2, Mode: mode(2)})
	if !voicemailObservationAgrees(unread, restored) || !voicemailObservationAgrees(restored, unread) {
		t.Fatal("restore 0/empty drifted from unread null")
	}
	queried := unread
	queried.AMRURL = enabled.AMRURL
	if !voicemailObservationAgrees(queried, enabled) {
		t.Fatal("unread mode drifted from an enable snapshot")
	}
	concrete := sms("", voicemailSlotState{Slot: 1, Mode: mode(0), TTS: text("")}, voicemailSlotState{Slot: 2, Mode: mode(2), TTS: text("旧预设")})
	cleared := restored
	if voicemailObservationAgrees(concrete, cleared) || voicemailObservationAgrees(sms("http://192.0.2.1:9/api/voicemail/amr/aa", voicemailSlotState{Slot: 1}, voicemailSlotState{Slot: 2}), unread) {
		t.Fatal("concrete mode or url drift was ignored")
	}
	if sameVoicemailState(unread, restored) {
		t.Fatal("unread null became identical to 0 for a write decision")
	}
}

func TestVoicemailEnableAcceptsRestoreBodyOnSMS(t *testing.T) {
	const phone, password = "13800138000", "000000"
	token := strings.Repeat("ab", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2); c.OutboundEnabled = true })
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	reader := protocol.NewReader(board)
	done := make(chan *httptest.ResponseRecorder, 1)
	restored := map[string]any{"channel": "sms", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": 0, "tts": ""}, {"slot": 2, "mode": 0, "tts": ""}}}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", restored, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "setamrurl" {
		t.Fatal("restore snapshot was treated as drift", cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p3"] != "*#slot2cim#"+password+"#2" {
		t.Fatal(cmd)
	}
	send(t, board, runtime.codec(), map[string]any{"type": 401, "subType": 16, "code": 0, "tid": cmd["tid"]})
	send(t, board, runtime.codec(), map[string]any{"type": 502, "code": 0, "tid": cmd["tid"], "slot": 2, "phNum": phone})
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal(cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	if w := <-done; w.Code != 200 || strings.Contains(w.Body.String(), "board_parameter_changed") {
		t.Fatal(w.Code, w.Body)
	}

	stale := map[string]any{"channel": "sms", "amr_url": "http://192.0.2.1:9/api/voicemail/amr/" + token, "slots": []map[string]any{{"slot": 1, "mode": 0, "tts": ""}, {"slot": 2, "mode": 2, "tts": "旧预设"}}}
	go func() {
		done <- action(runtime, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "enable", "confirmed": true, "expected": stale, "sms_phone": phone, "sms_password": password})
	}()
	answerVoicemailRead(t, board, runtime, reader, runtime.Config.VoicemailAMRURL)
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "board_parameter_changed") {
		t.Fatal(w.Code, w.Body)
	}
	board.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := reader.Next(); err == nil {
		t.Fatal("url drift continued into a write")
	}
}

func TestVoicemailSMSReplyParsing(t *testing.T) {
	if body, ok := voicemailSMSBody(2, "000000", "mode", 2, ""); !ok || body != "*#slot2cim#000000#2" {
		t.Fatal(body, ok)
	}
	if body, ok := voicemailSMSBody(2, "000000", "tts", 0, ""); !ok || body != "*#slot2tts#000000#" {
		t.Fatal(body, ok)
	}
	if _, ok := voicemailSMSBody(2, "000000", "mode", 3, ""); ok {
		t.Fatal("mode 3 accepted")
	}
	if _, ok := voicemailSMSBody(1, "12", "tts", 0, "你好"); ok {
		t.Fatal("short password")
	}
	if !smsModeOK("设备(SYNTHETIC) 卡槽2 设置为来电 2声后自动挂断", 2, 2) || smsModeOK("设备(SYNTHETIC) 卡槽2 设置为来电 不接听", 2, 2) {
		t.Fatal("mode reply")
	}
	if !smsModeOK("设备(SYNTHETIC) 卡槽2 设置为来电 不接听", 2, 0) || !smsTTSOK("设备(SYNTHETIC)卡槽2的自动语音，已经设置完成。", 2, false) || !smsTTSOK("设备(SYNTHETIC)卡槽2的自动语音，已经取消。", 2, true) {
		t.Fatal("reply class")
	}
	echo := protocol.Event{Type: 501, Body: "*#slot2tts#000000#请留言"}
	reply := protocol.Event{Type: 501, Body: "设备(SYNTHETIC)卡槽2的自动语音，已经设置完成。"}
	plain := protocol.Event{Type: 501, Body: "合成短信"}
	if !voicemailCommandSMS(echo) || voicemailCommandSMS(reply) || voicemailCommandSMS(plain) {
		t.Fatal("password command classification")
	}
	if !voicemailReplyText(compactSMS(reply.Body)) || voicemailReplyText(compactSMS(plain.Body)) {
		t.Fatal("reply text")
	}
}

func controlApply(intent string, expected map[string]any, phone, password string) map[string]any {
	body := map[string]any{"action": "voicemail_apply", "intent": intent, "confirmed": true, "expected": expected, "sms_phone": phone, "sms_password": password, "sms_cost_confirmed": true}
	if intent == "restore" {
		body["stop_auto_answer_confirmed"] = true
	}
	return body
}

func slotStat(phone, iccid string, slots ...int) map[string]any {
	info := map[string]any{}
	for _, slot := range slots {
		n := strconv.Itoa(slot)
		info["slot"+n+"_sta"] = "OK"
		info["sim"+n+"_iccId"] = iccid
		info["sim"+n+"_msIsdn"] = phone
	}
	return info
}

func answerControlStat(t *testing.T, board net.Conn, s *Server, reader *protocol.Reader, info map[string]any) {
	t.Helper()
	cmd := read(t, reader, s.codec())
	if cmd["cmd"] != "stat" || cmd["p1"] != nil {
		t.Fatal("stat before control sms", cmd)
	}
	send(t, board, s.codec(), map[string]any{"type": 402, "subType": 46, "code": 0, "tid": cmd["tid"], "slotInfo": info})
}

func TestVoicemailGreetingClaimRules(t *testing.T) {
	s, _ := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, strings.Repeat("ab", 16), 2) })
	connected := protocol.Number(10)
	zero := protocol.Number(0)
	ev := protocol.Event{Type: 602, Slot: 2, TelStartTS: 1, TelConnectedTS: &connected}
	if !s.claimVoicemailGreeting(ev) || s.claimVoicemailGreeting(ev) {
		t.Fatal("same connected call claimed twice")
	}
	s.Config.VoicemailEnabled = false
	ev.TelStartTS = 2
	if s.claimVoicemailGreeting(ev) {
		t.Fatal("disabled slot claimed a greeting")
	}
	s.Config.VoicemailEnabled = true
	ev.TelConnectedTS = &zero
	ev.TelStartTS = 3
	if s.claimVoicemailGreeting(ev) {
		t.Fatal("unconnected report claimed a greeting")
	}
	ev.Type = 601
	ev.TelConnectedTS = &connected
	ev.TelStartTS = 4
	if s.claimVoicemailGreeting(ev) {
		t.Fatal("ringing claimed a greeting")
	}
	ev.Type = 602
	ev.Slot = 1
	ev.TelStartTS = 5
	if s.claimVoicemailGreeting(ev) {
		t.Fatal("unselected slot claimed a greeting")
	}
	s.Config.VoicemailTTS = ""
	ev.Slot = 2
	ev.TelStartTS = 6
	if s.claimVoicemailGreeting(ev) {
		t.Fatal("empty greeting claimed")
	}
}

func TestVoicemailEnableLeavesPresetAndRestoreClearsIt(t *testing.T) {
	token := strings.Repeat("ef", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2) })
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	reader := protocol.NewReader(board)
	done := make(chan *httptest.ResponseRecorder, 1)
	current := map[string]any{"channel": "tcp", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": 0, "tts": ""}, {"slot": 2, "mode": 0, "tts": "旧预设"}}}
	go func() {
		done <- action(runtime, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "enable", "confirmed": true, "expected": current})
	}()
	answerPresetRead(t, board, runtime, reader, "", "0;0", "旧预设")
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "setamrurl" || cmd["p1"] != runtime.Config.VoicemailAMRURL {
		t.Fatal("upload url", cmd)
	}
	replyBoard(t, board, runtime, cmd, "ok")
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "slotcim" || cmd["p1"] != "2" || cmd["p2"] != "2" {
		t.Fatal("mode write", cmd)
	}
	replyBoard(t, board, runtime, cmd, "ok")
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal("enable wrote a greeting preset", cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "slotcim" {
		t.Fatal("readback", cmd)
	}
	replyBoard(t, board, runtime, cmd, "0;2")
	answerSlotTTS(t, board, runtime, reader, "", "旧预设")
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verified":true`) || !strings.Contains(w.Body.String(), "旧预设") {
		t.Fatal(w.Code, w.Body)
	}

	enabled := map[string]any{"channel": "tcp", "amr_url": runtime.Config.VoicemailAMRURL, "slots": []map[string]any{{"slot": 1, "mode": 0, "tts": ""}, {"slot": 2, "mode": 2, "tts": "旧预设"}}}
	go func() {
		done <- action(runtime, "/board/parameters", map[string]any{"action": "voicemail_apply", "intent": "restore", "confirmed": true, "expected": enabled})
	}()
	answerPresetRead(t, board, runtime, reader, runtime.Config.VoicemailAMRURL, "0;2", "旧预设")
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "slotcim" || cmd["p1"] != "2" || cmd["p2"] != "0" {
		t.Fatal("restore mode", cmd)
	}
	replyBoard(t, board, runtime, cmd, "ok")
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "slottts" || cmd["p1"] != "2" || cmd["p2"] != "" {
		t.Fatal("restore did not clear the preset", cmd)
	}
	replyBoard(t, board, runtime, cmd, "ok")
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "setamrurl" || cmd["p1"] != "clear" {
		t.Fatal("restore url", cmd)
	}
	replyBoard(t, board, runtime, cmd, "")
	answerPresetRead(t, board, runtime, reader, "", "0;0", "")
	w = <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verified":true`) || strings.Contains(w.Body.String(), "旧预设") {
		t.Fatal(w.Code, w.Body)
	}
}

func answerPresetRead(t *testing.T, board net.Conn, s *Server, reader *protocol.Reader, url, modes, slot2 string) {
	t.Helper()
	cmd := read(t, reader, s.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal("url read", cmd)
	}
	replyBoard(t, board, s, cmd, url)
	cmd = read(t, reader, s.codec())
	if cmd["cmd"] != "slotcim" {
		t.Fatal("mode read", cmd)
	}
	replyBoard(t, board, s, cmd, modes)
	answerSlotTTS(t, board, s, reader, "", slot2)
}

func answerSlotTTS(t *testing.T, board net.Conn, s *Server, reader *protocol.Reader, slot1, slot2 string) {
	t.Helper()
	for _, item := range []struct {
		slot, text string
	}{{"1", slot1}, {"2", slot2}} {
		cmd := read(t, reader, s.codec())
		if cmd["cmd"] != "slottts" || cmd["p1"] != item.slot || cmd["p2"] != nil {
			t.Fatal("tts read", cmd)
		}
		replyBoard(t, board, s, cmd, item.text)
	}
}

func answerVoicemailRead(t *testing.T, board net.Conn, s *Server, reader *protocol.Reader, url string) {
	t.Helper()
	cmd := read(t, reader, s.codec())
	if cmd["cmd"] != "askamrurl" || cmd["p1"] != nil {
		t.Fatal("url read", cmd)
	}
	replyBoard(t, board, s, cmd, url)
	cmd = read(t, reader, s.codec())
	if cmd["cmd"] != "slotcim" || cmd["p1"] != nil || cmd["p2"] != nil {
		t.Fatal("mode probe", cmd)
	}
	send(t, board, s.codec(), map[string]any{"type": 401, "code": 1, "tid": cmd["tid"], "subType": 0})
}

func postVoicemail(s *Server, token string, audio []byte, start int64, remote string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("media", "rec001.amr")
	part.Write(audio)
	w.Close()
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/voicemail/amr/"+token+"?slot=2&start="+strconv.FormatInt(start, 10)+"&phone=SYNTHETIC-CALLER", &buf)
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}
