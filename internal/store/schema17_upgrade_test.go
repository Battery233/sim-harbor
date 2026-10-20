package store

import (
	"path/filepath"
	"testing"
)

func TestSchema17UpgradeKeepsHistoryWithVoicemailOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	if _, err := s.DB.Exec(`INSERT INTO messages(key,slot,phone,body,sms_ts,complete,fragments,card_name,sms_group_ref,sms_total) VALUES('legacy-sms',2,'13800138000','合成甲',1700000000,0,'{"1":"合成甲"}','旧卡名','groupref',2)`); err != nil {
		t.Fatal(err)
	}
	var messageID int64
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE key='legacy-sms'`).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO notifications(message_id,state,attempts,accepted_at) VALUES(?,'sent',1,1700000001)`, messageID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO sms_receipts(key,message_id,sms_ts) VALUES('receipt-legacy',?,1700000000)`, messageID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO calls(key,device_id,card_name,slot,phone,start_ts,connected_ts,end_ts,ended,state,received_at) VALUES('legacy-call','SIM-TEST','旧卡名',2,'13800138000',1700001000,1700001003,1700001010,1,'answered',1700001010)`); err != nil {
		t.Fatal(err)
	}
	var callID int64
	if err := s.DB.QueryRow(`SELECT id FROM calls WHERE key='legacy-call'`).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO call_notifications(call_id,card_name,group_name,state,kind,accepted_at) VALUES(?,'旧卡名','来源未确认','sent','missed',1700001011)`, callID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO recovery_guard(id,restored_at,archive_sha256,restore_id,confirmed_at,confirmation_backup_sha256,confirmation_policy) VALUES(1,1700000000,'SYNTHETIC-ARCHIVE','SYNTHETIC-RESTORE',1700000500,'SYNTHETIC-BACKUP','complete_history_v1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE voicemail_controls; DROP TABLE voicemails; DELETE FROM schema_version; INSERT INTO schema_version VALUES(17)`); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	var version int
	if err := s.DB.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal("schema", version, err)
	}
	var body, fragments, noteState string
	var receipt int
	if err := s.DB.QueryRow(`SELECT body, fragments FROM messages WHERE id=?`, messageID).Scan(&body, &fragments); err != nil || body != "合成甲" || fragments != `{"1":"合成甲"}` {
		t.Fatal("sms", body, fragments, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM sms_receipts WHERE message_id=? AND sms_ts=1700000000`, messageID).Scan(&receipt); err != nil || receipt != 1 {
		t.Fatal("receipt link", receipt, err)
	}
	if err := s.DB.QueryRow(`SELECT state FROM notifications WHERE message_id=?`, messageID).Scan(&noteState); err != nil || noteState != "sent" {
		t.Fatal("message notification", noteState, err)
	}
	var callState, callNote string
	if err := s.DB.QueryRow(`SELECT state FROM calls WHERE id=?`, callID).Scan(&callState); err != nil || callState != "answered" {
		t.Fatal("call", callState, err)
	}
	if err := s.DB.QueryRow(`SELECT state FROM call_notifications WHERE call_id=?`, callID).Scan(&callNote); err != nil || callNote != "sent" {
		t.Fatal("call notification", callNote, err)
	}
	var confirmed int64
	var policy string
	if err := s.DB.QueryRow(`SELECT confirmed_at, confirmation_policy FROM recovery_guard WHERE id=1`).Scan(&confirmed, &policy); err != nil || confirmed != 1700000500 || policy != "complete_history_v1" {
		t.Fatal("restore confirmation", confirmed, policy, err)
	}
	var recordings, controls int
	if err := s.DB.QueryRow(`SELECT count(*) FROM voicemails`).Scan(&recordings); err != nil || recordings != 0 {
		t.Fatal("voicemail stayed off but gained recordings", recordings, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM voicemail_controls`).Scan(&controls); err != nil || controls != 0 {
		t.Fatal("upgrade created a control sms", controls, err)
	}
}
