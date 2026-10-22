package maintenance

import (
	"encoding/binary"
	"io"
	"os"

	"github.com/Battery233/sim-harbor/internal/store"
)

// Bound central-directory allocation before archive/zip reads attacker-controlled
// ZIP64 record counts. Normal and ZIP64 archives emitted by this tool are supported.
func checkZIPDirectory(path string, size int64) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	n := min(size, int64(65535+22))
	if n < 22 {
		return ErrInvalidBackup
	}
	tail := make([]byte, int(n))
	if _, e = f.ReadAt(tail, size-n); e != nil {
		return ErrInvalidBackup
	}
	for i := len(tail) - 22; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) != 0x06054b50 || i+22+int(binary.LittleEndian.Uint16(tail[i+20:])) != len(tail) {
			continue
		}
		if binary.LittleEndian.Uint16(tail[i+4:]) != 0 || binary.LittleEndian.Uint16(tail[i+6:]) != 0 {
			return ErrInvalidBackup
		}
		count := uint64(binary.LittleEndian.Uint16(tail[i+10:]))
		directoryBytes := uint64(binary.LittleEndian.Uint32(tail[i+12:]))
		offset := uint64(binary.LittleEndian.Uint32(tail[i+16:]))
		end := size - n + int64(i)
		if count == 65535 || directoryBytes == 0xffffffff || offset == 0xffffffff {
			locator := make([]byte, 20)
			if end < 20 {
				return ErrInvalidBackup
			}
			if _, e = f.ReadAt(locator, end-20); e != nil || binary.LittleEndian.Uint32(locator) != 0x07064b50 {
				return ErrInvalidBackup
			}
			at := binary.LittleEndian.Uint64(locator[8:])
			if at > uint64(size-56) {
				return ErrInvalidBackup
			}
			record := make([]byte, 56)
			if _, e = f.ReadAt(record, int64(at)); e != nil && e != io.EOF {
				return ErrInvalidBackup
			}
			if binary.LittleEndian.Uint32(record) != 0x06064b50 || binary.LittleEndian.Uint64(record[4:]) < 44 || binary.LittleEndian.Uint32(record[16:]) != 0 || binary.LittleEndian.Uint32(record[20:]) != 0 {
				return ErrInvalidBackup
			}
			count = binary.LittleEndian.Uint64(record[32:])
			directoryBytes = binary.LittleEndian.Uint64(record[40:])
			offset = binary.LittleEndian.Uint64(record[48:])
		}
		if count < 2 || count > uint64(store.RecordingBackupFiles+2) || directoryBytes > 8<<20 || offset > uint64(size) || directoryBytes > uint64(size)-offset {
			return ErrInvalidBackup
		}
		return nil
	}
	return ErrInvalidBackup
}
