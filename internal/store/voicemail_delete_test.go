package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteVoicemailPreservesCallDedupAndSentNotification(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "sim.db")
	s := open(t, db)
	now := time.Now().Unix()
	e := callEvent(603, now)
	e.TelEndTS = e.TelStartTS + 2
	recordCall(t, s, e, false)
	audio := []byte("#!AMR\nSYNTHETIC")
	media, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, e.Phone, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE call_notifications SET state='sent'`); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteVoicemail(ctx, dir, media); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "voicemail", media+".amr")); !os.IsNotExist(err) {
		t.Fatal("audio retained", err)
	}
	c, err := s.Call(ctx, 1, now+3)
	if err != nil || c.VoicemailID != 0 || !c.VoicemailDeleted || c.NotificationState != "sent" {
		t.Fatal(c, err)
	}
	again, _, duplicate, err := s.SaveVoicemail(ctx, dir, audio, 1, e.Phone, now, now+5)
	if err != nil || !duplicate || again != media || notificationCount(t, s, 0) != 1 {
		t.Fatal("deleted upload resurrected", duplicate, err)
	}
	groups, err := s.CallGroups(ctx, "", "", 0, now+10)
	if err != nil || len(groups) != 1 || len(groups[0].VoicemailMediaIDs) != 0 || !groups[0].Latest.VoicemailDeleted {
		t.Fatal("deleted audio in summary", groups, err)
	}
	if err = s.DeleteVoicemail(ctx, dir, media); err != nil {
		t.Fatal("idempotent delete", err)
	}
}

func TestDeleteUnlinkedDoesNotRelinkAndRecoversInterruptedCleanup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	audio := []byte("#!AMR\nSYNTHETIC")
	media, _, _, err := s.SaveVoicemail(ctx, dir, audio, 1, "SYNTHETIC-CALLER", 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteVoicemail(ctx, dir, media); err != nil {
		t.Fatal(err)
	}
	recordCall(t, s, callAt(1, "SYNTHETIC-CALLER", "SYNTHETIC-CARD", 1000), false)
	if _, _, _, err = s.SaveVoicemail(ctx, dir, audio, 1, "SYNTHETIC-CALLER", 1000, 1002); err != nil {
		t.Fatal(err)
	}
	items, err := s.UnlinkedVoicemails(ctx, 0)
	if err != nil || len(items) != 0 || notificationCount(t, s, 0) != 0 {
		t.Fatal("unlinked tombstone resurrected", items, err)
	}
	// Simulate process exit after committing the tombstone, before unlinking.
	path := filepath.Join(dir, "voicemail", media+".amr")
	if err = os.WriteFile(path, audio, 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "voicemail", "unrelated.partial")
	if err = os.WriteFile(other, []byte("KEEP"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupDeletedVoicemails(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("interrupted cleanup retained audio", err)
	}
	if b, err := os.ReadFile(other); err != nil || string(b) != "KEEP" {
		t.Fatal("unrelated file touched", err)
	}
}

func TestDeleteVoicemailCancelsPendingAndRejectsSymlinkDirectory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	recordCall(t, s, callAt(1, "SYNTHETIC-CALLER", "SYNTHETIC-CARD", 1000), false)
	media, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER", 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "outside")
	if err = os.Rename(filepath.Join(dir, "voicemail"), moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(moved, filepath.Join(dir, "voicemail")); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteVoicemail(ctx, dir, media); err != ErrVoicemailCleanup {
		t.Fatal("followed directory symlink", err)
	}
	if _, err = os.Stat(filepath.Join(moved, media+".amr")); err != nil {
		t.Fatal("outside audio touched", err)
	}
	c, err := s.Call(ctx, 1, 1001)
	if err != nil || c.NotificationState != "canceled" || c.LastError != "voicemail_deleted" {
		t.Fatal(c, err)
	}
}
