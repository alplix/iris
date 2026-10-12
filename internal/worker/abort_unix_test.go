//go:build !windows

package worker

import (
	"os/exec"
	"testing"
	"time"
)

// A program that ignores the termination request must still be stopped by an
// abort, once the grace period is over.
func TestAbortKillsAProgramThatIgnoresTheTerminationRequest(t *testing.T) {
	cmd := exec.Command("sh", "-c", `trap "" TERM; while true; do sleep 1; done`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // let the shell install its trap
	e := &Engine{
		running: map[string]*exec.Cmd{"stubborn": cmd},
		active:  map[string]bool{"stubborn": true},
		aborted: map[string]bool{},
		cache:   newDirCache(t.TempDir()),
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	terminateProcess(cmd)
	go e.killIfStillRunning("stubborn", cmd, 500*time.Millisecond)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("a program that ignores SIGTERM was never killed")
	}
}
