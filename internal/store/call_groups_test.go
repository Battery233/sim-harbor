package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestCallGroupsCompleteHistoryAndStableIdentity(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	start := time.Now().Unix() - 10000
	for i := 0; i < 60; i++ {
		e := callEvent(603, start+int64(i*30))
		e.TelEndTS = e.TelStartTS + 23
		if i > 0 {
			e.Slot = 2
			e.CardName = "Renamed"
		}
		recordCall(t, s, e, true)
	}
	other := callEvent(603, start+5000)
	other.ICCID = "SECOND-SYNTHETIC-CARD"
	other.TelEndTS = other.TelStartTS + 20
	recordCall(t, s, other, true)
	groups, err := s.CallGroups(ctx, "", "", 0, start+10000)
	if err != nil || len(groups) != 2 || groups[1].Count != 60 || groups[1].MissedCount != 60 || groups[1].ID != 1 || groups[1].Latest.ID != 60 {
		t.Fatal("global aggregation failed", groups, err)
	}
	g, err := s.CallGroup(ctx, 20, start+10000)
	if err != nil || g.ID != 1 || g.Count != 60 || g.Latest.CardName != "Renamed" {
		t.Fatal("old link did not resolve stable group", g, err)
	}
	calls, err := s.CallsInGroup(ctx, 1, 0, start+10000)
	if err != nil || len(calls) != 51 || calls[0].ID != 60 || calls[49].ID != 11 {
		t.Fatal("detail first page", err)
	}
	calls, err = s.CallsInGroup(ctx, 1, 11, start+10000)
	if err != nil || len(calls) != 10 || calls[9].CardName != "First card name" || calls[9].Slot != 1 {
		t.Fatal("detail pagination lost original snapshot", calls, err)
	}
	filtered, err := s.CallGroups(ctx, g.Latest.CardID, "CALLER", 0, start+10000)
	if err != nil || len(filtered) != 1 || filtered[0].Count != 60 {
		t.Fatal("card filter mixed callers", err)
	}
	// Adding another call updates the summary without changing the group ID.
	e := callEvent(601, start+6000)
	recordCall(t, s, e, true)
	g, err = s.CallGroup(ctx, 1, start+6001)
	if err != nil || g.ID != 1 || g.Count != 61 || g.MissedCount != 60 || g.Latest.State != "ringing" {
		t.Fatal("group changed or ringing marked missed", g, err)
	}
	var jobs int
	if err := s.DB.QueryRow(`SELECT count(*) FROM call_notifications`).Scan(&jobs); err != nil || jobs != 61 {
		t.Fatal("ring/start duplicated a notification", jobs, err)
	}
}

func TestCallGroupsUnknownIsolationAndGroupPagination(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	start := time.Now().Unix() - 10000
	for i := 0; i < 56; i++ {
		e := callEvent(603, start+int64(i*30))
		e.Phone = fmt.Sprintf("SYNTHETIC-%d", i)
		e.TelEndTS = e.TelStartTS + 5
		recordCall(t, s, e, false)
	}
	for i := 0; i < 6; i++ {
		e := callEvent(603, start+3000+int64(i*30))
		e.TelEndTS = e.TelStartTS + 5
		switch i / 2 {
		case 0:
			e.Phone = "anonymous"
		case 1:
			e.Phone = ""
		case 2:
			e.ICCID = ""
		}
		recordCall(t, s, e, false)
	}
	groups, err := s.CallGroups(ctx, "", "", 0, start+10000)
	if err != nil || len(groups) != 51 {
		t.Fatal("group pagination", err)
	}
	for _, g := range groups {
		if g.Count != 1 {
			t.Fatal("unknown callers merged", g)
		}
	}
	older, err := s.CallGroups(ctx, "", "", groups[49].Latest.ID, start+10000)
	if err != nil || len(older) != 12 || older[0].Latest.ID != 12 {
		t.Fatal("group page skipped records", older, err)
	}
	for _, id := range []int64{57, 58, 59, 60, 61, 62} {
		details, err := s.CallsInGroup(ctx, id, 0, start+10000)
		if err != nil || len(details) != 1 || details[0].ID != id {
			t.Fatal("withheld/unknown details mixed", id, err)
		}
	}
	if _, err := s.CallGroup(ctx, 999, start); err == nil {
		t.Fatal("missing group accepted")
	}
}

func TestCallGroupIncludesRecordingFromOlderCall(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	dir := t.TempDir()
	start := time.Now().Unix() - 100
	for i := 0; i < 3; i++ {
		e := callEvent(603, start+int64(i*10))
		e.TelEndTS = e.TelStartTS + 2
		recordCall(t, s, e, false)
	}
	media, _, _, err := s.SaveVoicemail(ctx, dir, []byte("#!AMR\nSYNTHETIC"), 1, "SYNTHETIC-CALLER", start, start+30)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := s.CallGroups(ctx, "", "", 0, start+40)
	if err != nil || len(groups) != 1 || groups[0].Count != 3 || groups[0].Latest.VoicemailID != 0 || groups[0].RecordingCount != 1 || groups[0].UnplayedRecordings != 1 {
		t.Fatal("older recording missing from summary", groups, err)
	}
	calls, err := s.CallsInGroup(ctx, groups[0].ID, 0, start+40)
	if err != nil || calls[2].VoicemailMediaID != media {
		t.Fatal("recording identity missing", err)
	}
	if _, err := s.MarkVoicemailPlayed(ctx, media, start+41); err != nil {
		t.Fatal(err)
	}
	g, err := s.CallGroup(ctx, groups[0].ID, start+42)
	if err != nil || g.RecordingCount != 1 || g.UnplayedRecordings != 0 {
		t.Fatal("played recording changed total", g, err)
	}
	if err := s.DeleteVoicemail(ctx, dir, media); err != nil {
		t.Fatal(err)
	}
	g, err = s.CallGroup(ctx, groups[0].ID, start+44)
	if err != nil || g.Count != 3 || g.RecordingCount != 0 || g.UnplayedRecordings != 0 {
		t.Fatal("deleted recording retained in group count", g, err)
	}
}

func TestCallGroupRecordingSummaryStaysSmallWithLongHistory(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := t.Context()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO cards(id,iccid,name,group_name) VALUES('aaaaaaaaaaaaaaaa','SYNTHETIC-CARD','Synthetic','Synthetic')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 1000; i++ {
		if _, err = tx.Exec(`INSERT INTO calls(id,key,device_id,card_id,slot,phone,start_ts,state,received_at) VALUES(?,?,?,'aaaaaaaaaaaaaaaa',1,'SYNTHETIC-CALLER',1,'answered',1)`, i, fmt.Sprint(i), "SIM-TEST"); err != nil {
			t.Fatal(err)
		}
		media := fmt.Sprintf("%032x", i)
		if _, err = tx.Exec(`INSERT INTO voicemails(media_id,call_id,sha256,size,stored_name,created_at) VALUES(?,?,?,7,?,1)`, media, i, fmt.Sprintf("%064x", i), media+".amr"); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	groups, err := s.CallGroups(ctx, "", "", 0, 2)
	if err != nil || len(groups) != 1 || groups[0].Count != 1000 || groups[0].RecordingCount != 1000 || groups[0].UnplayedRecordings != 1000 {
		t.Fatal("recording summary lost history", err)
	}
	data, err := json.Marshal(groups)
	if err != nil || len(data) > 2048 {
		t.Fatal("group payload grew with recording IDs", len(data), err)
	}
}
