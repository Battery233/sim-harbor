package maintenance

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battery233/sim/internal/protocol"
	"github.com/Battery233/sim/internal/store"
)

func fixture(t *testing.T) *store.Store {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "sim.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}

func TestOnlineBackupDuringIngestionHasConsistentMessageTaskPairs(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, Phone: "TEST", Body: fmt.Sprintf("synthetic-%d", i), SMSTS: protocol.Number(1700000000 + i)}
			raw, _ := json.Marshal(e)
			if err := s.Ingest(ctx, e, raw, true); err != nil {
				done <- err
				return
			}
			if i == 0 {
				close(started)
			}
		}
		done <- nil
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatal("ingestion stopped before first message", err)
	}
	a, err := Create(ctx, s, t.TempDir())
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	p, err := Inspect(ctx, a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if n := p.Manifest.Counts["messages"]; n < 1 || n > 100 || n != p.Manifest.Counts["notifications"] {
		t.Fatal("inconsistent snapshot", p.Manifest.Counts)
	}
}
func TestBackupPreservesWALReadStateOutboxAndRowIDs(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	if _, e := s.DB.Exec(`PRAGMA wal_autocheckpoint=0`); e != nil {
		t.Fatal(e)
	}
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 2, Phone: "TEST-SENDER", Body: "合成私密短信", SMSTS: 1700000000}
	raw, _ := json.Marshal(e)
	if err := s.Ingest(ctx, e, raw, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO outgoing(rowid,id,device_id,tid,fingerprint,slot,phone,body,segments,iccid,created_at,updated_at,expires_at,dispatched_at,state,rank) VALUES(77,'old-op','SIM-TEST','old-tid','fp',2,'000000','test',1,'synthetic-card',1,2,3,2,'waiting_device',1)`); err != nil {
		t.Fatal(err)
	}
	a, err := Create(ctx, s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	preview, err := Inspect(ctx, a.Path)
	if err != nil || preview.Manifest.Counts["messages"] != 1 || preview.Manifest.Counts["outgoing"] != 1 || preview.Manifest.Counts["notifications"] != 1 || len(preview.ArchiveSHA256) != 64 {
		t.Fatal(preview, err)
	}
	v, err := load(ctx, a.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer v.close()
	// Open has a deliberate restart transition, so query the immutable snapshot
	// through a plain connection instead to check the source was never changed.
	sourceState := ""
	if err = s.DB.QueryRow(`SELECT state FROM outgoing`).Scan(&sourceState); err != nil || sourceState != "waiting_device" {
		t.Fatal("backup changed source", sourceState, err)
	}
	snapshot, err := store.Open(v.database)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.DB.Close()
	var rowid int64
	var read bool
	if err = snapshot.DB.QueryRow(`SELECT rowid FROM outgoing`).Scan(&rowid); err != nil || rowid != 77 {
		t.Fatal("rowid changed", rowid, err)
	}
	if err = snapshot.DB.QueryRow(`SELECT is_read FROM messages`).Scan(&read); err != nil || !read {
		t.Fatal("read state lost", read, err)
	}
	for _, path := range []string{a.Path, v.database} {
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private mode", info, e)
		}
	}
}

func rewrite(t *testing.T, path string, change func(string, []byte) (string, []byte), extra bool) string {
	t.Helper()
	z, e := zip.OpenReader(path)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	out := filepath.Join(t.TempDir(), "modified.zip")
	f, e := os.Create(out)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(f)
	for _, entry := range z.File {
		r, e := entry.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		name, b := change(entry.Name, b)
		dst, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = dst.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if extra {
		dst, e := w.Create("../extra")
		if e != nil {
			t.Fatal(e)
		}
		dst.Write([]byte("do not extract"))
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestInspectRefusesDamagedForeignAndUnsafeArchives(t *testing.T) {
	s := fixture(t)
	a, e := Create(context.Background(), s, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	cases := []func(string, []byte) (string, []byte){
		func(name string, b []byte) (string, []byte) {
			if name == "sim.db" {
				b[len(b)/2] ^= 1
			}
			return name, b
		},
		func(name string, b []byte) (string, []byte) {
			if name == "manifest.json" {
				b = []byte(`{"application":"other"}`)
			}
			return name, b
		},
		func(name string, b []byte) (string, []byte) {
			if name == "sim.db" {
				name = "../sim.db"
			}
			return name, b
		},
		func(name string, b []byte) (string, []byte) { return "sim.db", b },
		func(name string, b []byte) (string, []byte) {
			if name == "manifest.json" {
				b = append(b, []byte(` {}`)...)
			}
			return name, b
		},
		func(name string, b []byte) (string, []byte) {
			if name == "manifest.json" {
				var m Manifest
				json.Unmarshal(b, &m)
				delete(m.Counts, "events")
				m.Counts["foreign"] = 0
				b, _ = json.Marshal(m)
			}
			return name, b
		},
	}
	for i, change := range cases {
		path := rewrite(t, a.Path, change, false)
		if _, e = Inspect(context.Background(), path); e == nil {
			t.Fatal("invalid archive accepted", i)
		}
	}
	extra := rewrite(t, a.Path, func(n string, b []byte) (string, []byte) { return n, b }, true)
	if _, e = Inspect(context.Background(), extra); e == nil {
		t.Fatal("extra entry accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Inspect(canceled, a.Path); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`DELETE FROM schema_version;INSERT INTO schema_version VALUES(99)`); e != nil {
		t.Fatal(e)
	}
	if _, e = Create(context.Background(), s, t.TempDir()); !errors.Is(e, ErrSchema) {
		t.Fatal("future schema accepted", e)
	}
}
func TestSnapshotCancellationCleansPartialAndDoesNotOverwrite(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(t.TempDir(), "snapshot ? #.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := s.SnapshotTo(ctx, path); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("partial snapshot remains", e)
	}
	if e := os.WriteFile(path, []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := s.SnapshotTo(context.Background(), path); e == nil {
		t.Fatal("overwrote existing file")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "preserve" {
		t.Fatal("existing file changed")
	}
	alternate := filepath.Join(t.TempDir(), "snapshot ? #.db")
	if e := s.SnapshotTo(context.Background(), alternate); e != nil {
		t.Fatal(e)
	}
	var header [16]byte
	f, _ := os.Open(alternate)
	defer f.Close()
	f.Read(header[:])
	if !strings.HasPrefix(string(header[:]), "SQLite format 3") {
		t.Fatal("bad snapshot")
	}
}
