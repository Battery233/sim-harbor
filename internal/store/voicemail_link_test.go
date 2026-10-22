package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestVoicemailLinkStaysConservative(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().Unix()
	recordCall(t, s, callAt(1, "11111111", "CARD-A", 1000), false)
	recordCall(t, s, callAt(1, "22222222", "CARD-A", 1001), false)
	media, _, dup, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-a"), 1, "11111111", 1001, now)
	if err != nil || dup || media == "" {
		t.Fatal(err, dup, media)
	}
	a := callByPhone(t, s, "11111111", now)
	b := callByPhone(t, s, "22222222", now)
	if a.VoicemailID == 0 || b.VoicemailID != 0 {
		t.Fatal("recording followed the other number", a.VoicemailID, b.VoicemailID)
	}
	if notes := notificationCount(t, s, b.ID); notes != 0 {
		t.Fatal("wrong call was notified", notes)
	}

	recordCall(t, s, callAt(1, "33333333", "CARD-A", 2000), false)
	recordCall(t, s, callAt(1, "33333333", "CARD-B", 2010), false)
	if _, _, _, err = s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-swap"), 1, "33333333", 2005, now); err != nil {
		t.Fatal(err)
	}
	var swapped int
	if err = s.DB.QueryRow(`SELECT count(*) FROM voicemails WHERE phone='33333333' AND call_id IS NOT NULL`).Scan(&swapped); err != nil || swapped != 0 {
		t.Fatal("card swap was linked", swapped, err)
	}

	recordCall(t, s, callAt(1, "44444444", "CARD-A", 3000), false)
	recordCall(t, s, callAt(1, "44444444", "CARD-A", 3010), false)
	first, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-first"), 1, "44444444", 3000, now)
	if err != nil || first == "" {
		t.Fatal(err)
	}
	if _, _, _, err = s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-second"), 1, "44444444", 3005, now); err != nil {
		t.Fatal(err)
	}
	var unlinked int
	if err = s.DB.QueryRow(`SELECT count(*) FROM voicemails WHERE phone='44444444' AND call_id IS NULL`).Scan(&unlinked); err != nil || unlinked != 1 {
		t.Fatal("leftover candidate was treated as identified", unlinked, err)
	}

	recordCall(t, s, callAt(1, "+8613800138000", "CARD-A", 4000), false)
	if _, _, _, err = s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-format"), 1, "13800138000", 4001, now); err != nil {
		t.Fatal(err)
	}
	if callByPhone(t, s, "+8613800138000", now).VoicemailID == 0 {
		t.Fatal("same number in another format stayed unlinked")
	}

	recordCall(t, s, callAt(1, "55555555", "", 5000), false)
	if _, _, _, err = s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-noid"), 1, "55555555", 5001, now); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-exact"), 1, "55555555", 5000, now); err != nil {
		t.Fatal(err)
	}
	var linkedExact, linkedNear int
	s.DB.QueryRow(`SELECT count(*) FROM voicemails WHERE phone='55555555' AND start_ts=5000 AND call_id IS NOT NULL`).Scan(&linkedExact)
	s.DB.QueryRow(`SELECT count(*) FROM voicemails WHERE phone='55555555' AND start_ts=5001 AND call_id IS NOT NULL`).Scan(&linkedNear)
	if linkedExact != 1 || linkedNear != 0 {
		t.Fatal("missing identity used the time window", linkedExact, linkedNear)
	}

	recordCall(t, s, callAt(1, "anonymous", "CARD-A", 6000), false)
	recordCall(t, s, callAt(1, "66666666", "CARD-A", 6001), false)
	if _, _, _, err = s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-anon"), 1, "anonymous", 6001, now); err != nil {
		t.Fatal(err)
	}
	if callByPhone(t, s, "66666666", now).VoicemailID != 0 || callByPhone(t, s, "anonymous", now).VoicemailID != 0 {
		t.Fatal("anonymous recording was attributed")
	}
}

func TestVoicemailWindowKeepsUnknownCardAmbiguous(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().Unix()
	recordCall(t, s, callAt(1, "15151515", "CARD-KNOWN", 11000), false)
	recordCall(t, s, callAt(1, "15151515", "", 11008), false)
	if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-mixed"), 1, "15151515", 11005, now); err != nil {
		t.Fatal(err)
	}
	var linked int
	if err := s.DB.QueryRow(`SELECT count(*) FROM voicemails WHERE phone='15151515' AND call_id IS NOT NULL`).Scan(&linked); err != nil || linked != 0 {
		t.Fatal("known card was chosen after dropping the unknown card", linked, err)
	}
	if notificationCount(t, s, 0) != 0 {
		t.Fatal("ambiguous recording notified a call")
	}

	recordCall(t, s, callAt(1, "16161616", "CARD-BEFORE", 12000), false)
	recordCall(t, s, callAt(1, "16161616", "CARD-AFTER", 12020), false)
	if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-swap"), 1, "16161616", 12010, now); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM voicemails WHERE phone='16161616' AND call_id IS NOT NULL`).Scan(&linked); err != nil || linked != 0 {
		t.Fatal("swap-card recording was linked", linked, err)
	}

	recordCall(t, s, callAt(1, "17171717", "CARD-NEAR", 13000), false)
	recordCall(t, s, callAt(1, "17171717", "CARD-NEAR", 13020), false)
	if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-near"), 1, "17171717", 13010, now); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM voicemails WHERE phone='17171717' AND call_id IS NOT NULL`).Scan(&linked); err != nil || linked != 0 {
		t.Fatal("two near calls were treated as one", linked, err)
	}
	if notificationCount(t, s, 0) != 0 {
		t.Fatal("unlinked recordings created a notification")
	}

	recordCall(t, s, callAt(2, "18181818", "CARD-ONLY", 14000), false)
	if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\namr-only"), 2, "18181818", 14002, now); err != nil {
		t.Fatal(err)
	}
	only := callByPhone(t, s, "18181818", now)
	if only.VoicemailID == 0 || notificationCount(t, s, only.ID) != 1 {
		t.Fatal("unique known card was left unlinked", only.VoicemailID)
	}
}

func TestVoicemailLateLinkDedupesFileAndNotification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().Unix()
	audio := []byte("#!AMR\nsame-bytes")
	media, _, dup, err := s.SaveVoicemail(ctx, dir, audio, 2, "77777777", 7000, now)
	if err != nil || dup {
		t.Fatal(err, dup)
	}
	if callCount(t, s) != 0 || notificationCount(t, s, 0) != 0 {
		t.Fatal("upload invented a call notification")
	}
	recordCall(t, s, callAt(2, "77777777", "CARD-A", 7000), true)
	got := callByPhone(t, s, "77777777", now)
	if got.VoicemailID == 0 || notificationCount(t, s, got.ID) != 1 {
		t.Fatal("later call did not link", got.VoicemailID)
	}
	again, _, dup, err := s.SaveVoicemail(ctx, dir, audio, 2, "77777777", 7000, now+1)
	if err != nil || !dup || again != media || notificationCount(t, s, got.ID) != 1 {
		t.Fatal("duplicate upload", again, media, dup, err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "voicemail", "*.amr"))
	if err != nil || len(files) != 1 {
		t.Fatal("recording file duplicated", files, err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	if notificationCount(t, s, got.ID) != 1 || callByPhone(t, s, "77777777", now).VoicemailBytes == 0 {
		t.Fatal("restart created another notification or dropped the link")
	}

	recordCall(t, s, callAt(2, "88888888", "CARD-A", 8000), true)
	if _, _, _, err = s.SaveVoicemail(ctx, dir, []byte("#!AMR\nlater-call-first"), 2, "88888888", 8000, now); err != nil {
		t.Fatal(err)
	}
	first := callByPhone(t, s, "88888888", now)
	if first.VoicemailID == 0 || notificationCount(t, s, first.ID) != 1 {
		t.Fatal("call-first order", first.VoicemailID)
	}
}

func TestVoicemailLinkPreservesMuteAndRestoreHold(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().Unix()
	recordCall(t, s, callAt(1, "12121212", "CARD-MUTE", 9000), false)
	if _, err := s.DB.Exec(`UPDATE cards SET notification_enabled=0`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nmuted"), 1, "12121212", 9000, now); err != nil {
		t.Fatal(err)
	}
	muted := callByPhone(t, s, "12121212", now)
	state, kind := notificationState(t, s, muted.ID)
	if muted.VoicemailID == 0 || state != "canceled" || kind != "voicemail" {
		t.Fatal("mute became a voicemail alert", state, kind)
	}

	recordCall(t, s, callAt(1, "13131313", "CARD-HOLD", 9100), false)
	if err := s.HoldRestoredData(ctx, "SYNTHETIC-ARCHIVE", now); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nheld"), 1, "13131313", 9100, now); err != nil {
		t.Fatal(err)
	}
	held := callByPhone(t, s, "13131313", now)
	state, kind = notificationState(t, s, held.ID)
	if held.VoicemailID == 0 || state != "restore_held" || kind != "voicemail" || notificationCount(t, s, held.ID) != 1 {
		t.Fatal("restore hold was released", state, kind)
	}
}

func TestVoicemailReconcileAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().Unix()
	if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nbefore-call"), 1, "14141414", 10000, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO calls(key,device_id,card_name,slot,phone,start_ts,state,received_at) VALUES('late','SIM-TEST','',1,'14141414',10000,'ringing',?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	var callID int64
	if err := s.DB.QueryRow(`SELECT call_id FROM voicemails WHERE phone='14141414'`).Scan(&callID); err != nil || callID == 0 {
		t.Fatal("reopen did not link", callID, err)
	}
	if notificationCount(t, s, callID) != 1 {
		t.Fatal("reconcile duplicated or skipped the notification")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "voicemail", "*.amr"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	if _, err := os.ReadFile(files[0]); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaNineteenUpgradeWithoutVoicemailUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	if _, err := s.DB.Exec(`DROP TABLE voicemail_controls; DELETE FROM schema_version; INSERT INTO schema_version VALUES(18)`); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	var version, controls int
	if err := s.DB.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM voicemail_controls`).Scan(&controls); err != nil {
		t.Fatal(err)
	}
}

func callAt(slot int, phone, iccid string, start int64) protocol.Event {
	direction := protocol.Number(1)
	return protocol.Event{DeviceID: "SIM-TEST", Type: 601, Slot: protocol.Number(slot), Phone: phone, ICCID: iccid, CardName: "Synthetic", TelStartTS: protocol.Number(start), TelDir: &direction}
}

func callByPhone(t *testing.T, s *Store, phone string, now int64) Call {
	t.Helper()
	var id int64
	if err := s.DB.QueryRow(`SELECT id FROM calls WHERE phone=? ORDER BY id`, phone).Scan(&id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Call(context.Background(), id, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func callCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM calls`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func notificationCount(t *testing.T, s *Store, callID int64) int {
	t.Helper()
	var n int
	var err error
	if callID == 0 {
		err = s.DB.QueryRow(`SELECT count(*) FROM call_notifications`).Scan(&n)
	} else {
		err = s.DB.QueryRow(`SELECT count(*) FROM call_notifications WHERE call_id=?`, callID).Scan(&n)
	}
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func notificationState(t *testing.T, s *Store, callID int64) (string, string) {
	t.Helper()
	var state, kind string
	if err := s.DB.QueryRow(`SELECT state, kind FROM call_notifications WHERE call_id=?`, callID).Scan(&state, &kind); err != nil {
		t.Fatal(err)
	}
	return state, kind
}

func TestVoicemailControlClaimSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ctx := context.Background()
	now := time.Now()
	limits := DayLimits(now, 60, 5)
	id, err := s.ClaimVoicemailControl(ctx, "SIM-TEST", 2, "13800138000", "SYNTHETIC-ICC", "mode", 2, false, now.Unix(), limits, false)
	if err != nil || id == 0 {
		t.Fatal(err, id)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	_, err = s.ClaimVoicemailControl(ctx, "SIM-TEST", 2, "13800138000", "SYNTHETIC-ICC", "mode", 2, false, time.Now().Unix(), limits, false)
	safety, ok := err.(*SafetyError)
	if !ok || safety.Code != "control_already_dispatched" {
		t.Fatal(err)
	}
	var rows int
	var bodyLeak int
	if err = s.DB.QueryRow(`SELECT count(*) FROM voicemail_controls`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal(rows, err)
	}
	if err = s.DB.QueryRow(`SELECT count(*) FROM voicemail_controls WHERE phone LIKE '%000000%' OR iccid LIKE '%000000%'`).Scan(&bodyLeak); err != nil || bodyLeak != 0 {
		t.Fatal("control row kept a password", bodyLeak, err)
	}
	if _, err = s.DB.Exec(`UPDATE voicemail_controls SET state='rejected', sent_at=? WHERE id=?`, now.Unix()-120, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimVoicemailControl(ctx, "SIM-TEST", 2, "13800138000", "SYNTHETIC-ICC", "mode", 2, false, time.Now().Unix(), limits, false); err != nil {
		t.Fatal("rejected control could not be retried", err)
	}
	raw, _ := json.Marshal(map[string]string{"secret": "000000"})
	if _, err = s.DB.Exec(`INSERT INTO outgoing(id,device_id,tid,fingerprint,slot,phone,body,segments,iccid,created_at,updated_at,expires_at,dispatched_at,state,rank) VALUES('op','SIM-TEST','tid','fp',2,'13800138000',?,1,'SYNTHETIC-ICC',?,?,?,?,'waiting_device',1)`, string(raw), now.Unix(), now.Unix(), now.Unix()+60, now.Unix()); err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimVoicemailControl(ctx, "SIM-TEST", 2, "13800138000", "SYNTHETIC-ICC", "tts", 0, true, time.Now().Unix(), limits, false)
	safety, ok = err.(*SafetyError)
	if !ok || safety.Code != "send_interval" {
		t.Fatal(err)
	}
}
