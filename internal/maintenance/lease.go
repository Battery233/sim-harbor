package maintenance

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var ErrBusy = errors.New("data_directory_busy")

type Lease struct{ file *os.File }

// Never unlink the lock file: doing so would allow two different inodes to
// carry independent locks for the same data directory.
func Acquire(dir string) (*Lease, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	fd, e := unix.Open(filepath.Join(dir, ".sim.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "sim-data-lock")
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		if errors.Is(e, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, e
	}
	return &Lease{file: f}, nil
}
func (l *Lease) Close() {
	if l != nil && l.file != nil {
		unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
		l.file.Close()
		l.file = nil
	}
}
