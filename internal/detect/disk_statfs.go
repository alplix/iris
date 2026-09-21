//go:build linux || darwin || freebsd

package detect

import "syscall"

// getDiskUnix reports total and free bytes of the filesystem holding path.
// statfs(2) needs no external tool, so it also works on minimal systems
// (BusyBox has no `df -B1`).
func getDiskUnix(path string) (total, free float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := float64(uint64(st.Bsize))
	return float64(uint64(st.Blocks)) * bs, float64(uint64(st.Bavail)) * bs
}
