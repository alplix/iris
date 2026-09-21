package detect

import (
	"testing"
	"time"
)

func TestBenchmarkReturnsPlausibleFlops(t *testing.T) {
	got := Benchmark(2, 150*time.Millisecond)
	// Any machine that can run the tests manages well over 10 MFLOPS per core.
	if got < 2*1e7 {
		t.Fatalf("Benchmark = %.3g FLOPS, implausibly low", got)
	}
}
