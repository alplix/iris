package scheduler

import (
	"bytes"
	"io"
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
	results  []ResultInfo
	added    []ResultInfo
	hostInfo HostInfoSnapshot
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

func (f *fakeState) AddResult(r ResultInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, r)
}

func (f *fakeState) Added() []ResultInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ResultInfo(nil), f.added...)
}

func (f *fakeState) GetResults() []ResultInfo                                       { return f.results }
func (f *fakeState) RemoveResult(string)                                            {}
func (f *fakeState) GetHostInfo() HostInfoSnapshot                                  { return f.hostInfo }
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

// TestWorkFetchRequestAsksForIdleCoresOnly guards the actual bug this fixes:
// a scheduler request with every work-fetch field at its zero value tells
// real schedulers "I don't want anything", so a freshly attached project
// could sit at zero tasks forever even once contact itself succeeded.
func TestWorkFetchRequestAsksForIdleCoresOnly(t *testing.T) {
	workSecs, cpuSecs, instances := workFetchRequest(4, 0)
	if instances != 4 {
		t.Errorf("4 idle cores should request 4 instances, got %v", instances)
	}
	if workSecs <= 0 || cpuSecs <= 0 {
		t.Errorf("an idle machine must ask for a nonzero amount of work, got work=%v cpu=%v", workSecs, cpuSecs)
	}

	if _, _, instances := workFetchRequest(4, 2); instances != 2 {
		t.Errorf("2 of 4 cores already queued should request 2 more instances, got %v", instances)
	}

	if workSecs, cpuSecs, instances := workFetchRequest(4, 4); workSecs != 0 || cpuSecs != 0 || instances != 0 {
		t.Errorf("a fully queued machine should request nothing, got work=%v cpu=%v instances=%v", workSecs, cpuSecs, instances)
	}

	if _, _, instances := workFetchRequest(4, 10); instances != 0 {
		t.Errorf("more already queued than cores should never request a negative amount, got %v", instances)
	}
}

func TestCountQueuedForProjectIgnoresOtherProjectsAndFinishedTasks(t *testing.T) {
	results := []ResultInfo{
		{ProjectURL: "a", State: 0}, // downloading
		{ProjectURL: "a", State: 2}, // computing
		{ProjectURL: "a", State: 4}, // ready to report — no longer occupies a slot
		{ProjectURL: "a", State: 5}, // error — likewise
		{ProjectURL: "b", State: 0}, // a different project entirely
	}
	if n := countQueuedForProject(results, "a"); n != 2 {
		t.Errorf("countQueuedForProject(a) = %d, want 2", n)
	}
	if n := countQueuedForProject(results, "b"); n != 1 {
		t.Errorf("countQueuedForProject(b) = %d, want 1", n)
	}
}

// TestDoRPCSendsANonZeroWorkRequest is an end-to-end check that the fields
// computed above actually reach the wire — not just that the pure function
// works in isolation.
func TestDoRPCSendsANonZeroWorkRequest(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			gotBody, _ = io.ReadAll(r.Body)
		}
		w.Write([]byte(`<scheduler_reply></scheduler_reply>`))
	}))
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "X"})
	fs.hostInfo = HostInfoSnapshot{Ncpus: 4}
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})

	if !bytes.Contains(gotBody, []byte("<cpu_req_instances>4")) {
		t.Errorf("expected a request for 4 idle instances, got body:\n%s", gotBody)
	}
}

// TestMatchAppVersionPrefersExactVersionAndPlanClass guards the heuristic
// documented on matchAppVersion: a result only carries app_version_num and
// plan_class, never a direct pointer to one <app_version> block, so matching
// has to be inferred from those two fields (falling back to version_num
// alone, then to "there was only one on offer" when even plan_class isn't
// enough to disambiguate).
func TestMatchAppVersionPrefersExactVersionAndPlanClass(t *testing.T) {
	versions := []AppVersionXML{
		{AppName: "cpu_app", VersionNum: 5, PlanClass: ""},
		{AppName: "cuda_app", VersionNum: 5, PlanClass: "cuda_fma"},
		{AppName: "old_cpu_app", VersionNum: 3, PlanClass: ""},
	}

	if av, ok := matchAppVersion(versions, ReplyResult{AppVersionNum: 5, PlanClass: "cuda_fma"}); !ok || av.AppName != "cuda_app" {
		t.Errorf("expected an exact (version, plan_class) match to win, got %+v (ok=%v)", av, ok)
	}
	if av, ok := matchAppVersion(versions, ReplyResult{AppVersionNum: 3, PlanClass: ""}); !ok || av.AppName != "old_cpu_app" {
		t.Errorf("expected version_num=3 to match old_cpu_app, got %+v (ok=%v)", av, ok)
	}
	if _, ok := matchAppVersion(versions, ReplyResult{AppVersionNum: 99}); ok {
		t.Error("a version_num nothing offers should not match")
	}
	if av, ok := matchAppVersion([]AppVersionXML{{AppName: "only_one", VersionNum: 1}}, ReplyResult{AppVersionNum: 42}); !ok || av.AppName != "only_one" {
		t.Errorf("a single app_version on offer should match even on a version_num mismatch, got %+v (ok=%v)", av, ok)
	}
}

// TestDoRPCAttachesTheRealAppExecutableFromAppVersion is the end-to-end
// check for the roadmap's "run real applications" item: a scheduler reply
// with an <app_version> naming the real executable must result in a
// ResultInfo whose Files carries that executable with MainProgram set, not
// just the workunit's own input files.
func TestDoRPCAttachesTheRealAppExecutableFromAppVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(`<scheduler_reply>
  <app_version>
    <app_name>my_app</app_name>
    <version_num>7</version_num>
    <platform>x86_64-pc-linux-gnu</platform>
    <file_ref>
      <file_name>my_app_7_x86_64-pc-linux-gnu</file_name>
      <main_program/>
    </file_ref>
  </app_version>
  <file_info>
    <name>my_app_7_x86_64-pc-linux-gnu</name>
    <url>http://example.invalid/download/my_app</url>
    <nbytes>12345</nbytes>
  </file_info>
  <result>
    <name>wu_1_0</name>
    <wu_name>wu_1</wu_name>
    <app_version_num>7</app_version_num>
  </result>
</scheduler_reply>`))
	}))
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "RealApp"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})

	added := fs.Added()
	if len(added) != 1 {
		t.Fatalf("expected exactly one result to be added, got %d", len(added))
	}
	r := added[0]
	if r.AppName != "my_app" {
		t.Errorf("AppName = %q, want %q", r.AppName, "my_app")
	}
	var mainProgram *FileInfo
	for i := range r.Files {
		if r.Files[i].MainProgram {
			mainProgram = &r.Files[i]
		}
	}
	if mainProgram == nil {
		t.Fatalf("expected one file flagged MainProgram, got files=%+v", r.Files)
	}
	if mainProgram.Name != "my_app_7_x86_64-pc-linux-gnu" {
		t.Errorf("main program file name = %q, want the app_version's own file_ref name", mainProgram.Name)
	}
}

// TestBuildCoprocsXMLOmitsTheElementForAGPUlessHost guards a real, easy to
// miss mistake: sending an empty <coprocs/> (as opposed to no element at
// all) can itself signal "coprocessor-capable" to some schedulers, which
// would misrepresent a host with no GPU at all.
func TestBuildCoprocsXMLOmitsTheElementForAGPUlessHost(t *testing.T) {
	if c := buildCoprocsXML(HostInfoSnapshot{}); c != nil {
		t.Errorf("a GPU-less host must get a nil <coprocs>, got %+v", c)
	}
}

func TestBuildCoprocsXMLAdvertisesEachDetectedVendor(t *testing.T) {
	c := buildCoprocsXML(HostInfoSnapshot{NvidiaCount: 2, NvidiaName: "GeForce RTX 4080 SUPER", AtiCount: 1, AtiName: "Radeon RX 7900"})
	if c == nil {
		t.Fatal("expected a non-nil <coprocs> for a host with GPUs")
	}
	if c.CUDA == nil || c.CUDA.Count != 2 || c.CUDA.Name != "GeForce RTX 4080 SUPER" || c.CUDA.HaveCUDA != 1 {
		t.Errorf("coproc_cuda = %+v, want count=2 name=GeForce RTX 4080 SUPER have_cuda=1", c.CUDA)
	}
	if c.ATI == nil || c.ATI.Count != 1 || c.ATI.Name != "Radeon RX 7900" {
		t.Errorf("coproc_ati = %+v, want count=1 name=Radeon RX 7900", c.ATI)
	}
	if c.CUDA.PeakFlops != 0 {
		t.Error("peak_flops must stay 0 (unmeasured) rather than a fabricated number")
	}
}

// TestDoRPCAdvertisesGPUsOnlyWhenTheHostHasThem is the end-to-end check that
// a detected NVIDIA GPU actually reaches the wire, and that a GPU-less host
// sends no <coprocs> at all.
func TestDoRPCAdvertisesGPUsOnlyWhenTheHostHasThem(t *testing.T) {
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			lastBody, _ = io.ReadAll(r.Body)
		}
		w.Write([]byte(`<scheduler_reply></scheduler_reply>`))
	}))
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "NoGPU"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})
	if bytes.Contains(lastBody, []byte("coprocs")) {
		t.Errorf("a GPU-less host must not send <coprocs>, got body:\n%s", lastBody)
	}

	fs.hostInfo = HostInfoSnapshot{NvidiaCount: 1, NvidiaName: "GeForce RTX 4080 SUPER"}
	e.doRPC(&ProjectState{URL: srv.URL})
	if !bytes.Contains(lastBody, []byte("<coproc_cuda>")) || !bytes.Contains(lastBody, []byte("GeForce RTX 4080 SUPER")) {
		t.Errorf("expected the detected NVIDIA GPU on the wire, got body:\n%s", lastBody)
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
