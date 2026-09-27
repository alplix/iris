package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// mutableState is a StateAccessor double that actually applies state
// transitions (unlike cycle_test.go's fakeState, which only records that a
// call happened). Tests of the eligible/occupied accounting in runCycle need
// this: a task must really leave StateNew once picked, or it stays eligible
// forever and gets started again on every tick.
type mutableState struct {
	mu      sync.Mutex
	results map[string]*ResultSnapshot
	msgs    []string
}

func newMutableState(results []ResultSnapshot) *mutableState {
	m := &mutableState{results: map[string]*ResultSnapshot{}}
	for _, r := range results {
		r := r
		m.results[r.Name] = &r
	}
	return m
}

func (m *mutableState) GetResults() []ResultSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ResultSnapshot, 0, len(m.results))
	for _, r := range m.results {
		out = append(out, *r)
	}
	return out
}

func (m *mutableState) get(name string) (ResultSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.results[name]; ok {
		return *r, true
	}
	return ResultSnapshot{}, false
}

func (m *mutableState) UpdateResult(name string, state int, frac, cpu float64, exit int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.results[name]; ok {
		r.State, r.FracDone, r.CPUTime, r.ExitStatus = state, frac, cpu, exit
	}
}
func (m *mutableState) SetSlotPath(name, slot string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.results[name]; ok {
		r.Slot = slot
	}
}
func (m *mutableState) RemoveResult(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.results, name)
}
func (m *mutableState) GetTaskMode() int                               { return 1 }
func (m *mutableState) GetDiskUsage() int64                            { return 0 }
func (m *mutableState) GetDiskQuota() int64                            { return 0 }
func (m *mutableState) SetDiskUsage(v int64)                           {}
func (m *mutableState) UpdateStats(ok bool, cpu, gpu, credit float64)  {}
func (m *mutableState) RecordTaskDay(url string, ok bool, cpu float64) {}
func (m *mutableState) AddMessage(body, project string, pri int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, body)
}
func (m *mutableState) messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.msgs...)
}
func (m *mutableState) Save()                                     {}
func (m *mutableState) SetAppCPUTime(name string, cpu float64)    {}
func (m *mutableState) IsSuspended(name string) bool              { return false }
func (m *mutableState) SetOutputs(name string, outs []OutputRef)  {}
func (m *mutableState) MarkOutputUploaded(name, file string) bool { return true }

// dirCache hands out real, unique temp directories as slots and actually
// removes them on FreeSlot, so a test can tell whether a slot was freed.
type dirCache struct {
	base  string
	mu    sync.Mutex
	n     int
	freed map[string]bool
}

func newDirCache(base string) *dirCache { return &dirCache{base: base, freed: map[string]bool{}} }

func (c *dirCache) Full(gpu bool) bool { return false }
func (c *dirCache) AllocSlot(gpu bool) (string, error) {
	c.mu.Lock()
	c.n++
	n := c.n
	c.mu.Unlock()
	p := filepath.Join(c.base, fmt.Sprintf("slot%d", n))
	return p, os.MkdirAll(p, 0o755)
}
func (c *dirCache) FreeSlot(slot string) error {
	c.mu.Lock()
	c.freed[slot] = true
	c.mu.Unlock()
	return os.RemoveAll(slot)
}
func (c *dirCache) SlotDir(gpu bool) string    { return c.base }
func (c *dirCache) ProjectDir(gpu bool) string { return c.base }
func (c *dirCache) wasFreed(slot string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.freed[slot]
}
