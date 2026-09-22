package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a real test. It's exec'd as a subprocess by
// TestPauseThenResumeStopsAndContinuesARealProcess to get a real, observable
// OS process to suspend — the standard os/exec self-exec pattern, since a
// helper binary would otherwise need its own build step.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("IRIS_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fp := os.Args[len(os.Args)-1]
	for n := 1; ; n++ {
		os.WriteFile(fp, []byte(strconv.Itoa(n)), 0o644)
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPauseThenResumeStopsAndContinuesARealProcess is a live check of
// pauseProcess/resumeProcess (pause_unix.go's SIGSTOP/SIGCONT, or
// pause_windows.go's NtSuspendProcess/NtResumeProcess) against a real,
// running process on whatever OS this test runs on — not a mock — since a
// wrong syscall here would otherwise only ever be discovered by a person
// clicking "suspend" on a real task.
func TestPauseThenResumeStopsAndContinuesARealProcess(t *testing.T) {
	dir := t.TempDir()
	counterFile := filepath.Join(dir, "counter")

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "-test.v")
	cmd.Args = append(cmd.Args, "--", counterFile)
	cmd.Env = append(os.Environ(), "IRIS_WANT_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	defer cmd.Process.Kill()

	readCounter := func() int {
		data, err := os.ReadFile(counterFile)
		if err != nil {
			return 0
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return n
	}

	deadline := time.Now().Add(3 * time.Second)
	for readCounter() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if readCounter() == 0 {
		t.Fatal("helper process never started counting")
	}

	if err := pauseProcess(cmd); err != nil {
		t.Fatalf("pauseProcess: %v", err)
	}
	time.Sleep(80 * time.Millisecond) // let any in-flight tick land
	frozen := readCounter()
	time.Sleep(400 * time.Millisecond)
	if got := readCounter(); got != frozen {
		t.Fatalf("counter advanced from %d to %d while suspended", frozen, got)
	}

	if err := resumeProcess(cmd); err != nil {
		t.Fatalf("resumeProcess: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for readCounter() <= frozen && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := readCounter(); got <= frozen {
		t.Fatalf("counter did not advance after resume: still %d", got)
	}
}
