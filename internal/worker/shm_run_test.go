package worker

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestShmHelperApp is not a test: the test below runs this test binary as a
// BOINC application that talks only through the shared-memory channel.
func TestShmHelperApp(t *testing.T) {
	if os.Getenv("IRIS_SHM_HELPER") != "1" {
		return
	}
	init, err := os.ReadFile("init_data.xml")
	if err != nil {
		os.Exit(3)
	}
	mem, err := attachAsApp(string(init))
	if err != nil {
		os.Stderr.WriteString("attach: " + err.Error() + "\n")
		os.Exit(4)
	}
	status := mem[5*1024 : 6*1024]
	control := mem[0:1024]
	up := mem[6*1024 : 7*1024]
	down := mem[7*1024 : 8*1024]
	sentUp := false
	for {
		if !sentUp && up[0] == 0 {
			os.WriteFile("trickle_up.xml", []byte("<variety>credit</variety>\n<cpu>1</cpu>\n"), 0o644)
			msg := "<have_new_trickle_up/>\n"
			copy(up[1:], msg)
			up[1+len(msg)] = 0
			up[0] = 1
			sentUp = true
		}
		if down[0] != 0 {
			down[0] = 0
			os.WriteFile("got_trickle_down", []byte("1"), 0o644)
		}
		if status[0] == 0 {
			msg := "<current_cpu_time>7.5</current_cpu_time>\n<checkpoint_cpu_time>0</checkpoint_cpu_time>\n<want_network>0</want_network>\n<fraction_done>4.000000e-01</fraction_done>\n"
			copy(status[1:], msg)
			status[1+len(msg)] = 0
			status[0] = 1
		}
		if control[0] != 0 {
			end := 1
			for control[end] != 0 {
				end++
			}
			msg := string(control[1:end])
			control[0] = 0
			switch {
			case strings.Contains(msg, "<suspend/>"):
				os.WriteFile("suspended", []byte("1"), 0o644)
			case strings.Contains(msg, "<resume/>"):
				os.WriteFile("resumed", []byte("1"), 0o644)
			case strings.Contains(msg, "<quit/>"):
				os.WriteFile("quit", []byte("1"), 0o644)
				os.Exit(0)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// progState records what the worker reports about one task.
type progState struct {
	fakeState
	pm        sync.Mutex
	frac      float64
	appCPU    float64
	suspended bool
	states    []int
}

func (p *progState) UpdateResult(name string, state int, frac, cpu float64, exit int) {
	p.pm.Lock()
	p.frac = frac
	p.states = append(p.states, state)
	p.pm.Unlock()
	p.fakeState.UpdateResult(name, state, frac, cpu, exit)
}
func (p *progState) SetAppCPUTime(name string, cpu float64) {
	p.pm.Lock()
	p.appCPU = cpu
	p.pm.Unlock()
}
func (p *progState) IsSuspended(string) bool {
	p.pm.Lock()
	defer p.pm.Unlock()
	return p.suspended
}
func (p *progState) snapshot() (frac, cpu float64) {
	p.pm.Lock()
	defer p.pm.Unlock()
	return p.frac, p.appCPU
}
func (p *progState) setSuspended(v bool) {
	p.pm.Lock()
	p.suspended = v
	p.pm.Unlock()
}

// The worker must take the application's own progress and CPU time from the
// channel, suspend and resume it through the channel, and ask it to quit
// (letting it exit by itself) when the client stops.
func TestWorkerTalksToAnApplicationThroughSharedMemory(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("IRIS_SHM_HELPER", "1")
	slot := t.TempDir()
	st := &progState{fakeState: fakeState{touched: map[string]bool{}}}
	dataDir := t.TempDir()
	e := NewEngine(st, fakeCache{}, nil, nil, nil, Config{MaxConcurrent: 1, CheckpointSec: 3600, DataDir: dataDir})
	shm, err := createShmem(slot)
	if err != nil {
		t.Fatal(err)
	}
	r := ResultSnapshot{Name: "shm-task", Slot: slot, ProjectURL: "https://p.example/", CmdLine: "-test.run=^TestShmHelperApp$"}
	if err := os.WriteFile(filepath.Join(slot, "init_data.xml"), buildInitDataXML(r, "", "data", 60, shm.initTag), 0o644); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		e.runApp(r, self, shm)
		close(finished)
	}()
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if ok() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}

	// The 10 s progress tick is the slowest part; the shared-memory poll itself
	// runs every second, so the CPU time shows first.
	waitFor("the application's CPU time", func() bool { _, cpu := st.snapshot(); return cpu == 7.5 })

	// Trickle-up: the app's file must land in the project folder under the
	// name the scheduler side sends it by.
	waitFor("the trickle-up in the project folder", func() bool {
		entries, _ := os.ReadDir(filepath.Join(dataDir, "projects", "p.example"))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "trickle_up_shm-task_") {
				return true
			}
		}
		return false
	})
	// Trickle-down: a file the scheduler side put in the slot must be announced.
	os.WriteFile(filepath.Join(slot, "trickle_down_7"), []byte("<msg>x</msg>"), 0o644)
	waitFor("the app to be told of the trickle-down", func() bool { _, err := os.Stat(filepath.Join(slot, "got_trickle_down")); return err == nil })

	st.setSuspended(true)
	waitFor("a <suspend/> message", func() bool { _, err := os.Stat(filepath.Join(slot, "suspended")); return err == nil })
	st.setSuspended(false)
	waitFor("a <resume/> message", func() bool { _, err := os.Stat(filepath.Join(slot, "resumed")); return err == nil })

	e.Stop()
	select {
	case <-finished:
	case <-time.After(40 * time.Second):
		t.Fatal("runApp did not return after the client stopped")
	}
	if _, err := os.Stat(filepath.Join(slot, "quit")); err != nil {
		t.Error("the application must be asked to quit through the channel, not killed")
	}
}
