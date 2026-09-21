package detect

import (
	"runtime"
	"sync"
	"time"
)

var benchSink float64

// Benchmark measures floating-point throughput by running one busy loop per
// core for roughly d and returns the summed FLOPS of all cores.
func Benchmark(ncpu int, d time.Duration) float64 {
	if ncpu < 1 {
		ncpu = 1
	}
	if d < 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	results := make([]float64, ncpu)
	var wg sync.WaitGroup
	for i := 0; i < ncpu; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			results[i] = spin(d)
		}(i)
	}
	wg.Wait()
	var total float64
	for _, r := range results {
		total += r
	}
	return total
}

// spin runs a dependent multiply/add chain (4 flops per iteration) and
// returns the achieved flops per second.
func spin(d time.Duration) float64 {
	const batch = 1 << 20
	x, y := 1.0, 0.5
	var flops float64
	start := time.Now()
	for time.Since(start) < d {
		for i := 0; i < batch; i++ {
			x = x*1.0000001 + 0.5
			y = y*0.9999999 - 0.25
			x = x*0.9999999 + y
			y = y*1.0000001 - x
		}
		flops += batch * 8
	}
	benchSink += x + y
	return flops / time.Since(start).Seconds()
}
