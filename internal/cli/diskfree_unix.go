//go:build unix

package cli

import "golang.org/x/sys/unix"

func statfsFree(dir string) (int64, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, false
	}
	// Statfs_t field types differ across unixes (Bsize is int64 on Linux,
	// uint32 on Darwin) — cast both.
	return int64(st.Bavail) * int64(st.Bsize), true
}
