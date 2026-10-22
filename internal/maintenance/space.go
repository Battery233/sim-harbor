package maintenance

import (
	"golang.org/x/sys/unix"
)

func requireSpace(path string, need int64) error {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return err
	}
	if need < 0 || uint64(st.Bavail)*uint64(st.Bsize) < uint64(need) {
		return unix.ENOSPC
	}
	return nil
}
