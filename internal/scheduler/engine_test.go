package scheduler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeState is a minimal StateManager double for testing the engine without
// the real irisd state package.
type fakeState struct {
	mu       sync.Mutex
	projects []ProjectInfo
	messages []string
	pending  map[string]bool
}

func newFakeState(projects ...ProjectInfo) *fakeState {
	return &fakeState{projects: projects, pending: map[string]bool{}}
}

func (f *fakeState) GetProjects() []ProjectInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ProjectInfo(nil), f.projects...)
}
func (f *fakeState) AddResult(ResultInfo)                                           {}
func (f *fakeState) GetResults() []ResultInfo                                       { return nil }
func (f *fakeState) RemoveResult(string)                                            {}
func (f *fakeState) GetHostInfo() HostInfoSnapshot                                  { return HostInfoSnapshot{} }
func (f *fakeState) GetNetworkMode() int                                            { return 0 }
func (f *fakeState) GetDiskUsage() int64                                            { return 0 }
func (f *fakeState) SetDiskUsage(int64)                                             {}
func (f *fakeState) UpdateStats(bool, float64, float64, float64)                    {}
func (f *fakeState) UpdateProjectCredit(string, float64, float64, float64, float64) {}
func (f *fakeState) Save()                                                          {}

func (f *fakeState) AddMessage(body, _ string, _ int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, body)
}

func (f *fakeState) SetProjectSchedPending(url string, pending bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[url] = pending
}

func (f *fakeState) Messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func (f *fakeState) Pending(url string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending[url]
}

type fakeCache struct{}

func (fakeCache) AllocSlot(bool) (string, error) { return "", nil }
func (fakeCache) FreeSlot(string) error          { return nil }
func (fakeCache) SlotDir(bool) string            { return "" }
func (fakeCache) ProjectDir(bool) string         { return "" }

// TestDoRPCClearsPendingAndLogsOnFailure guards against the bug where a
// project that was ever updated (or whose scheduler contact simply failed —
// an unreachable real project server, say) showed "updating" in the UI
// forever: SchedRPCPending was set by the "Update" button but nothing ever
// cleared it, and a failed contact left no trace anywhere the person could
// see it.
func TestDoRPCClearsPendingAndLogsOnFailure(t *testing.T) {
	const url = "http://127.0.0.1:1/" // nothing listens on port 1
	fs := newFakeState(ProjectInfo{URL: url, Name: "X"})
	fs.SetProjectSchedPending(url, true)

	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: url})

	if fs.Pending(url) {
		t.Error("a failed contact must still clear the pending flag, or the UI shows \"updating\" forever")
	}
	msgs := fs.Messages()
	if len(msgs) == 0 || !strings.Contains(msgs[0], "Couldn't reach") {
		t.Errorf("expected a failure message explaining what went wrong, got %v", msgs)
	}
}

// TestDoRPCLogsAContactConfirmationOnQuietSuccess guards against a
// successful-but-uneventful scheduler contact (no message from the server,
// no new work) leaving zero trace — making "never tried" indistinguishable
// from "tried, nothing to do".
func TestDoRPCLogsAContactConfirmationOnQuietSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(`<scheduler_reply></scheduler_reply>`))
	}))
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Quiet"})
	fs.SetProjectSchedPending(srv.URL, true)
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})

	if fs.Pending(srv.URL) {
		t.Error("pending flag should be cleared after a successful contact")
	}
	msgs := fs.Messages()
	if len(msgs) == 0 || !strings.Contains(msgs[0], "Contacted") {
		t.Errorf("expected a contact confirmation message, got %v", msgs)
	}
}

// TestDoRPCDiscoversTheRealSchedulerURLFromTheMasterPage reproduces the
// exact real-world failure this was written for: a project (like
// Einstein@Home) whose scheduler lives at a completely different address
// than "<master_url>/cgi-bin/scheduler" — that conventional path 404s here
// on purpose, so the test only passes if discovery, not the guess, is what
// found the real one.
func TestDoRPCDiscoversTheRealSchedulerURLFromTheMasterPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/cgi-bin/scheduler", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // the naive guess must NOT be what makes this pass
	})
	mux.HandleFunc("/unconventional/cgi", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<scheduler_reply></scheduler_reply>`))
	})
	var realSchedulerURL string
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head>
  <!-- Project scheduling servers -->
  <link rel="boinc_scheduler" href="` + realSchedulerURL + `">
</head></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	realSchedulerURL = srv.URL + "/unconventional/cgi"

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Real Project"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})

	msgs := fs.Messages()
	if len(msgs) == 0 || !strings.Contains(msgs[0], "Contacted") {
		t.Errorf("expected a successful contact via the discovered scheduler URL, got %v", msgs)
	}
}

// TestForceOneContactsAProjectNotYetTrackedByAPeriodicCycle guards
// RequestUpdate's real-world use case: a project attached moments ago,
// before any periodic runCycle has discovered it into e.projects yet.
func TestForceOneContactsAProjectNotYetTrackedByAPeriodicCycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<scheduler_reply></scheduler_reply>`))
	}))
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Fresh"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	// Note: e.projects is deliberately still empty here.
	e.forceOne(srv.URL)

	msgs := fs.Messages()
	if len(msgs) == 0 || !strings.Contains(msgs[0], "Contacted") {
		t.Errorf("expected forceOne to contact the project and log it, got %v", msgs)
	}
}
