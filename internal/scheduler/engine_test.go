package scheduler

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alplix/iris/internal/detect"
	"github.com/alplix/iris/internal/product"
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
	names    map[string]string
	rpc      map[string][2]int
}

func newFakeState(projects ...ProjectInfo) *fakeState {
	return &fakeState{projects: projects, pending: map[string]bool{}, names: map[string]string{}, rpc: map[string][2]int{}}
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

func (f *fakeState) SetProjectName(url, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names[url] = name
}

func (f *fakeState) SetProjectRPCState(url string, hostID, rpcSeqno int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rpc[url] = [2]int{hostID, rpcSeqno}
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

	if av, ok := matchAppVersion(versions, ReplyResult{AppVersionNum: 5, PlanClass: "cuda_fma"}, ""); !ok || av.AppName != "cuda_app" {
		t.Errorf("expected an exact (version, plan_class) match to win, got %+v (ok=%v)", av, ok)
	}
	if av, ok := matchAppVersion(versions, ReplyResult{AppVersionNum: 3, PlanClass: ""}, ""); !ok || av.AppName != "old_cpu_app" {
		t.Errorf("expected version_num=3 to match old_cpu_app, got %+v (ok=%v)", av, ok)
	}
	if _, ok := matchAppVersion(versions, ReplyResult{AppVersionNum: 99}, ""); ok {
		t.Error("a version_num nothing offers should not match")
	}
	if av, ok := matchAppVersion([]AppVersionXML{{AppName: "only_one", VersionNum: 1}}, ReplyResult{AppVersionNum: 42}, ""); !ok || av.AppName != "only_one" {
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
	if c := buildCoprocsXML(HostInfoSnapshot{}, 0); c != nil {
		t.Errorf("a GPU-less host must get a nil <coprocs>, got %+v", c)
	}
}

func TestBuildCoprocsXMLAdvertisesEachDetectedVendor(t *testing.T) {
	c := buildCoprocsXML(HostInfoSnapshot{NvidiaCount: 2, NvidiaName: "GeForce RTX 4080 SUPER", AtiCount: 1, AtiName: "Radeon RX 7900"}, 0)
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

// strictScheduler behaves like the real BOINC scheduler CGI that rejected
// every request Iris ever sent: it reads the request line by line, so a
// document squeezed onto one line (or missing the final newline) is answered
// with "no end tag", and a client that doesn't state its version in the
// core_client_*_version fields is told it is 0.0.0. Both replies were seen
// verbatim from Einstein@Home.
func strictScheduler(t *testing.T, reply string, gotBody *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Write([]byte(`<html></html>`)) // the master page
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if gotBody != nil {
			*gotBody = string(raw)
		}
		body := string(raw)
		msg := ""
		lines := strings.Split(body, "\n")
		hasEnd := false
		for i, l := range lines {
			if strings.TrimSpace(l) == "</scheduler_request>" && i < len(lines)-1 {
				hasEnd = true // the end tag must be on its own line, followed by a newline
			}
		}
		switch {
		case !hasEnd:
			msg = "Error in request message: no end tag "
		case !strings.Contains(body, "<core_client_major_version>8</core_client_major_version>"):
			msg = "Need version 5.8.0 or higher of the BOINC client. You have 0.0.0."
		}
		w.Header().Set("Content-Type", "text/xml")
		if msg != "" {
			w.Write([]byte(`<scheduler_reply><request_delay>60</request_delay><message priority="low">` + msg + `</message></scheduler_reply>`))
			return
		}
		w.Write([]byte(reply))
	}))
}

// TestDoRPCSendsARequestARealSchedulerAccepts is the regression test for the
// bug that meant no real project ever sent Iris any work: with the old
// single-line, no-trailing-newline request, this strict scheduler (and the
// real Einstein@Home) answers "Error in request message: no end tag".
func TestDoRPCSendsARequestARealSchedulerAccepts(t *testing.T) {
	var body string
	srv := strictScheduler(t, `<scheduler_reply><request_delay>60</request_delay></scheduler_reply>`, &body)
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Strict"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})

	for _, m := range fs.Messages() {
		if strings.Contains(m, "Error in request message") || strings.Contains(m, "Need version") {
			t.Fatalf("the scheduler rejected Iris's request: %q\nrequest was:\n%s", m, body)
		}
	}
	if lines := strings.Split(body, "\n"); lines[0] != "<scheduler_request>" || len(lines) < 12 {
		t.Errorf("request must have one element per line (first line %q, %d lines), got:\n%s", lines[0], len(lines), body)
	}
	if !strings.HasSuffix(body, "</scheduler_request>\n") {
		t.Errorf("request must end with a newline after the closing tag, got tail %q", body[max(0, len(body)-40):])
	}
}

func TestDoRPCHonorsTheServersRequestDelay(t *testing.T) {
	srv := strictScheduler(t, `<scheduler_reply><request_delay>3600</request_delay></scheduler_reply>`, nil)
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Slow"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	ps := &ProjectState{URL: srv.URL}
	e.doRPC(ps)

	if ps.MinRPCInterval != time.Hour {
		t.Errorf("MinRPCInterval = %v, want 1h from the server's <request_delay>", ps.MinRPCInterval)
	}
}

func TestDoRPCLearnsTheProjectNameFromTheReply(t *testing.T) {
	srv := strictScheduler(t, `<scheduler_reply><project_name>Einstein@Home</project_name></scheduler_reply>`, nil)
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: ""}) // attached with only a URL
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})

	if got := fs.names[srv.URL]; got != "Einstein@Home" {
		t.Errorf("project name = %q, want the one the scheduler reported", got)
	}
}

func TestDoRPCKeepsANameThePersonChose(t *testing.T) {
	srv := strictScheduler(t, `<scheduler_reply><project_name>Einstein@Home</project_name></scheduler_reply>`, nil)
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "My own name"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL, Name: "My own name"})

	if _, set := fs.names[srv.URL]; set {
		t.Error("a name the person chose must not be overwritten by the scheduler's")
	}
}

func TestReplyMessagePriorityMarksErrorsEvenWhenTheServerCallsThemLow(t *testing.T) {
	cases := map[string]int{
		"Error in request message: no end tag ":             3,
		"Invalid or missing account key.  To fix, detach":   3,
		"Need version 5.8.0 or higher of the BOINC client.": 3,
		"No work sent":                   1,
		"Project has no tasks available": 1,
	}
	for text, want := range cases {
		if got := replyMessagePriority(ReplyMessage{Priority: "low", Text: text}); got != want {
			t.Errorf("priority(%q) = %d, want %d", text, got, want)
		}
	}
	if got := replyMessagePriority(ReplyMessage{Priority: "high", Text: "anything"}); got != 3 {
		t.Errorf("a high-priority message must rank 3, got %d", got)
	}
}

// TestRequestUsesTheTagsARealSchedulerReads pins the request vocabulary to
// what a real scheduler parses: platform_name (not "platform"), no bare
// top-level resource_share (newer servers fail the whole request on it),
// host_cpid inside host_info, and coprocs beside host_info rather than inside.
func TestRequestUsesTheTagsARealSchedulerReads(t *testing.T) {
	var body string
	srv := strictScheduler(t, `<scheduler_reply></scheduler_reply>`, &body)
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Tags"})
	fs.hostInfo = HostInfoSnapshot{Ncpus: 4, NvidiaCount: 1, NvidiaName: "GeForce RTX 5070 Ti"}
	e := NewEngine(fs, fakeCache{}, EngineConfig{HostCPID: "abc123"})
	e.doRPC(&ProjectState{URL: srv.URL})

	for _, want := range []string{"<platform_name>", "<hostid>0</hostid>", "<rpc_seqno>0</rpc_seqno>", "<resource_share_fraction>1</resource_share_fraction>", "<host_cpid>abc123</host_cpid>", "<coproc_cuda>"} {
		if !strings.Contains(body, want) {
			t.Errorf("request is missing %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<resource_share>") || strings.Contains(body, "<platform>") {
		t.Errorf("request must not send the tags no scheduler reads (<resource_share>, <platform>):\n%s", body)
	}
	hostEnd := strings.Index(body, "</host_info>")
	if c := strings.Index(body, "<coprocs>"); c < hostEnd {
		t.Errorf("<coprocs> must come after </host_info>, not inside it:\n%s", body)
	}
}

// TestHostIDIsStoredAndSentBack guards against registering a brand-new host
// on the project's server at every single contact: the id the server assigns
// in its first reply has to be remembered and included in the next request.
func TestHostIDIsStoredAndSentBack(t *testing.T) {
	var body string
	srv := strictScheduler(t, `<scheduler_reply><hostid>4242</hostid></scheduler_reply>`, &body)
	defer srv.Close()

	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Host"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	ps := &ProjectState{URL: srv.URL}

	e.doRPC(ps) // first contact: hostid 0
	if !strings.Contains(body, "<hostid>0</hostid>") {
		t.Errorf("first contact must send hostid 0, got:\n%s", body)
	}
	if got := fs.rpc[srv.URL]; got != [2]int{4242, 1} {
		t.Fatalf("stored (hostid, rpc_seqno) = %v, want [4242 1]", got)
	}

	e.doRPC(ps) // later contact: must carry the assigned id and a higher sequence number
	if !strings.Contains(body, "<hostid>4242</hostid>") || !strings.Contains(body, "<rpc_seqno>1</rpc_seqno>") {
		t.Errorf("second contact must send the stored hostid and rpc_seqno=1, got:\n%s", body)
	}
}

func TestResourceShareFractionSplitsByShareWithTheBoincDefault(t *testing.T) {
	ps := []ProjectInfo{{URL: "a", ResourceShare: 300}, {URL: "b"}, {URL: "c", ResourceShare: 100}}
	if got := resourceShareFraction(ps, "a"); got != 300.0/500.0 {
		t.Errorf("a = %v, want 0.6", got)
	}
	if got := resourceShareFraction(ps, "b"); got != 100.0/500.0 { // unset share counts as 100
		t.Errorf("b = %v, want 0.2", got)
	}
	if got := resourceShareFraction(nil, "x"); got != 1 {
		t.Errorf("no projects should give 1, got %v", got)
	}
}

// TestWorkunitInputsAndResultOutputsAreSeparated checks a real-format reply:
// the workunit names the inputs and command line, the result only the output
// (a generated_locally file with its upload address and certificate).
func TestWorkunitInputsAndResultOutputsAreSeparated(t *testing.T) {
	const reply = `<scheduler_reply>
<app_version><app_name>a</app_name><version_num>7</version_num><platform>p</platform>
<file_ref><file_name>exe</file_name><open_name>run</open_name><main_program/></file_ref></app_version>
<workunit><name>w</name><app_name>a</app_name><command_line>--x</command_line>
<file_ref><file_name>in0</file_name><open_name>input</open_name></file_ref></workunit>
<result><name>r</name><wu_name>w</wu_name><report_deadline>123</report_deadline><version_num>7</version_num>
<file_ref><file_name>out0</file_name><open_name>result</open_name></file_ref></result>
<file_info><name>exe</name><url>http://x/exe</url><md5_cksum>m1</md5_cksum><nbytes>3</nbytes></file_info>
<file_info><name>in0</name><url>http://x/in0</url><md5_cksum>m2</md5_cksum><nbytes>4</nbytes></file_info>
<file_info><name>out0</name><generated_locally/><upload_when_present/><max_nbytes>500</max_nbytes><url>http://x/up</url><xml_signature>
SIGNED
</xml_signature></file_info>
</scheduler_reply>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, reply) }))
	defer srv.Close()
	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "W"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})
	added := fs.Added()
	if len(added) != 1 {
		t.Fatalf("expected one task, got %d (messages %v)", len(added), fs.Messages())
	}
	r := added[0]
	if r.CmdLine != "--x" || r.Deadline != 123 || r.VersionNum != 7 || r.AppName != "a" {
		t.Errorf("task fields wrong: %+v", r)
	}
	var in, exe *FileInfo
	for i := range r.Files {
		switch r.Files[i].Name {
		case "in0":
			in = &r.Files[i]
		case "exe":
			exe = &r.Files[i]
		}
	}
	if in == nil || in.OpenName != "input" || in.MD5 != "m2" || in.URL != "http://x/in0" {
		t.Errorf("input file wrong: %+v", in)
	}
	if exe == nil || !exe.MainProgram || exe.OpenName != "run" || exe.MD5 != "m1" {
		t.Errorf("app file wrong: %+v", exe)
	}
	if len(r.Outputs) != 1 || r.Outputs[0].Name != "out0" || r.Outputs[0].OpenName != "result" || r.Outputs[0].MaxNBytes != 500 ||
		len(r.Outputs[0].URLs) != 1 || strings.TrimSpace(r.Outputs[0].Signature) != "SIGNED" {
		t.Errorf("output wrong: %+v", r.Outputs)
	}
	if len(r.Files) != 2 {
		t.Errorf("the output must not be treated as a download: %+v", r.Files)
	}
}

func TestCoprocsCarryMemoryAndNoDoubledVendor(t *testing.T) {
	c := buildCoprocsXML(HostInfoSnapshot{NvidiaCount: 1, NvidiaName: "NVIDIA GeForce RTX 5070 Ti", NvidiaMem: 17094934528, NvidiaCCMajor: 12, CudaVersion: 13000, NvidiaDriver: "616.92"}, 0)
	if c.CUDA.Name != "GeForce RTX 5070 Ti" || c.CUDA.TotalGlobalMem != 17094934528 || c.CUDA.Major != 12 || c.CUDA.CudaVersion != 13000 || c.CUDA.DrvVersion != 61692 || c.CUDA.ReqInstances != 1 {
		t.Errorf("cuda entry wrong: %+v", c.CUDA)
	}
	a := buildCoprocsXML(HostInfoSnapshot{AtiCount: 1, AtiName: "AMD Radeon RX 7900", AtiMem: 8 << 30}, 1)
	if a.ATI.Name != "Radeon RX 7900" || a.ATI.LocalRAM != 8192 || a.ATI.ReqSecs != 0 {
		t.Errorf("ati entry wrong: %+v", a.ATI)
	}
}

func TestRequestNamesTheBrand(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, "<scheduler_reply></scheduler_reply>")
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	c.SetSchedulerURL(srv.URL)
	if _, err := c.SendRequest(&Request{Authenticator: "x", Platform: "p", VersionNum: 802}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "\n <client_brand>"+product.Name+" "+product.Version+"</client_brand>\n") {
		t.Errorf("request should name the client brand on its own line:\n%s", body)
	}
}

func TestReportedOSAndHostName(t *testing.T) {
	if got := reportedOSName("Microsoft Windows 11 Pro"); got != "Iris client - athena.org.tr | Microsoft Windows 11 Pro" {
		t.Errorf("got %q", got)
	}
	if got := reportedOSName(""); got != "Iris client - athena.org.tr" {
		t.Errorf("got %q", got)
	}
	e := NewEngine(newFakeState(), fakeCache{}, EngineConfig{HostNameFn: func() string { return "  Alp's PC " }})
	if got := e.hostName(); got != "Alp's PC" {
		t.Errorf("a name chosen in Settings must be used, got %q", got)
	}
	e = NewEngine(newFakeState(), fakeCache{}, EngineConfig{HostNameFn: func() string { return "" }})
	if e.hostName() == "" {
		t.Error("an empty setting must fall back to the machine's own name")
	}
}

func TestRequestCarriesTheProductName(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, "<scheduler_reply></scheduler_reply>")
	}))
	defer srv.Close()
	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "P"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})
	if !strings.Contains(body, "\n  <product_name>athena.org.tr</product_name>\n") {
		t.Errorf("host_info should carry product_name on its own line:\n%s", body)
	}
}

func TestOpenCLDescriptionIsSentAndHaveOpenCLFollowsIt(t *testing.T) {
	cl := &detect.OpenCLDevice{Name: "NVIDIA GeForce RTX 5070 Ti", Vendor: "NVIDIA Corporation", Available: true, GlobalMem: 17066033152,
		MaxComputeUnits: 70, MaxClockMHz: 2542, NvCCMajor: 12, DeviceVersion: "OpenCL 3.0 CUDA", PlatformVersion: "OpenCL 3.0 CUDA 13.4.89", DriverVersion: "616.92"}
	c := buildCoprocsXML(HostInfoSnapshot{NvidiaCount: 1, NvidiaName: "GeForce X", NvidiaCL: cl}, 0)
	if c.CUDA.HaveOpenCL != 1 || c.CUDA.OpenCL == nil || c.CUDA.OpenCL.DeviceVersion != "OpenCL 3.0 CUDA" {
		t.Fatalf("the OpenCL block must be sent: %+v", c.CUDA)
	}
	if c.CUDA.Major != 12 || c.CUDA.MultiProcessorCount != 70 || c.CUDA.ClockRate != 2542000 || c.CUDA.TotalGlobalMem != 17066033152 {
		t.Errorf("gaps must be filled from OpenCL: %+v", c.CUDA)
	}
	out, _ := xml.MarshalIndent(c, "", " ")
	for _, want := range []string{"<coproc_opencl>", "<opencl_device_version>OpenCL 3.0 CUDA</opencl_device_version>", "<max_compute_units>70</max_compute_units>", "<nv_compute_capability_major>12</nv_compute_capability_major>"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("request lacks %s:\n%s", want, out)
		}
	}
	// Without a driver report Iris must not claim OpenCL.
	none := buildCoprocsXML(HostInfoSnapshot{NvidiaCount: 1, NvidiaName: "GeForce X"}, 0)
	if none.CUDA.HaveOpenCL != 0 || none.CUDA.OpenCL != nil {
		t.Errorf("no OpenCL report means no OpenCL claim: %+v", none.CUDA)
	}
}
