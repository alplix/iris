package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newMgr(t *testing.T, cfg Config) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m := New(dir, cfg)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	return m, dir
}

func TestGPUAndCPUWorkLiveInSeparateDirectories(t *testing.T) {
	m, dir := newMgr(t, Config{Enabled: true, CacheSizeMB: 2048, CPUCacheSizeMB: 4096, SeparateSlots: true, SeparateProjects: true})
	gpuSlot, err := m.AllocSlot(true)
	if err != nil {
		t.Fatal(err)
	}
	cpuSlot, err := m.AllocSlot(false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(gpuSlot) != filepath.Join(dir, "slots_gpu") || filepath.Dir(cpuSlot) != filepath.Join(dir, "slots") {
		t.Fatalf("slots: gpu=%s cpu=%s", gpuSlot, cpuSlot)
	}
	if m.ProjectDir(true) != filepath.Join(dir, "projects_gpu") || m.ProjectDir(false) != filepath.Join(dir, "projects") {
		t.Fatalf("project dirs: %s %s", m.ProjectDir(true), m.ProjectDir(false))
	}
}

func TestSharedLayoutWhenSeparationIsOff(t *testing.T) {
	m, dir := newMgr(t, Config{Enabled: true, CacheSizeMB: 100, CPUCacheSizeMB: 200})
	if m.SlotDir(true) != m.SlotDir(false) || m.SlotDir(true) != filepath.Join(dir, "slots") {
		t.Fatalf("slots must be shared: %s %s", m.SlotDir(true), m.SlotDir(false))
	}
	if m.ProjectDir(true) != m.ProjectDir(false) {
		t.Fatal("project files must be shared")
	}
	// One pool, so one limit: the CPU one.
	if m.Limit(true) != 200*1024*1024 || m.Limit(false) != 200*1024*1024 {
		t.Fatalf("limits: gpu=%d cpu=%d", m.Limit(true), m.Limit(false))
	}
}

func TestLimitsPerClass(t *testing.T) {
	m, _ := newMgr(t, Config{Enabled: true, CacheSizeMB: 2048, CPUCacheSizeMB: 4096, SeparateSlots: true, SeparateProjects: true})
	if m.Limit(true) != 2048<<20 || m.Limit(false) != 4096<<20 {
		t.Fatalf("limits: gpu=%d cpu=%d", m.Limit(true), m.Limit(false))
	}
	off, _ := newMgr(t, Config{Enabled: false, CacheSizeMB: 1, CPUCacheSizeMB: 1, SeparateSlots: true})
	if off.Limit(true) != 0 || off.Limit(false) != 0 || off.Full(true) {
		t.Fatal("a disabled cache must impose no limit")
	}
	zero, _ := newMgr(t, Config{Enabled: true, CacheSizeMB: 0, CPUCacheSizeMB: 0, SeparateSlots: true})
	if zero.Limit(true) != 0 || zero.Full(false) {
		t.Fatal("a size of 0 means unlimited")
	}
}

func TestUsageIsCountedPerClassAndFullIsPerClass(t *testing.T) {
	m, dir := newMgr(t, Config{Enabled: true, CacheSizeMB: 1, CPUCacheSizeMB: 8, SeparateSlots: true, SeparateProjects: true})
	// 1.5 MB of GPU work (over its 1 MB share), 2 MB of CPU work (within 8 MB).
	put(t, filepath.Join(dir, "slots_gpu", "0", "in.dat"), 1<<20)
	put(t, filepath.Join(dir, "projects_gpu", "p.example", "app.bin"), 512<<10)
	put(t, filepath.Join(dir, "slots", "0", "in.dat"), 2<<20)

	if got := m.Usage(true); got != 1<<20+512<<10 {
		t.Errorf("GPU usage = %d, want %d", got, 1<<20+512<<10)
	}
	if got := m.Usage(false); got != 2<<20 {
		t.Errorf("CPU usage = %d, want %d", got, 2<<20)
	}
	if !m.Full(true) {
		t.Error("the GPU share (1 MB) is used up")
	}
	if m.Full(false) {
		t.Error("the CPU share (8 MB) still has room; a full GPU cache must not stop CPU work")
	}
}
