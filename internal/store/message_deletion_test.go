package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDeleteMessageClearsPayloadCancelsPendingAndSurvivesReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sim.db")
	s := open(t, path)
	ctx := context.Background()
	e := sms()
	e.ICCID = "SYNTHETIC-CARD"
	e.CardName = "Synthetic name"
	ingest(t, s, e)
	m, _ := s.Message(ctx, 1)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.DeleteMessage(ctx, m.ID, 1700001000); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := s.Message(ctx, m.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted detail exposed", err)
	}
	if err := s.Read(ctx, m.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted read allowed", err)
	}
	for _, filter := range []MessageFilter{{}, {Card: m.CardID}, {Card: "unknown"}} {
		rows, err := s.ExportMessages(ctx, filter)
		if err != nil || len(rows) != 0 {
			t.Fatal("deleted export", rows, err)
		}
	}
	if _, err := s.NextNotification(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted push selected", err)
	}
	var payload, phone, parts, name, status string
	s.DB.QueryRow(`SELECT body,phone,fragments,card_name FROM messages WHERE id=1`).Scan(&payload, &phone, &parts, &name)
	s.DB.QueryRow(`SELECT state FROM notifications WHERE message_id=1`).Scan(&status)
	if payload != "" || phone != "" || parts != "{}" || name != "" || status != "canceled" {
		t.Fatal("payload or pending notification retained")
	}
	s.DB.Close()
	s = open(t, path)
	e.Slot = 2
	ingest(t, s, e)
	rows, _ := s.Messages(ctx, 0, "")
	if len(rows) != 0 || count(t, s, "messages") != 1 || count(t, s, "notifications") != 1 {
		t.Fatal("replay resurrected deleted SMS")
	}
	e.SMSTS++
	ingest(t, s, e)
	rows, _ = s.Messages(ctx, 0, "")
	if len(rows) != 1 || rows[0].ID == m.ID {
		t.Fatal("new same-body event lost or ID reused")
	}
}

func TestDeletePartialPreventsLateFragmentsAndPreservesSuccessfulStats(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sim.db"))
	ctx := context.Background()
	e := sms()
	e.Group = "SYNTHETIC-GROUP"
	e.Max = 2
	e.Fragments = []string{"1,SYNTHETIC"}
	e.Body = ""
	ingest(t, s, e)
	if err := s.DeleteMessage(ctx, 1, 1700001000); err != nil {
		t.Fatal(err)
	}
	e.Fragments = []string{"2,-END"}
	ingest(t, s, e)
	rows, _ := s.Messages(ctx, 0, "")
	if len(rows) != 0 || count(t, s, "notifications") != 0 {
		t.Fatal("late fragment resurrected")
	}
	e = sms()
	e.SMSTS++
	ingest(t, s, e)
	n, err := s.NextNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = s.NotificationResultCodeAt(ctx, n, true, "accepted", now.Unix()); err != nil {
		t.Fatal(err)
	}
	before, _ := s.NotificationSuccess(ctx, now, time.UTC)
	if err = s.DeleteMessage(ctx, n.MessageID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	after, _ := s.NotificationSuccess(ctx, now, time.UTC)
	if before != after || after.Today != 1 {
		t.Fatal("deletion rewrote success history", before, after)
	}
	if err = s.DeleteMessage(ctx, 999, now.Unix()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unknown delete", err)
	}
}
