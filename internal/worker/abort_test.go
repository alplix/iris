package worker

import (
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestAbortKillsARunningProcess is the case the forum report's "no way to
// stop a runaway task" pointed at: a task already running must actually be
// killed, not just dropped from the list while the process keeps going.
func TestAbortKillsARunningProcess(t *testing.T) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	} else {
		cmd = exec.Command("sleep", "30")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	e := &Engine{
		running: map[string]*exec.Cmd{"victim": cmd},
		active:  map[string]bool{"victim": true},
		aborted: map[string]bool{},
		cache:   newDirCache(t.TempDir()),
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	e.Abort("victim", "/some/slot")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Abort did not actually kill the running process")
	}
}

func TestAbortOfAnInactiveTaskFreesItsSlotImmediately(t *testing.T) {
	cache := newDirCache(t.TempDir())
	e := &Engine{running: map[string]*exec.Cmd{}, active: map[string]bool{}, aborted: map[string]bool{}, cache: cache}
	e.Abort("gone", "/some/slot/path")
	if !cache.wasFreed("/some/slot/path") {
		t.Error("aborting a task with nothing active for it must free its slot right away")
	}
	if e.isAborted("gone") {
		t.Error("the abort mark must be cleared once handled")
	}
}

// TestAbortWhileDownloadingFreesTheSlotOnceItStops covers the case there is no
// lower-level cancellation for: the slot must stay in place while the
// download is still actually happening, and be freed as soon as it stops.
func TestAbortWhileDownloadingFreesTheSlotOnceItStops(t *testing.T) {
	dl := &blockingDownloader{release: make(chan struct{}), inFlight: new(int32)}
	st := newMutableState([]ResultSnapshot{
		{Name: "victim", State: StateNew, Files: []FileRef{{Name: "f", URL: "http://x/f"}}},
	})
	cache := newDirCache(t.TempDir())
	e := NewEngine(st, cache, nil, dl, dl, Config{MaxConcurrent: 1})

	e.runCycle()
	waitFor(t, func() bool { return atomic.LoadInt32(dl.inFlight) == 1 }, "the download to start")

	r, ok := st.get("victim")
	if !ok || r.Slot == "" {
		t.Fatalf("expected a slot to have been allocated: %+v ok=%v", r, ok)
	}
	e.Abort("victim", r.Slot)
	if cache.wasFreed(r.Slot) {
		t.Fatal("the slot must not be freed before the download actually stops")
	}
	close(dl.release)
	waitFor(t, func() bool { return cache.wasFreed(r.Slot) }, "the slot to be freed once the download stops")
}
