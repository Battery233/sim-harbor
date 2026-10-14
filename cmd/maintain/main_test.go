package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Battery233/sim/internal/maintenance"
	"github.com/Battery233/sim/internal/store"
)

func TestMaintenanceInspectConfirmRestoreAndInvalidParameters(t *testing.T) {
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "sim.db"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := maintenance.Create(context.Background(), s, dir)
	s.DB.Close()
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	var out bytes.Buffer
	if e = run([]string{"inspect", "-backup", a.Path}, &out); e != nil {
		t.Fatal(e)
	}
	var p maintenance.Preview
	if e = json.Unmarshal(out.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if e = run([]string{"restore", "-backup", a.Path, "-to", target}, &out); e == nil {
		t.Fatal("missing confirm accepted")
	}
	out.Reset()
	if e = run([]string{"restore", "-backup", a.Path, "-to", target, "-confirm-archive", p.ArchiveSHA256}, &out); e != nil {
		t.Fatal(e)
	}
	restored, e := store.Open(filepath.Join(target, "sim.db"))
	if e != nil {
		t.Fatal(e)
	}
	blocked, e := restored.RestoreBlocked(context.Background())
	restored.DB.Close()
	if e != nil || !blocked {
		t.Fatal("CLI restore missing guard", e)
	}
	for _, args := range [][]string{{}, {"other"}, {"inspect", "-backup", a.Path, "-to", target}, {"backup"}, {"restore", "-backup", a.Path, "-to", target, "-confirm-archive", p.ArchiveSHA256}} {
		if e = run(args, &out); e == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
	out.Reset()
	if e = run([]string{"backup", "-data-directory", target, "-output", filepath.Join(t.TempDir(), "copy.zip")}, &out); e != nil {
		t.Fatal(e)
	}
}
