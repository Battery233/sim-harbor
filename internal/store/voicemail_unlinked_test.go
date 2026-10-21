package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestUnlinkedVoicemailPaginationAndReadOnlyAccess(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "sim.db"))
	ctx := context.Background()
	for i := 1; i <= 54; i++ {
		if _, _, _, err := s.SaveVoicemail(ctx, dir, []byte(fmt.Sprintf("#!AMR\nSYNTHETIC-%d", i)), 0, "", 0, int64(1000+i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(`INSERT INTO calls(id,key,device_id,slot,phone,start_ts,state,received_at) VALUES(1,'SYNTHETIC-CALL','SIM-TEST',1,'TEST-CALLER',1000,'answered',1000); UPDATE voicemails SET call_id=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	items, err := s.UnlinkedVoicemails(ctx, 0)
	if err != nil || len(items) != 51 || items[0].ID != 54 || items[50].ID != 4 {
		t.Fatal("wrong first page", len(items), err)
	}
	more, err := s.UnlinkedVoicemails(ctx, items[49].ID)
	if err != nil || len(more) != 3 || more[0].ID != 4 || more[2].ID != 2 {
		t.Fatal("cursor duplicated, omitted or included linked audio", err)
	}
	if _, err = s.UnlinkedVoicemailFile(ctx, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("linked audio returned from unlinked-only lookup")
	}
	file, err := s.UnlinkedVoicemailFile(ctx, 2)
	if err != nil || file.Name == "" || file.Size == 0 {
		t.Fatal("unlinked audio unavailable", err)
	}
	if count(t, s, "voicemails") != 54 || count(t, s, "call_notifications") != 0 {
		t.Fatal("viewing changed recordings or generated a notification")
	}
}
