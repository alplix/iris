package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectOutputsJudgesTheFilesATaskDeclared(t *testing.T) {
	st := &fakeState{touched: map[string]bool{}}
	e := NewEngine(st, fakeCache{}, nil, nil, nil, Config{MaxConcurrent: 1})
	slot := t.TempDir()
	r := ResultSnapshot{Name: "t", Slot: slot, Outputs: []OutputRef{{Name: "phys", OpenName: "logical.txt", MaxNBytes: 10}}}

	if code, _ := e.collectOutputs(r); code != ExitFileMissing {
		t.Errorf("a missing required output must fail the task, got %d", code)
	}
	if err := os.WriteFile(filepath.Join(slot, "logical.txt"), []byte("0123456789ab"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.collectOutputs(r); code != ExitFileTooBig {
		t.Errorf("an output over max_nbytes must fail the task, got %d", code)
	}
	if err := os.WriteFile(filepath.Join(slot, "logical.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, msg := e.collectOutputs(r); code != 0 {
		t.Errorf("a valid output should pass, got %d %s", code, msg)
	}
	r.Outputs = []OutputRef{{Name: "maybe", Optional: true}}
	if code, _ := e.collectOutputs(r); code != 0 {
		t.Errorf("a missing optional output is fine, got %d", code)
	}
}

func TestInterruptedTaskIsQueuedAgainNotLeftComputing(t *testing.T) {
	st := &fakeState{touched: map[string]bool{}, results: []ResultSnapshot{{Name: "stuck", State: StateCompute}}}
	e := NewEngine(st, fakeCache{}, nil, nil, nil, Config{MaxConcurrent: 1})
	e.runCycle()
	if !st.started("stuck") {
		t.Error("a task marked computing with nothing running it must be re-queued")
	}
}

func TestLinkOpenNameMakesTheLogicalNameReadable(t *testing.T) {
	slot := t.TempDir()
	os.WriteFile(filepath.Join(slot, "wu_123_in"), []byte("data"), 0o644)
	if err := linkOpenName(slot, FileRef{Name: "wu_123_in", OpenName: "in.txt"}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(slot, "in.txt")); err != nil || string(b) != "data" {
		t.Errorf("logical name not readable: %q %v", b, err)
	}
	if err := linkOpenName(slot, FileRef{Name: "wu_123_in", OpenName: "../escape"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(slot), "escape")); err == nil {
		t.Error("an open_name with a path must never be written outside the slot")
	}
}

func TestOwnApplicationIsCopiedFromTheProjectFolder(t *testing.T) {
	proj, slot := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(proj, "myapp.exe"), []byte("binary"), 0o644)
	e := NewEngine(&fakeState{touched: map[string]bool{}}, fakeCache{}, nil, nil, nil, Config{MaxConcurrent: 1})
	r := ResultSnapshot{Name: "t", Slot: slot, Files: []FileRef{{Name: "myapp.exe", OpenName: "run", MainProgram: true, LocalPath: filepath.Join(proj, "myapp.exe")}}}
	if err := e.downloadFiles(r); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"myapp.exe", "run"} {
		if b, err := os.ReadFile(filepath.Join(slot, n)); err != nil || string(b) != "binary" {
			t.Errorf("%s not in the slot: %q %v", n, b, err)
		}
	}
	r.Files[0].LocalPath = filepath.Join(proj, "missing.exe")
	if err := e.downloadFiles(r); err == nil || !strings.Contains(err.Error(), "app_info.xml") {
		t.Errorf("a file named in app_info.xml but absent must be explained, got %v", err)
	}
}
