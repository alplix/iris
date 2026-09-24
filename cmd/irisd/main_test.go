package main

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alplix/iris/internal/app"
	"github.com/alplix/iris/internal/boinc"
	"github.com/alplix/iris/internal/cache"
	"github.com/alplix/iris/internal/config"
	"github.com/alplix/iris/internal/detect"
	"github.com/alplix/iris/internal/guirpc"
	"github.com/alplix/iris/internal/prefs"
	"github.com/alplix/iris/internal/product"
	"github.com/alplix/iris/internal/state"
	"github.com/alplix/iris/internal/worker"
)

const testPassword = "s3cret"

// rig is a real irisd handler served over GUI RPC, so the tests exercise the
// same wire format the manager parses.
type rig struct {
	st     *state.State
	h      *clientHandler
	srv    *guirpc.Server
	host   string
	port   int
	worker *stateWorkerAdapter
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	st := state.New(dir)
	cm := cache.New(dir, cache.Config{Enabled: true, CacheSizeMB: 64, CPUCacheSizeMB: 64, SeparateSlots: true, SeparateProjects: true})
	if err := cm.Init(); err != nil {
		t.Fatal(err)
	}
	h := &clientHandler{state: st, cache: cm, dataDir: dir, cfg: config.Default(), prefs: prefs.Open(dir), xfers: newTransferTracker(st)}
	srv := guirpc.NewServer(h, testPassword)
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	host, portStr, _ := net.SplitHostPort(srv.Addr())
	port, _ := strconv.Atoi(portStr)
	return &rig{st: st, h: h, srv: srv, host: host, port: port, worker: &stateWorkerAdapter{st}}
}

func (r *rig) client(t *testing.T) *boinc.Client {
	t.Helper()
	c := boinc.NewClient(r.host, r.port, testPassword)
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestRPCRequiresPassword(t *testing.T) {
	r := newRig(t)

	bad := boinc.NewClient(r.host, r.port, "wrong")
	if err := bad.Connect(); err == nil {
		t.Fatal("a wrong password must be rejected")
	}

	// A raw peer that skips the handshake gets nothing.
	for _, cmd := range []string{"<get_state/>", "<quit/>", "<run_benchmarks/>"} {
		conn, err := net.DialTimeout("tcp", r.srv.Addr(), 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		conn.Write([]byte(cmd + "\n"))
		reply, err := bufio.NewReader(conn).ReadString(guirpc.ETX)
		conn.Close()
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		if !strings.Contains(reply, "unauthorized") {
			t.Fatalf("%s answered without authentication: %q", cmd, reply)
		}
	}
}

func TestRPCReportsVersionAndModes(t *testing.T) {
	r := newRig(t)
	c := r.client(t)
	if want := strings.TrimPrefix(product.UserAgent(), product.Name+"/"); c.Version != want {
		t.Errorf("server version = %q, want %q (product.Version)", c.Version, want)
	}
	if err := c.SetRunMode("never"); err != nil {
		t.Fatal(err)
	}
	cc, err := c.GetCcStatus()
	if err != nil {
		t.Fatal(err)
	}
	if cc.TaskMode.I() != 3 || cc.NetworkMode.I() != 1 {
		t.Errorf("cc_status = %+v, want task_mode 3 / network_mode 1", cc)
	}
}

func TestRPCMessagesTransfersAndHistory(t *testing.T) {
	r := newRig(t)
	r.st.AddMessage("hello", "", 1)
	r.st.AddProject(state.Project{Name: "P", MasterURL: "https://p.example"})
	r.st.UpdateProjectCredit("https://p.example", 1000, 50, 400, 20)
	r.st.RecordTaskDay("https://p.example", true, 120)
	r.st.RecordTaskDay("https://p.example", true, 80)
	r.st.RecordTaskDay("https://p.example", false, 30)
	r.st.AddXfer(false, 5000)
	r.st.AddXfer(true, 700)
	r.st.BeginTransfer(state.Xfer{Name: "wu_1", ProjectURL: "https://p.example", Nbytes: 100, BytesXferred: 40})
	c := r.client(t)

	msgs, err := c.GetMessages(-1)
	if err != nil || len(msgs) != 1 || msgs[0].Body != "hello" {
		t.Fatalf("messages = %v, %v", msgs, err)
	}
	xfers, err := c.GetTransfers()
	if err != nil || len(xfers) != 1 || xfers[0].Name != "wu_1" || xfers[0].BytesXferred.I() != 40 {
		t.Fatalf("transfers = %+v, %v", xfers, err)
	}
	stats, err := c.GetStats()
	if err != nil || len(stats) != 1 || stats[0].MasterURL != "https://p.example" || len(stats[0].Daily) != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	if d := stats[0].Daily[0]; !d.Cumulative() || d.HostTotalCredit.I() != 400 || d.UserTotalCredit.I() != 1000 {
		t.Errorf("daily stat = %+v", d)
	}
	// Credit (scheduler) and task counts (worker) land in the same day bucket
	// even though they are recorded independently.
	if d := stats[0].Daily[0]; d.TasksSuccess.I() != 2 || d.TasksError.I() != 1 {
		t.Errorf("daily stat task counts = %+v, want success=2 error=1", d)
	}
	dx, err := c.GetDailyXferHistory()
	if err != nil || len(dx) != 1 || dx[0].Down.I() != 5000 || dx[0].Up.I() != 700 {
		t.Fatalf("xfer history = %+v, %v", dx, err)
	}
	if dx[0].When.I64() < 1e9 {
		t.Errorf("xfer time %d should be unix seconds", dx[0].When.I64())
	}
}

// GetStats merges credit history and task-completion counts by (project, day)
// even when only one of the two was ever recorded for a given project.
func TestRPCStatsTaskCountsWithoutCredit(t *testing.T) {
	r := newRig(t)
	r.st.AddProject(state.Project{Name: "Q", MasterURL: "https://q.example"})
	r.st.RecordTaskDay("https://q.example", true, 60)
	c := r.client(t)

	stats, err := c.GetStats()
	if err != nil || len(stats) != 1 || stats[0].MasterURL != "https://q.example" {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	d := stats[0].Daily[0]
	if d.TasksSuccess.I() != 1 || d.HostTotalCredit.I() != 0 {
		t.Errorf("daily stat = %+v, want tasks_success=1 and no credit fields", d)
	}
}

func TestRPCPrefsOverridePersists(t *testing.T) {
	r := newRig(t)
	c := r.client(t)
	if err := c.SetPrefsOverride([][2]string{{"max_ncpus_pct", "50"}, {"work_buf_min_days", "0.5"}}); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetPrefsOverride()
	if err != nil || got["max_ncpus_pct"] != "50" || got["work_buf_min_days"] != "0.5" {
		t.Fatalf("prefs = %v, %v", got, err)
	}
	if err := c.SetPrefsOverride([][2]string{{"not a key", "1"}}); err == nil {
		t.Error("an invalid preference name must be rejected")
	}
	if err := c.SetPrefsOverride(nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.GetPrefsOverride(); len(got) != 0 {
		t.Errorf("prefs after reset = %v", got)
	}
}

func TestRPCHostInfoListsGPUs(t *testing.T) {
	r := newRig(t)
	r.st.PlatformName = "riscv64-unknown-linux-gnu"
	r.st.HostInfo, r.st.OpenCLGpuProps = buildHostInfo(detect.Specs{
		GPUs: []detect.GPU{{Name: "NVIDIA GeForce RTX 4090", Vendor: "NVIDIA", DedicatedMB: 24576}},
	})
	c := r.client(t)
	cs, err := c.GetState()
	if err != nil {
		t.Fatal(err)
	}
	snap := app.Normalize("h", false, cs, nil, &boinc.CcStatus{}, nil, c.Version)
	if len(snap.HostInfo.GPUs) != 1 || snap.HostInfo.GPUs[0].Vendor != "NVIDIA" || snap.HostInfo.GPUs[0].Names[0] != "NVIDIA GeForce RTX 4090" {
		t.Fatalf("gpus = %+v", snap.HostInfo.GPUs)
	}
	if snap.HostInfo.Platform != "riscv64-unknown-linux-gnu" {
		t.Errorf("platform = %q", snap.HostInfo.Platform)
	}
	if snap.HostInfo.GPUs[0].VRAM != 24576*1048576 {
		t.Errorf("vram = %d", snap.HostInfo.GPUs[0].VRAM)
	}
}

// TestStateAdapterGetHostInfoCarriesGPUsToTheScheduler guards the wiring
// between hardware detection and the scheduler's GPU work request: a
// detected NVIDIA GPU must reach scheduler.HostInfoSnapshot, not just the
// manager-facing snapshot TestRPCHostInfoListsGPUs already covers.
func TestStateAdapterGetHostInfoCarriesGPUsToTheScheduler(t *testing.T) {
	r := newRig(t)
	r.st.HostInfo, _ = buildHostInfo(detect.Specs{
		GPUs: []detect.GPU{{Name: "NVIDIA GeForce RTX 4090", Vendor: "NVIDIA", DedicatedMB: 24576}},
	})
	hi := (&stateAdapter{r.st}).GetHostInfo()
	if hi.NvidiaCount != 1 || hi.NvidiaName != "NVIDIA GeForce RTX 4090" {
		t.Errorf("scheduler-facing host info = %+v, want NvidiaCount=1 NvidiaName=%q", hi, "NVIDIA GeForce RTX 4090")
	}
	if hi.AtiCount != 0 {
		t.Errorf("AtiCount = %d, want 0 for a host with no AMD GPU", hi.AtiCount)
	}
}

func TestTaskStatusAsSeenByManager(t *testing.T) {
	r := newRig(t)
	r.st.AddProject(state.Project{Name: "P", MasterURL: "https://p.example"})
	for _, n := range []string{"a", "b", "c", "d"} {
		r.st.AddResult(state.Result{Name: n, ProjectURL: "https://p.example"})
	}
	r.worker.UpdateResult("a", worker.StateCompute, 0.25, 100, 0)
	r.worker.UpdateResult("b", worker.StateError, 0, 3, 0) // the worker reports no exit code
	r.worker.UpdateResult("c", worker.StateReady, 0, 50, 0)
	r.worker.UpdateResult("d", worker.StateDownload, 0, 0, 0)

	c := r.client(t)
	cs, err := c.GetState()
	if err != nil {
		t.Fatal(err)
	}
	snap := app.Normalize("h", false, cs, nil, &boinc.CcStatus{}, nil, "")
	want := map[string]app.TaskStatus{
		"a": app.StatusRunning,
		"b": app.StatusError,
		"c": app.StatusReady,
		"d": app.StatusDownloading,
	}
	for _, tk := range snap.Tasks {
		if tk.Status != want[tk.Name] {
			t.Errorf("task %s shows as %q, want %q", tk.Name, tk.Status, want[tk.Name])
		}
		if tk.Name == "a" && (tk.Elapsed != 100 || tk.ETA < 299 || tk.ETA > 301) {
			t.Errorf("running task elapsed=%v eta=%v, want 100 / 300", tk.Elapsed, tk.ETA)
		}
	}
}

func TestTransferAbortAndRetry(t *testing.T) {
	r := newRig(t)
	r.st.AddResult(state.Result{Name: "task1", State: worker.StateError, ExitStatus: 1, ReadyToReport: 1,
		Files: []state.FileInfo{{Name: "input.dat"}}})
	r.st.BeginTransfer(state.Xfer{Name: "input.dat"})
	r.st.EndTransfer("input.dat", false) // failed: kept and paused
	c := r.client(t)

	if err := c.FileTransferOp("input.dat", "retry"); err != nil {
		t.Fatal(err)
	}
	if xs, _ := c.GetTransfers(); len(xs) != 0 {
		t.Errorf("retried transfer should be gone, still have %+v", xs)
	}
	res := r.st.Snapshot().Results[0]
	if res.State != worker.StateNew || res.ExitStatus != 0 || res.ReadyToReport != 0 {
		t.Errorf("task not requeued: %+v", res)
	}

	// Errors the client reports must reach the manager as Go errors.
	if err := c.FileTransferOp("nope", "abort"); err == nil || !strings.Contains(err.Error(), "no transfer named") {
		t.Errorf("RPC error not surfaced, got %v", err)
	}

	// A transfer that did not fail cannot be retried, and aborting an
	// unknown one is an error too.
	r.st.BeginTransfer(state.Xfer{Name: "live.dat"})
	if err := r.h.FileTransferOp("live.dat", "retry"); err == nil {
		t.Error("retry of a healthy transfer should fail")
	}
	if err := r.h.FileTransferOp("nope", "abort"); err == nil {
		t.Error("abort of an unknown transfer should fail")
	}
}

func TestBenchmarkUpdatesHostSpeed(t *testing.T) {
	r := newRig(t)
	r.st.HostInfo.PFlops = 1
	if err := r.h.RunBenchmarks(); err != nil {
		t.Fatal(err)
	}
	if err := r.h.RunBenchmarks(); err == nil {
		t.Error("a second concurrent benchmark should be refused")
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if r.st.Snapshot().HostInfo.PFlops > 1e6 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("benchmark never published a result")
}

// The "no new tasks" / "allow new tasks" buttons used to be accepted and then
// ignored: the handler had no case for them.
func TestProjectOpNoMoreWorkTogglesTheFlag(t *testing.T) {
	r := newRig(t)
	r.st.AddProject(state.Project{Name: "P", MasterURL: "https://p.example/"})
	if err := r.h.ProjectOp("https://p.example/", "nomorework"); err != nil {
		t.Fatal(err)
	}
	if r.st.GetProjectByURL("https://p.example/").DontRequestMoreWork != 1 {
		t.Error("nomorework must set the flag")
	}
	if err := r.h.ProjectOp("https://p.example/", "allowmorework"); err != nil {
		t.Fatal(err)
	}
	if r.st.GetProjectByURL("https://p.example/").DontRequestMoreWork != 0 {
		t.Error("allowmorework must clear the flag")
	}
	if err := r.h.ProjectOp("https://p.example/", "bogus"); err == nil {
		t.Error("an unknown operation must be an error, not a silent success")
	}
}

func TestIrisEnergyReplyCarriesTheHistoryAndFigures(t *testing.T) {
	r := newRig(t)
	r.st.AddEnergy(time.Now(), 500, 250, false)
	b, err := r.h.GetIrisEnergy()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"<iris_energy>", "<cpu_watts>65</cpu_watts>", "<grid_g_per_kwh>475</grid_g_per_kwh>", "<cpu_wh>500</cpu_wh>", "<gpu_est_wh>250</gpu_est_wh>"} {
		if !strings.Contains(s, want) {
			t.Errorf("reply lacks %s:\n%s", want, s)
		}
	}
}
