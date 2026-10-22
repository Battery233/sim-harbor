package maintenance

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestZIPDirectoryBoundsAndZIP64(t *testing.T) {
	path := filepath.Join(t.TempDir(), "normal.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, name := range []string{"sim.db", "manifest.json"} {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte("SYNTHETIC"))
	}
	z.Close()
	f.Close()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	end := len(original) - 22
	if err = checkZIPDirectory(path, int64(len(original))); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		name  string
		count uint16
		size  uint32
	}{{"too-many", 10003, 1}, {"directory-allocation", 2, 9 << 20}, {"too-few", 1, 1}} {
		t.Run(bad.name, func(t *testing.T) {
			b := append([]byte(nil), original...)
			binary.LittleEndian.PutUint16(b[end+10:], bad.count)
			binary.LittleEndian.PutUint32(b[end+12:], bad.size)
			os.WriteFile(path, b, 0600)
			if err := checkZIPDirectory(path, int64(len(b))); !errors.Is(err, ErrInvalidBackup) {
				t.Fatal(err)
			}
		})
	}
	// Promote a small valid archive to ZIP64 without allocating a multi-gigabyte file.
	record := make([]byte, 56)
	binary.LittleEndian.PutUint32(record, 0x06064b50)
	binary.LittleEndian.PutUint64(record[4:], 44)
	binary.LittleEndian.PutUint64(record[24:], 2)
	binary.LittleEndian.PutUint64(record[32:], 2)
	binary.LittleEndian.PutUint64(record[40:], uint64(binary.LittleEndian.Uint32(original[end+12:])))
	binary.LittleEndian.PutUint64(record[48:], uint64(binary.LittleEndian.Uint32(original[end+16:])))
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator, 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(end))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	eocd := append([]byte(nil), original[end:]...)
	binary.LittleEndian.PutUint16(eocd[8:], 65535)
	binary.LittleEndian.PutUint16(eocd[10:], 65535)
	b := append(append(append(append([]byte(nil), original[:end]...), record...), locator...), eocd...)
	os.WriteFile(path, b, 0600)
	if err = checkZIPDirectory(path, int64(len(b))); err != nil {
		t.Fatal("valid ZIP64", err)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 {
		t.Fatal("ZIP64 not readable")
	}
	reader.Close()
	binary.LittleEndian.PutUint64(b[end+32:], math.MaxUint64)
	os.WriteFile(path, b, 0600)
	if err = checkZIPDirectory(path, int64(len(b))); !errors.Is(err, ErrInvalidBackup) {
		t.Fatal("ZIP64 huge count", err)
	}
}

func TestBackupSpacePreflight(t *testing.T) {
	if err := requireSpace(t.TempDir(), math.MaxInt64); !errors.Is(err, unix.ENOSPC) {
		t.Fatal(err)
	}
}
