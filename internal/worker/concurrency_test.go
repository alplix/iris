package worker

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// blockingDownloader simulates a slow download: every call blocks until
// release is closed, and inFlight tracks how many calls are blocked right now.
type blockingDownloader struct {
	release  chan struct{}
	inFlight *int32
}

func (d *blockingDownloader) DownloadFile(projectURL, filename, destPath string) error {
	return d.block()
}
func (d *blockingDownloader) DownloadFileByURL(url, destPath string) error { return d.block() }
func (d *blockingDownloader) UploadFile(projectURL, filePath string) error { return nil }
func (d *blockingDownloader) block() error {
	atomic.AddInt32(d.inFlight, 1)
	defer atomic.AddInt32(d.inFlight, -1)
	<-d.release
	return errors.New("test stop: never really downloads")
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestOccupiedCountIncludesStillDownloadingTasks guards against the bug a
// real Ryzen 9 3900X (24 threads) hit: with max_concurrent honoured only by
// counting StateCompute tasks, a task still downloading was invisible to the
// count, so every 5-second tick started a fresh batch on top of whatever the
// last tick's downloads hadn't finished yet — 50 tasks ended up running on a
// 24-thread host. It must count a task as occupying a slot for its whole time
// in the pipeline, download included.
func TestOccupiedCountIncludesStillDownloadingTasks(t *testing.T) {
	var inFlight int32
	// release is intentionally never closed: these downloads stay parked for
	// the rest of the test binary's run, which is exactly what "still
	// downloading" means here and keeps their eventual retry/error handling
	// from logging in the background after this test has already finished.
	dl := &blockingDownloader{release: make(chan struct{}), inFlight: &inFlight}

	var results []ResultSnapshot
	for i := 0; i < 6; i++ {
		results = append(results, ResultSnapshot{
			Name: fmt.Sprintf("t%d", i), State: StateNew,
			Files: []FileRef{{Name: "f", URL: "http://x/f"}},
		})
	}
	st := newMutableState(results)
	cache := newDirCache(t.TempDir())
	e := NewEngine(st, cache, nil, dl, dl, Config{MaxConcurrent: 2})

	e.runCycle()
	waitFor(t, func() bool { return atomic.LoadInt32(&inFlight) == 2 }, "2 downloads to start")

	// Simulate several more 5-second ticks while those first two are still
	// "downloading". Before the fix, each tick added more on top since the
	// occupied count only saw StateCompute tasks (none yet).
	for i := 0; i < 4; i++ {
		e.runCycle()
		time.Sleep(80 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&inFlight); got > 2 {
		t.Fatalf("max_concurrent=2 but %d tasks were downloading/running at once", got)
	}
}

// TestSuspendedComputingTaskStillFreesASlot guards the one deliberate
// exception: a task the person paused draws no CPU, so a new one may start in
// its place, exactly as before this fix.
func TestSuspendedComputingTaskStillFreesASlot(t *testing.T) {
	st := newMutableState([]ResultSnapshot{
		{Name: "paused", State: StateCompute, Suspended: 1},
		{Name: "new", State: StateNew},
	})
	e := NewEngine(st, fakeCache{}, nil, nil, nil, Config{MaxConcurrent: 1})
	e.setActive("paused", true) // as if runApp were mid-flight for it
	e.runCycle()
	waitFor(t, func() bool { r, ok := st.get("new"); return ok && r.State != StateNew }, "the new task to start despite the paused one")
}
