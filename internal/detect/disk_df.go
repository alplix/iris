//go:build !linux && !darwin && !freebsd

package detect

import (
	"strconv"
	"strings"
	"time"
)

// getDiskUnix falls back to df on systems whose syscall package has no
// portable statfs (OpenBSD, NetBSD). Windows never calls it.
func getDiskUnix(path string) (total, free float64) {
	out, err := run(10*time.Second, "df", "-k", path)
	if err != nil {
		return 0, 0
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return 0, 0
	}
	f := strings.Fields(lines[1])
	if len(f) < 4 {
		return 0, 0
	}
	t, _ := strconv.ParseFloat(f[1], 64)
	a, _ := strconv.ParseFloat(f[3], 64)
	return t * 1024, a * 1024
}
