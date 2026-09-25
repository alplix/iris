package worker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeState struct {
	mu      sync.Mutex
	results []ResultSnapshot
	touched map[string]bool
}

func (f *fakeState) GetResults() []ResultSnapshot { return f.results }
func (f *fakeState) UpdateResult(name string, state int, frac, cpu float64, exit int) {
	f.mu.Lock()
	f.touched[name] = true
	f.mu.Unlock()
}
func (f *fakeState) SetSlotPath(name, slotPath string)                  {}
func (f *fakeState) RemoveResult(name string)                           {}
func (f *fakeState) GetTaskMode() int                                   { return 1 }
func (f *fakeState) GetDiskUsage() int64                                { return 0 }
func (f *fakeState) GetDiskQuota() int64                                { return 0 }
func (f *fakeState) SetDiskUsage(v int64)                               {}
func (f *fakeState) UpdateStats(ok bool, cpu, gpu, credit float64)      {}
func (f *fakeState) RecordTaskDay(url string, ok bool, cpuTime float64) {}
func (f *fakeState) AddMessage(body, project string, pri int)           {}
func (f *fakeState) Save()                                              {}
func (f *fakeState) SetAppCPUTime(name string, cpu float64)             {}
func (f *fakeState) IsSuspended(name string) bool                       { return false }
func (f *fakeState) SetOutputs(name string, outs []OutputRef)           {}
func (f *fakeState) MarkOutputUploaded(name, file string) bool          { return true }
func (f *fakeState) started(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.touched[name]
}

// fakeCache has a full GPU share and refuses to hand out slots, so a started
// task ends immediately (and is recorded by fakeState).
type fakeCache struct{ gpuFull bool }

func (c fakeCache) Full(gpu bool) bool                 { return gpu && c.gpuFull }
func (c fakeCache) AllocSlot(gpu bool) (string, error) { return "", errors.New("no slot in test") }
func (c fakeCache) FreeSlot(slot string) error         { return nil }
func (c fakeCache) SlotDir(gpu bool) string            { return "" }
func (c fakeCache) ProjectDir(gpu bool) string         { return "" }

func TestFullGPUCacheStopsGPUWorkButNotCPUWork(t *testing.T) {
	st := &fakeState{touched: map[string]bool{}, results: []ResultSnapshot{
		{Name: "gpu-task", GPU: true, State: StateNew},
		{Name: "cpu-task", GPU: false, State: StateNew},
	}}
	e := NewEngine(st, fakeCache{gpuFull: true}, nil, nil, nil, Config{MaxConcurrent: 4})
	e.runCycle()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !st.started("cpu-task") {
		time.Sleep(10 * time.Millisecond)
	}
	if !st.started("cpu-task") {
		t.Fatal("CPU work must start while only the GPU cache is full")
	}
	time.Sleep(200 * time.Millisecond)
	if st.started("gpu-task") {
		t.Fatal("GPU work must not start while the GPU cache is full")
	}

	// With room again, the GPU task is picked up on the next cycle.
	e2 := NewEngine(st, fakeCache{gpuFull: false}, nil, nil, nil, Config{MaxConcurrent: 4})
	e2.runCycle()
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !st.started("gpu-task") {
		time.Sleep(10 * time.Millisecond)
	}
	if !st.started("gpu-task") {
		t.Fatal("GPU work should start once its cache has room")
	}
}

func TestSwitchedOffGPUStopsGPUTasksButNotCPUTasks(t *testing.T) {
	st := &fakeState{touched: map[string]bool{}, results: []ResultSnapshot{
		{Name: "gpu-task", GPU: true, State: StateNew},
		{Name: "cpu-task", GPU: false, State: StateNew},
	}}
	e := NewEngine(st, fakeCache{}, nil, nil, nil, Config{MaxConcurrent: 4, GPUEnabledFn: func() bool { return false }})
	e.runCycle()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !st.started("cpu-task") {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if !st.started("cpu-task") {
		t.Error("CPU work must still run")
	}
	if st.started("gpu-task") {
		t.Error("GPU work must not start while GPU use is switched off")
	}
}
