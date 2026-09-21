//go:build linux || darwin || freebsd

package detect

import "testing"

func TestGetDiskUnixReportsSaneNumbers(t *testing.T) {
	total, free := getDiskUnix("/")
	if total <= 0 || free < 0 || free > total {
		t.Fatalf("total=%v free=%v", total, free)
	}
	if total2, _ := getDiskUnix("/definitely/not/here"); total2 != 0 {
		t.Fatalf("a missing path should report 0, got %v", total2)
	}
}
