package worker

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Runs a real BOINC application (from a slot directory whose path is given in
// IRIS_REAL_APP_SLOT) with the shared-memory channel and checks it talks
// through it: status arrives, and a <quit/> makes it stop. It is skipped
// unless that variable is set, since it needs a real project application.
func TestRealBOINCApplicationUsesTheSharedMemoryChannel(t *testing.T) {
	src := os.Getenv("IRIS_REAL_APP_SLOT")
	if src == "" {
		t.Skip("set IRIS_REAL_APP_SLOT to a slot directory holding a real BOINC application")
	}
	slot := t.TempDir()
	var exe string
	entries, _ := os.ReadDir(src)
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == "init_data.xml" || n == "stderr.txt" || n == "stdout.txt" || n == "boinc_lockfile" || n == "checkpoint" || strings.HasSuffix(n, "_checkpoint") || n == "out" {
			continue
		}
		in, err := os.Open(filepath.Join(src, n))
		if err != nil {
			t.Fatal(err)
		}
		out, _ := os.Create(filepath.Join(slot, n))
		io.Copy(out, in)
		in.Close()
		out.Close()
		if strings.HasPrefix(n, "GetDecics") || strings.HasSuffix(n, ".exe") {
			exe = filepath.Join(slot, n)
		}
	}
	if exe == "" {
		t.Skip("no executable found in the slot")
	}
	shm, err := createShmem(slot)
	if err != nil {
		t.Fatal(err)
	}
	defer shm.Close()
	r := ResultSnapshot{Name: "real_test", WuName: "wu", Slot: slot, AppVersionNum: 400}
	if err := os.WriteFile(filepath.Join(slot, "init_data.xml"), buildInitDataXML(r, "", "data", 60, shm.initTag), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = slot
	errf, _ := os.Create(filepath.Join(slot, "stderr.txt"))
	cmd.Stderr, cmd.Stdout = errf, errf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); errf.Close() }()

	var got appStatus
	seen := 0
	deadline := time.After(20 * time.Second)
loop:
	for {
		select {
		case err := <-done:
			t.Fatalf("the application exited on its own: %v", err)
		case <-deadline:
			break loop
		case <-time.After(500 * time.Millisecond):
			shm.send(chHeartbeat, "<heartbeat/>")
			if msg, ok := shm.get(chAppStatus); ok {
				if st, ok := parseAppStatus(msg); ok {
					got = st
					seen++
				}
			}
		}
	}
	t.Logf("status messages seen: %d, last: %+v", seen, got)
	if seen == 0 {
		b, _ := os.ReadFile(filepath.Join(slot, "stderr.txt"))
		t.Fatalf("the application never reported through shared memory; its stderr:\n%.600s", b)
	}
	if got.cpuTime <= 0 {
		t.Errorf("expected a CPU time, got %+v", got)
	}
	shm.sendOverwrite(chProcessControlRequest, "<quit/>")
	select {
	case err := <-done:
		t.Logf("exited after <quit/>: %v", err)
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Error("the application ignored <quit/>")
	}
	b, _ := os.ReadFile(filepath.Join(slot, "stderr.txt"))
	t.Logf("stderr head: %.400s", b)
}
