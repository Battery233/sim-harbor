package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestControlSMSDoesNotDropOrdinaryInbox(t *testing.T) {
	runtime, listener := fixture(t, 1, func(c *config.Config) {
		enableVoicemail(c, strings.Repeat("ab", 16), 2)
		c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC", "https://example.invalid", "https://example.invalid"
	})
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	sendSMS := func(slot int, phone, body string, ts int64) {
		t.Helper()
		send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": slot, "phNum": phone, "smsTs": ts, "smsBd": body})
	}
	sendSMS(2, "13800138000", "卡槽2 设置为来电 不接听", 1700000001)
	sendSMS(2, "13800138000", "合成短信", 1700000002)
	sendSMS(1, "13900139000", "卡槽2 设置为来电 不接听", 1700000003)
	waitMessageCount(t, runtime, 3)
	if _, err := runtime.Store.DB.Exec(`INSERT INTO voicemail_controls(device_id,slot,phone,iccid,kind,mode,clear_tts,state,created_at,sent_at) VALUES('SIM-TEST',2,'13800138000','SYNTHETIC-ICC','mode',0,0,'claimed',?,?)`, time.Now().Unix(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	sendSMS(2, "+8613800138000", "*#slot2cim#000000#0", 1700000005)
	send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 2, "iccId": "SYNTHETIC-ICC", "phNum": "+8613800138000", "smsTs": time.Now().Unix(), "smsBd": "设备(SYNTHETIC) 卡槽2 设置为来电 不接听"})
	sendSMS(2, "13900139000", "设备(SYNTHETIC) 卡槽2 设置为来电 不接听", 1700000007)
	sendSMS(1, "13800138000", "设备(SYNTHETIC) 卡槽1 设置为来电 不接听", 1700000008)
	waitMessageCount(t, runtime, 5)
	var leaked int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM messages WHERE instr(body,'000000')>0 OR instr(body,'*#slot')>0`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("password entered the inbox", leaked, err)
	}
	var notes int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM notifications`).Scan(&notes); err != nil || notes != 5 {
		t.Fatal("notification count", notes, err)
	}
}

func TestOrdinarySMSStoredWhenVoicemailOff(t *testing.T) {
	runtime, listener := fixture(t, 1, func(c *config.Config) {
		c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC", "https://example.invalid", "https://example.invalid"
	})
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 2, "phNum": "13800138000", "smsTs": 1700000100, "smsBd": "卡槽2 设置为来电 不接听"})
	send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 2, "phNum": "13800138000", "smsTs": 1700000101, "smsBd": "合成短信"})
	send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 2, "phNum": "13800138000", "smsTs": 1700000102, "smsBd": "*#slot2cim#000000#2"})
	time.Sleep(300 * time.Millisecond)
	waitMessageCount(t, runtime, 2)
	var leaked int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM messages WHERE instr(body,'000000')>0`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("password entered the inbox while voicemail was off", leaked, err)
	}
	var notes int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM notifications`).Scan(&notes); err != nil || notes != 2 {
		t.Fatal(notes, err)
	}
}

func TestControlSMSObeysPaidSendProtections(t *testing.T) {
	const phone, password = "13800138000", "000000"
	token := strings.Repeat("ab", 16)
	unknown := map[string]any{"channel": "sms", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	runtime, listener := fixture(t, 1, func(c *config.Config) {
		enableVoicemail(c, token, 2)
		c.SendDailyLimit = 1
	})
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	reader := protocol.NewReader(board)
	done := make(chan *httptest.ResponseRecorder, 1)
	board.SetDeadline(time.Now().Add(20 * time.Second))

	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "outbound_disabled") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)

	if err := runtime.Store.HoldRestoredData(context.Background(), strings.Repeat("cd", 32), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	runtime.Config.OutboundEnabled = true
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "outbound_restore_blocked") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)

	runtime.Config.OutboundEnabled = false
	body := controlApply("restore", unknown, phone, password)
	delete(body, "stop_auto_answer_confirmed")
	go func() { done <- action(runtime, "/board/parameters", body) }()
	answerVoicemailRead(t, board, runtime, reader, "")
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "stop_auto_answer_required") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)

	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	answerControlStat(t, board, runtime, reader, slotStat("13900139000", "SYNTHETIC-ICC-2", 2))
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "card_number_mismatch") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)

	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	answerControlStat(t, board, runtime, reader, map[string]any{"slot2_sta": "OK", "sim2_msIsdn": phone})
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "card_identity_required") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)

	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", unknown, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p3"] != "*#slot2cim#"+password+"#0" {
		t.Fatal("stopping auto-answer was blocked", cmd)
	}
	send(t, board, runtime.codec(), map[string]any{"type": 502, "code": 0, "tid": cmd["tid"], "slot": 2, "phNum": phone})
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "daily_limit") {
		t.Fatal(w.Code, w.Body)
	}
	var leaked int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM outgoing WHERE instr(body,?)>0`, password).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("password stored in outbox", leaked, err)
	}
	rows, _ := runtime.Logs.Snapshot(diagnostics.Filter{}, 200)
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		if strings.Contains(string(encoded), password) {
			t.Fatal("password logged")
		}
	}
}

func TestControlSMSDoesNotResendAfterUnknownOrRestart(t *testing.T) {
	const phone, password = "13800138000", "000000"
	token := strings.Repeat("ef", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 2); c.OutboundEnabled = true })
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	reader := protocol.NewReader(board)
	board.SetDeadline(time.Now().Add(15 * time.Second))
	done := make(chan *httptest.ResponseRecorder, 1)
	unknown := map[string]any{"channel": "sms", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		body := controlApply("enable", unknown, phone, password)
		req := httptest.NewRequest("POST", "http://example.com/api/board/parameters", strings.NewReader(mustJSON(t, body))).WithContext(ctx)
		req.Host = "127.0.0.1:8080"
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		runtime.Handler().ServeHTTP(w, req)
		done <- w
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	cmd := read(t, reader, runtime.codec())
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-A", 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" {
		t.Fatal(cmd)
	}
	cancel()
	if w := <-done; w.Code != 502 {
		t.Fatal(w.Code, w.Body)
	}
	queried := map[string]any{"channel": "sms", "amr_url": runtime.Config.VoicemailAMRURL, "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", queried, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-B", 2))
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "card_changed") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", queried, phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-A", 2))
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "control_already_dispatched") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)
}

func TestDualSlotControlSMSChecksEachNumber(t *testing.T) {
	const password = "000000"
	token := strings.Repeat("ab", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) { enableVoicemail(c, token, 1, 2); c.OutboundEnabled = true })
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	reader := protocol.NewReader(board)
	board.SetDeadline(time.Now().Add(20 * time.Second))
	done := make(chan *httptest.ResponseRecorder, 1)
	unknown := map[string]any{"channel": "sms", "amr_url": "", "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
	one := controlApply("enable", unknown, "13800138000", password)
	go func() { done <- action(runtime, "/board/parameters", one) }()
	answerVoicemailRead(t, board, runtime, reader, "")
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "voicemail_sms_required") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)
	both := controlApply("enable", unknown, "", password)
	delete(both, "sms_phone")
	both["sms_phones"] = map[string]string{"1": "13800138001", "2": "13800138002"}
	go func() { done <- action(runtime, "/board/parameters", both) }()
	answerVoicemailRead(t, board, runtime, reader, "")
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "setamrurl" {
		t.Fatal(cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	answerControlStat(t, board, runtime, reader, map[string]any{
		"slot1_sta": "OK", "sim1_iccId": "SYNTHETIC-ICC-1", "sim1_msIsdn": "13800138001",
		"slot2_sta": "OK", "sim2_iccId": "SYNTHETIC-ICC-2", "sim2_msIsdn": "13800138002",
	})
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p1"] != "1" || cmd["p2"] != "13800138001" {
		t.Fatal(cmd)
	}
	send(t, board, runtime.codec(), map[string]any{"type": 502, "code": 0, "tid": cmd["tid"], "slot": 1, "phNum": "13800138001"})
	answerControlStat(t, board, runtime, reader, map[string]any{
		"slot1_sta": "OK", "sim1_iccId": "SYNTHETIC-ICC-1", "sim1_msIsdn": "13800138001",
		"slot2_sta": "OK", "sim2_iccId": "SYNTHETIC-ICC-2", "sim2_msIsdn": "13800138002",
	})
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p1"] != "2" || cmd["p2"] != "13800138002" {
		t.Fatal(cmd)
	}
	send(t, board, runtime.codec(), map[string]any{"type": 502, "code": 0, "tid": cmd["tid"], "slot": 2, "phNum": "13800138002"})
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal(cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verified":false`) || strings.Contains(w.Body.String(), `"mode":2`) {
		t.Fatal(w.Code, w.Body)
	}
}

func TestControlReplyMatchesSourceSlotAndSIM(t *testing.T) {
	const phone, password, iccid = "13800138000", "000000", "SYNTHETIC-ICC-2"
	token := strings.Repeat("ab", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) {
		enableVoicemail(c, token, 2)
		c.OutboundEnabled = true
		c.BarkKey, c.BarkBaseURL, c.PublicURL = "SYNTHETIC", "https://example.invalid", "https://example.invalid"
	})
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	reader := protocol.NewReader(board)
	board.SetDeadline(time.Now().Add(20 * time.Second))
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
	answerControlStat(t, board, runtime, reader, slotStat(phone, iccid, 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" {
		t.Fatal(cmd)
	}
	success := "设备(SYNTHETIC) 卡槽2 设置为来电 2声后自动挂断"
	fresh := time.Now().Unix()
	sendReply := func(slot int, from, card, body string, ts int64) {
		t.Helper()
		send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": slot, "iccId": card, "phNum": from, "smsTs": ts, "smsBd": body})
	}
	sendReply(2, "13900139000", iccid, success, fresh)
	sendReply(1, phone, iccid, success, fresh+1)
	sendReply(2, phone, "SYNTHETIC-ICC-OTHER", success, fresh+2)
	sendReply(2, phone, "", success, fresh+3)
	sendReply(2, phone, iccid, success, fresh-3600)
	sendReply(2, "13700137000", "SYNTHETIC-ICC-OTHER", "密码不正确", fresh+4)
	sendReply(1, phone, iccid, "格式错误", fresh+5)
	waitMessageCount(t, runtime, 7)
	board.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := reader.Next(); err == nil {
		t.Fatal("unrelated sms was accepted as the control reply")
	}
	board.SetDeadline(time.Now().Add(20 * time.Second))
	select {
	case w := <-done:
		t.Fatal("unrelated sms changed the control", w.Code, w.Body.String())
	default:
	}
	sendReply(2, phone, iccid, success, time.Now().Unix())
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal(cmd)
	}
	replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verified":true`) || !strings.Contains(w.Body.String(), `"mode":2`) {
		t.Fatal(w.Code, w.Body)
	}
	var stored int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM messages`).Scan(&stored); err != nil || stored != 7 {
		t.Fatal("legitimate reply changed the inbox", stored, err)
	}
	var leaked int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM messages WHERE instr(body,?)>0`, password).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("password entered the inbox", leaked, err)
	}
	var state string
	if err := runtime.Store.DB.QueryRow(`SELECT state FROM voicemail_controls`).Scan(&state); err != nil || state != "accepted" {
		t.Fatal(state, err)
	}
}

func TestRestoreAfterEnableSendsClearTTSAndURL(t *testing.T) {
	const phone, password, iccid = "13800138000", "000000", "SYNTHETIC-ICC-2"
	runtime, board, reader, done := startControlBoard(t)
	url := runtime.Config.VoicemailAMRURL
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", smsBoard(""), phone, password))
	}()
	serveRestore(t, board, runtime, reader, "", "", []controlSMS{{"*#slot2cim#" + password + "#0", "设备(SYNTHETIC) 卡槽2 设置为来电 不接听"}, {"*#slot2tts#" + password + "#", "设备(SYNTHETIC)卡槽2的自动语音，已经取消。"}})
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verified":true`) || !strings.Contains(w.Body.String(), `"mode":0`) || !strings.Contains(w.Body.String(), `"tts":""`) || !strings.Contains(w.Body.String(), `"sms_submitted":false`) {
		t.Fatal(w.Code, w.Body)
	}
	backdateControlLedger(t, runtime)
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("enable", smsBoard(""), phone, password))
	}()
	serveEnable(t, board, runtime, reader, phone, password, "", true, url)
	w = <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":2`) || strings.Contains(w.Body.String(), "slottts") {
		t.Fatal(w.Code, w.Body)
	}
	backdateControlLedger(t, runtime)
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", smsBoard(url), phone, password))
	}()
	serveRestore(t, board, runtime, reader, url, "", []controlSMS{{"*#slot2cim#" + password + "#0", "设备(SYNTHETIC) 卡槽2 设置为来电 不接听"}, {"*#slot2tts#" + password + "#", "设备(SYNTHETIC)卡槽2的自动语音，已经取消。"}})
	w = <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"verified":true`) || !strings.Contains(w.Body.String(), `"url_verified":true`) || !strings.Contains(w.Body.String(), `"mode":0`) || !strings.Contains(w.Body.String(), `"tts":""`) {
		t.Fatal(w.Code, w.Body)
	}
	if controlRows(t, runtime) != 5 {
		t.Fatal("restore enable restore sms count", controlRows(t, runtime))
	}
	assertControlSecretsAbsent(t, runtime, password)
}

func TestEnableRestoreCycleRepeatsClearTTS(t *testing.T) {
	const phone, password = "13800138000", "000000"
	runtime, board, reader, done := startControlBoard(t)
	url := runtime.Config.VoicemailAMRURL
	current := ""
	for i := 0; i < 2; i++ {
		expected := current
		go func() {
			done <- action(runtime, "/board/parameters", controlApply("enable", smsBoard(expected), phone, password))
		}()
		serveEnable(t, board, runtime, reader, phone, password, expected, true, url)
		if w := <-done; w.Code != 200 || strings.Contains(w.Body.String(), "slottts") {
			t.Fatal(w.Code, w.Body)
		}
		backdateControlLedger(t, runtime)
		current = url
		expected = current
		go func() {
			done <- action(runtime, "/board/parameters", controlApply("restore", smsBoard(expected), phone, password))
		}()
		serveRestore(t, board, runtime, reader, expected, "", []controlSMS{{"*#slot2cim#" + password + "#0", "设备(SYNTHETIC) 卡槽2 设置为来电 不接听"}, {"*#slot2tts#" + password + "#", "设备(SYNTHETIC)卡槽2的自动语音，已经取消。"}})
		w := <-done
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":0`) || !strings.Contains(w.Body.String(), `"tts":""`) || !strings.Contains(w.Body.String(), `"url_verified":true`) {
			t.Fatal(w.Code, w.Body)
		}
		backdateControlLedger(t, runtime)
		current = ""
	}
	if controlRows(t, runtime) != 6 {
		t.Fatal("two enable and restore cycles", controlRows(t, runtime))
	}
	assertControlSecretsAbsent(t, runtime, password)
}

func TestDuplicateRestoreStepStillClearsUploadURL(t *testing.T) {
	const phone, password, iccid = "13800138000", "000000", "SYNTHETIC-ICC-2"
	runtime, board, reader, done := startControlBoard(t)
	url := runtime.Config.VoicemailAMRURL
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		body := controlApply("restore", smsBoard(url), phone, password)
		req := httptest.NewRequest("POST", "http://example.com/api/board/parameters", strings.NewReader(mustJSON(t, body))).WithContext(ctx)
		req.Host = "127.0.0.1:8080"
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		runtime.Handler().ServeHTTP(w, req)
		done <- w
	}()
	answerVoicemailRead(t, board, runtime, reader, url)
	answerControlStat(t, board, runtime, reader, slotStat(phone, iccid, 2))
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p3"] != "*#slot2cim#"+password+"#0" {
		t.Fatal(cmd)
	}
	send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 2, "iccId": iccid, "phNum": phone, "smsTs": time.Now().Unix(), "smsBd": "设备(SYNTHETIC) 卡槽2 设置为来电 不接听"})
	answerControlStat(t, board, runtime, reader, slotStat(phone, iccid, 2))
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p3"] != "*#slot2tts#"+password+"#" {
		t.Fatal(cmd)
	}
	cancel()
	if w := <-done; w.Code != 502 {
		t.Fatal(w.Code, w.Body)
	}
	if controlRows(t, runtime) != 2 {
		t.Fatal(controlRows(t, runtime))
	}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", smsBoard(url), phone, password))
	}()
	serveRestore(t, board, runtime, reader, url, "", []controlSMS{{"", ""}, {"", ""}})
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sms_submitted":false`) || !strings.Contains(w.Body.String(), `"url_verified":true`) || !strings.Contains(w.Body.String(), `"verified":false`) || strings.Contains(w.Body.String(), `"mode":0`) || strings.Contains(w.Body.String(), `"tts":""`) {
		t.Fatal(w.Code, w.Body)
	}
	if controlRows(t, runtime) != 2 {
		t.Fatal("skipped steps consumed quota", controlRows(t, runtime))
	}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", smsBoard(""), phone, password))
	}()
	answerVoicemailRead(t, board, runtime, reader, "")
	answerControlStat(t, board, runtime, reader, slotStat(phone, iccid, 2))
	answerControlStat(t, board, runtime, reader, slotStat(phone, iccid, 2))
	if w := <-done; w.Code != 409 || !strings.Contains(w.Body.String(), "control_already_dispatched") {
		t.Fatal(w.Code, w.Body)
	}
	expectNoBoardCommand(t, board)
	if controlRows(t, runtime) != 2 {
		t.Fatal(controlRows(t, runtime))
	}
}

func TestUnknownRestoreDoesNotResendFinishedMode(t *testing.T) {
	const phone, password, iccid = "13800138000", "000000", "SYNTHETIC-ICC-2"
	runtime, board, reader, done := startControlBoard(t)
	url := runtime.Config.VoicemailAMRURL
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		body := controlApply("restore", smsBoard(url), phone, password)
		req := httptest.NewRequest("POST", "http://example.com/api/board/parameters", strings.NewReader(mustJSON(t, body))).WithContext(ctx)
		req.Host = "127.0.0.1:8080"
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		runtime.Handler().ServeHTTP(w, req)
		done <- w
	}()
	answerVoicemailRead(t, board, runtime, reader, url)
	answerControlStat(t, board, runtime, reader, slotStat(phone, iccid, 2))
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" {
		t.Fatal(cmd)
	}
	cancel()
	if w := <-done; w.Code != 502 {
		t.Fatal(w.Code, w.Body)
	}
	go func() {
		done <- action(runtime, "/board/parameters", controlApply("restore", smsBoard(url), phone, password))
	}()
	serveRestore(t, board, runtime, reader, url, "", []controlSMS{{"", ""}, {"*#slot2tts#" + password + "#", "设备(SYNTHETIC)卡槽2的自动语音，已经取消。"}})
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sms_submitted":true`) || !strings.Contains(w.Body.String(), `"url_verified":true`) || !strings.Contains(w.Body.String(), `"tts":""`) || strings.Contains(w.Body.String(), `"mode":0`) {
		t.Fatal(w.Code, w.Body)
	}
	if controlRows(t, runtime) != 2 {
		t.Fatal("unknown mode was sent again", controlRows(t, runtime))
	}
	assertControlSecretsAbsent(t, runtime, password)
}

type controlSMS struct {
	body, receipt string
}

func startControlBoard(t *testing.T) (*Server, net.Conn, *protocol.Reader, chan *httptest.ResponseRecorder) {
	t.Helper()
	token := strings.Repeat("ab", 16)
	runtime, listener := fixture(t, 1, func(c *config.Config) {
		enableVoicemail(c, token, 2)
		c.OutboundEnabled = true
		c.SendDailyLimit = 20
	})
	board := connect(t, listener)
	send(t, board, runtime.codec(), map[string]any{"type": 998, "devId": "SIM-TEST"})
	waitActive(t, runtime)
	reader := protocol.NewReader(board)
	board.SetDeadline(time.Now().Add(60 * time.Second))
	return runtime, board, reader, make(chan *httptest.ResponseRecorder, 1)
}

func smsBoard(url string) map[string]any {
	return map[string]any{"channel": "sms", "amr_url": url, "slots": []map[string]any{{"slot": 1, "mode": nil, "tts": nil}, {"slot": 2, "mode": nil, "tts": nil}}}
}

func backdateControlLedger(t *testing.T, runtime *Server) {
	t.Helper()
	if _, err := runtime.Store.DB.Exec(`UPDATE voicemail_controls SET sent_at=sent_at-120 WHERE sent_at>0`); err != nil {
		t.Fatal(err)
	}
}

func controlRows(t *testing.T, runtime *Server) int {
	t.Helper()
	var n int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM voicemail_controls`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func serveRestore(t *testing.T, board net.Conn, runtime *Server, reader *protocol.Reader, currentURL, finalURL string, messages []controlSMS) {
	t.Helper()
	answerVoicemailRead(t, board, runtime, reader, currentURL)
	for _, item := range messages {
		answerControlStat(t, board, runtime, reader, slotStat("13800138000", "SYNTHETIC-ICC-2", 2))
		if item.body == "" {
			continue
		}
		cmd := read(t, reader, runtime.codec())
		if cmd["cmd"] != "sendsms" || cmd["p1"] != "2" || cmd["p3"] != item.body {
			t.Fatal("restore sms", cmd)
		}
		send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 2, "iccId": "SYNTHETIC-ICC-2", "phNum": "13800138000", "smsTs": time.Now().Unix(), "smsBd": item.receipt})
	}
	if currentURL != finalURL {
		cmd := read(t, reader, runtime.codec())
		if cmd["cmd"] != "setamrurl" || cmd["p1"] != "clear" {
			t.Fatal("restore url", cmd)
		}
		replyBoard(t, board, runtime, cmd, "")
	}
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal("restore readback", cmd)
	}
	replyBoard(t, board, runtime, cmd, finalURL)
}

func serveEnable(t *testing.T, board net.Conn, runtime *Server, reader *protocol.Reader, phone, password, currentURL string, writeURL bool, finalURL string) {
	t.Helper()
	answerVoicemailRead(t, board, runtime, reader, currentURL)
	if writeURL {
		cmd := read(t, reader, runtime.codec())
		if cmd["cmd"] != "setamrurl" || cmd["p1"] != runtime.Config.VoicemailAMRURL {
			t.Fatal("enable url", cmd)
		}
		replyBoard(t, board, runtime, cmd, runtime.Config.VoicemailAMRURL)
	}
	answerControlStat(t, board, runtime, reader, slotStat(phone, "SYNTHETIC-ICC-2", 2))
	cmd := read(t, reader, runtime.codec())
	if cmd["cmd"] != "sendsms" || cmd["p3"] != "*#slot2cim#"+password+"#2" {
		t.Fatal("enable sms", cmd)
	}
	if strings.Contains(cmd["p3"].(string), "slottts") {
		t.Fatal("enable sent clear-tts", cmd["p3"])
	}
	send(t, board, runtime.codec(), map[string]any{"type": 501, "devId": "SIM-TEST", "slot": 2, "iccId": "SYNTHETIC-ICC-2", "phNum": phone, "smsTs": time.Now().Unix(), "smsBd": "设备(SYNTHETIC) 卡槽2 设置为来电 2声后自动挂断"})
	cmd = read(t, reader, runtime.codec())
	if cmd["cmd"] != "askamrurl" {
		t.Fatal("enable readback", cmd)
	}
	replyBoard(t, board, runtime, cmd, finalURL)
}

func assertControlSecretsAbsent(t *testing.T, runtime *Server, password string) {
	t.Helper()
	var leaked int
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM messages WHERE instr(body,?)>0`, password).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("password entered the inbox", leaked, err)
	}
	if err := runtime.Store.DB.QueryRow(`SELECT count(*) FROM outgoing WHERE instr(body,?)>0`, password).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("password entered the outbox", leaked, err)
	}
	rows, _ := runtime.Logs.Snapshot(diagnostics.Filter{}, 200)
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		if strings.Contains(string(encoded), password) {
			t.Fatal("password logged")
		}
	}
}

func waitMessageCount(t *testing.T, s *Server, n int) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		var count int
		if err := s.Store.DB.QueryRow(`SELECT count(*) FROM messages`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("message count", n)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
