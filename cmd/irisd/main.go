package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alplix/iris/internal/boinc"
	"github.com/alplix/iris/internal/cache"
	"github.com/alplix/iris/internal/config"
	"github.com/alplix/iris/internal/detect"
	"github.com/alplix/iris/internal/guirpc"
	"github.com/alplix/iris/internal/local"
	"github.com/alplix/iris/internal/prefs"
	"github.com/alplix/iris/internal/product"
	"github.com/alplix/iris/internal/project"
	"github.com/alplix/iris/internal/scheduler"
	"github.com/alplix/iris/internal/state"
	"github.com/alplix/iris/internal/worker"
)

// detachedEnv marks the background copy of the client started by --daemon.
const detachedEnv = "IRIS_DETACHED"

func guiRPCPort() int {
	port, note := product.GUIRPCPort()
	if note != "" {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", note)
	}
	return port
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Println(banner())
			return
		case "--help", "-h":
			printUsage()
			return
		case "--status":
			showStatus()
			return
		case "--stop":
			stopDaemon()
			return
		case "--daemon", "-d":
			if os.Getenv(detachedEnv) == "" {
				detach()
				return
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			printUsage()
			os.Exit(1)
		}
	}
	runDaemon()
}

// detach starts the client in the background and returns immediately.
func detach() {
	exe, err := os.Executable()
	if err != nil {
		fatal("cannot locate executable: %v", err)
	}
	cmd, err := local.StartDetached(exe)
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("Iris client started in the background (pid %d).\n", cmd.Process.Pid)
	fmt.Printf("Log: %s\n", filepath.Join(config.DataDir(), "irisd.log"))
	cmd.Process.Release()
}

// redirectOutput sends the background client's console output to irisd.log.
func redirectOutput(dataDir string) {
	f, err := os.OpenFile(filepath.Join(dataDir, "irisd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	os.Stdout, os.Stderr = f, f
	log.SetOutput(f)
}

func banner() string {
	return `Iris Grid Compute Client v` + product.Version + `
Coded By Alperen Yavuz`
}

func printUsage() {
	fmt.Println(banner())
	fmt.Println(`
Usage:
  iris[ d]                Run daemon (foreground)
  iris[ d] --status       Show client status
  iris[ d] --stop         Stop running client
  iris[ d] --version      Show version
  iris[ d] --help         Show this help

Lightweight volunteer computing client with
separate GPU/CPU cache management.`)
}

func runDaemon() {
	dataDir := config.DataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fatal("cannot create data directory: %v", err)
	}
	if os.Getenv(detachedEnv) != "" {
		redirectOutput(dataDir)
	}

	pidFile := filepath.Join(dataDir, "iris.pid")
	os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(pidFile)

	cfgPath := filepath.Join(dataDir, "cc_config.xml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fatal("cannot load config: %v", err)
	}
	cfg.Options.UserAgent = product.UserAgent()
	config.Save(cfg, cfgPath)

	cacheMgr := cache.New(dataDir, cache.Config{
		Enabled:          cfg.GPUCache.Enabled,
		CacheSizeMB:      cfg.GPUCache.CacheSizeMB,
		CPUCacheSizeMB:   cfg.GPUCache.CPUCacheSizeMB,
		SeparateSlots:    cfg.GPUCache.SeparateSlots,
		SeparateProjects: cfg.GPUCache.SeparateProjects,
	})
	if err := cacheMgr.Init(); err != nil {
		fatal("cannot initialize cache: %v", err)
	}

	st := state.New(dataDir)
	if err := st.Load(); err != nil {
		fmt.Printf("Warning: could not load state: %v\n", err)
	}

	st.HostInfo, st.OpenCLGpuProps = detectHostInfo()
	st.HostInfo.HostCPID = loadOrCreateHostCPID(dataDir)
	st.PlatformName = scheduler.Platform()

	fmt.Println(banner())
	fmt.Println()
	fmt.Printf("Data directory: %s\n", dataDir)
	fmt.Printf("CPU: %s %s (%d cores)\n", st.HostInfo.PVendor, st.HostInfo.PModel, int(st.HostInfo.PNcpus))
	if st.HostInfo.MNbytes > 0 {
		fmt.Printf("RAM: %.1f GB\n", st.HostInfo.MNbytes/1073741824)
	}
	if st.HostInfo.DTotal > 0 {
		fmt.Printf("Disk: %.1f GB free of %.1f GB\n", st.HostInfo.DFree/1073741824, st.HostInfo.DTotal/1073741824)
	}
	for i, gpu := range st.HostInfo.GPUs {
		fmt.Printf("GPU %d: %s\n", i, gpu)
	}
	fmt.Printf("GPU Cache: separate slots=%v, separate projects=%v\n",
		cfg.GPUCache.SeparateSlots, cfg.GPUCache.SeparateProjects)
	fmt.Printf("Projects: %d | Tasks: %d\n", len(st.Projects), len(st.Results))
	os.Stdout.Sync()

	guiRPCAddr := fmt.Sprintf("0.0.0.0:%d", guiRPCPort())
	if !cfg.Options.AllowRemoteGuiRPC {
		guiRPCAddr = fmt.Sprintf("127.0.0.1:%d", guiRPCPort())
	}

	password := loadOrCreatePassword(dataDir)
	overrides := prefs.Open(dataDir)
	handler := &clientHandler{state: st, cache: cacheMgr, dataDir: dataDir, cfg: cfg, prefs: overrides}
	xfers := newTransferTracker(st)
	handler.xfers = xfers
	srv := guirpc.NewServer(handler, password)
	if err := srv.Start(guiRPCAddr); err != nil {
		fatal("GUI RPC server failed: %v\n(port %d is in use by another program. Iris and BOINC use different ports and do not conflict; this is most likely a second Iris client. Set IRIS_GUI_RPC_PORT to use another port.)", err, guiRPCPort())
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	srv.SetOnQuit(func() { sig <- syscall.SIGTERM })

	fmt.Printf("GUI RPC listening on %s\n", guiRPCAddr)
	fmt.Println("Ready.")
	os.Stdout.Sync()

	schedEngine := scheduler.NewEngine(&stateAdapter{st}, &cacheAdapter{cacheMgr}, scheduler.EngineConfig{
		SchedulerInterval: 60 * time.Second,
		MinRPCInterval:    30 * time.Second,
		UserAgent:         product.UserAgent(),
		DataDir:           dataDir,
		HostCPID:          st.HostInfo.HostCPID,
	})
	handler.sched = schedEngine
	schedEngine.Start()

	dl := &downloaderAdapter{dataDir: dataDir, xfers: xfers}
	workerEngine := worker.NewEngine(&stateWorkerAdapter{st}, &cacheAdapter{cacheMgr}, &projectAdapter{st}, dl, dl, worker.Config{
		MaxConcurrent:     runtime.NumCPU(),
		MaxConcurrentFn:   func() int { return overrides.MaxCPUs(runtime.NumCPU()) },
		DataDir:           dataDir,
		UserAgent:         product.UserAgent(),
		RealAppsEnabledFn: func() bool { return overrides.Bool(prefs.RealAppsKey) },
	})
	workerEngine.Start()

	st.AddMessage("Client started", "", 1)

	<-sig

	fmt.Println("\nShutting down...")
	workerEngine.Stop()
	schedEngine.Stop()
	workerEngine.Cleanup()
	st.AddMessage("Client shutting down", "", 1)
	st.Save()
	srv.Stop()
	fmt.Println("Goodbye.")
}

func detectHostInfo() (state.HostInfo, []state.OpenCLProp) {
	return buildHostInfo(detect.Detect())
}

func buildHostInfo(specs detect.Specs) (state.HostInfo, []state.OpenCLProp) {
	hi := state.HostInfo{
		OSName:    specs.OSName,
		OSVersion: specs.OSVersion,
		PVendor:   specs.Vendor,
		PModel:    specs.Model,
		PNcpus:    float64(specs.Ncpus),
		PFlops:    specs.PFlops,
		MNbytes:   specs.MNbytes,
		DFree:     specs.DFree,
		DTotal:    specs.DTotal,
		CamVer:    product.Version,
	}
	var props []state.OpenCLProp
	for _, g := range specs.GPUs {
		hi.GPUs = append(hi.GPUs, g.Name)
		lower := strings.ToLower(g.Vendor + " " + g.Name)
		switch {
		case strings.Contains(lower, "nvidia"):
			hi.Coprocs.NvidiaDeviceNames = append(hi.Coprocs.NvidiaDeviceNames, g.Name)
			hi.Coprocs.NvidiaDevCount++
		case strings.Contains(lower, "amd") || strings.Contains(lower, "ati ") || strings.Contains(lower, "radeon") || strings.Contains(lower, "advanced micro"):
			hi.Coprocs.AtiDeviceNames = append(hi.Coprocs.AtiDeviceNames, g.Name)
			hi.Coprocs.AtiDevCount++
		case strings.Contains(lower, "intel"):
			hi.Coprocs.IntelGpuDeviceNames = append(hi.Coprocs.IntelGpuDeviceNames, g.Name)
			hi.Coprocs.IntelGpuDevCount++
		default:
			hi.Coprocs.OtherGpuDeviceNames = append(hi.Coprocs.OtherGpuDeviceNames, g.Name)
		}
		if g.DedicatedMB > 0 {
			props = append(props, state.OpenCLProp{Vendor: g.Vendor, Name: g.Name, GlobalMem: float64(g.DedicatedMB) * 1048576})
		}
	}
	hi.Coprocs.Count = float64(len(specs.GPUs))
	return hi, props
}

// loadOrCreatePassword returns the GUI RPC password, generating a random one
// on first run. The file is owner-readable only.
func loadOrCreatePassword(dataDir string) string {
	fp := filepath.Join(dataDir, "gui_rpc_auth.cfg")
	if data, err := os.ReadFile(fp); err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			os.Chmod(fp, 0o600)
			return p
		}
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		fatal("cannot generate RPC password: %v", err)
	}
	pass := hex.EncodeToString(b[:])
	if err := os.WriteFile(fp, []byte(pass+"\n"), 0o600); err != nil {
		fatal("cannot write %s: %v", fp, err)
	}
	return pass
}

func stopDaemon() {
	if !isDaemonRunning() {
		fmt.Println("Iris client is not running.")
		return
	}
	pass := local.ReadPassword(config.DataDir())
	c := boinc.NewClient("127.0.0.1", guiRPCPort(), pass)
	defer c.Close()
	if err := c.Connect(); err != nil {
		fmt.Printf("Cannot reach the client: %v\n", err)
		os.Exit(1)
	}
	if _, err := c.Call("<quit/>"); err != nil {
		// The client closes the connection while shutting down.
		fmt.Println("Stop signal sent.")
		return
	}
	fmt.Println("Stop signal sent.")
}

func showStatus() {
	dataDir := config.DataDir()
	fp := filepath.Join(dataDir, "client_state.xml")
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		if isDaemonRunning() {
			fmt.Println("Iris client is running.")
			return
		}
		fmt.Println("Iris client is not running.")
		return
	}
	st := state.New(dataDir)
	if err := st.Load(); err != nil {
		fmt.Println("Cannot read client state.")
		return
	}
	fmt.Printf("Iris v%s\n", strings.TrimPrefix(st.Version, "Iris/"))
	fmt.Printf("Projects: %d\n", len(st.Projects))
	fmt.Printf("Tasks: %d\n", len(st.Results))
	for _, p := range st.Projects {
		fmt.Printf("  - %s (%s)\n", p.Name, p.MasterURL)
	}
}

func isDaemonRunning() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", guiRPCPort()), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}

type clientHandler struct {
	state    *state.State
	cache    *cache.Manager
	dataDir  string
	cfg      *config.Config
	prefs    *prefs.Store
	xfers    *transferTracker
	sched    *scheduler.Engine
	benching atomic.Bool
}

func (h *clientHandler) GetState() ([]byte, error) {
	return h.state.MarshalState()
}

func (h *clientHandler) GetCcStatus() ([]byte, error) {
	s := h.state.Snapshot()
	return xml.MarshalIndent(struct {
		XMLName xml.Name `xml:"cc_status"`
		state.Status
	}{Status: s.Status}, "", "  ")
}

func (h *clientHandler) GetMessages(after int) ([]byte, error) {
	type msgXML struct {
		XMLName xml.Name    `xml:"msgs"`
		M       []state.Msg `xml:"msg"`
	}
	return xml.MarshalIndent(msgXML{M: h.state.GetMessages(after)}, "", "  ")
}

func (h *clientHandler) GetTransfers() ([]byte, error) {
	type xferXML struct {
		XMLName xml.Name     `xml:"file_transfers"`
		X       []state.Xfer `xml:"file_transfer"`
	}
	s := h.state.Snapshot()
	return xml.MarshalIndent(xferXML{X: s.Transfers}, "", "  ")
}

// GetStats reports each project's credit history in BOINC's
// project_statistics layout (one entry per day, cumulative totals).
func (h *clientHandler) GetStats() ([]byte, error) {
	type dailyXML struct {
		Day          int64   `xml:"day"`
		UserTotal    float64 `xml:"user_total_credit"`
		UserExpavg   float64 `xml:"user_expavg_credit"`
		HostTotal    float64 `xml:"host_total_credit"`
		HostExpavg   float64 `xml:"host_expavg_credit"`
		TasksSuccess int     `xml:"tasks_success"`
		TasksError   int     `xml:"tasks_error"`
	}
	type projXML struct {
		URL   string     `xml:"master_url"`
		Daily []dailyXML `xml:"daily_statistics"`
	}
	type statsXML struct {
		XMLName xml.Name  `xml:"statistics"`
		Project []projXML `xml:"project_statistics"`
	}
	s := h.state.Snapshot()

	// Credit (scheduler RPC) and task counts (worker) are recorded on
	// unrelated schedules, so they are merged here by (project, day) rather
	// than assumed to line up.
	type dayKey struct {
		url string
		day int64
	}
	byDay := map[dayKey]*dailyXML{}
	byURL := map[string][]dayKey{}
	seen := map[string]bool{}
	touch := func(url string, day int64) *dailyXML {
		k := dayKey{url, day}
		d, ok := byDay[k]
		if !ok {
			d = &dailyXML{Day: day * 86400}
			byDay[k] = d
			byURL[url] = append(byURL[url], k)
		}
		if !seen[url] {
			seen[url] = true
		}
		return d
	}
	var order []string
	for _, c := range s.Credits {
		if !seen[c.URL] {
			order = append(order, c.URL)
		}
		d := touch(c.URL, c.Day)
		d.UserTotal, d.UserExpavg = c.UserTotal, c.UserExpavg
		d.HostTotal, d.HostExpavg = c.HostTotal, c.HostExpavg
	}
	for _, t := range s.TaskDays {
		if !seen[t.URL] {
			order = append(order, t.URL)
		}
		d := touch(t.URL, t.Day)
		d.TasksSuccess, d.TasksError = t.Success, t.Error
	}
	var out statsXML
	for _, u := range order {
		keys := byURL[u]
		sort.Slice(keys, func(a, b int) bool { return keys[a].day < keys[b].day })
		p := projXML{URL: u}
		for _, k := range keys {
			p.Daily = append(p.Daily, *byDay[k])
		}
		out.Project = append(out.Project, p)
	}
	return xml.MarshalIndent(out, "", "  ")
}

func (h *clientHandler) GetDiskUsage() ([]byte, error) {
	di := h.cache.GetDiskUsage()
	s := h.state.Snapshot()
	type projXML struct {
		URL       string `xml:"master_url"`
		DiskUsage int64  `xml:"disk_usage"`
	}
	type diskXML struct {
		XMLName  xml.Name  `xml:"disk_usage"`
		DTotal   int64     `xml:"d_total"`
		DFree    int64     `xml:"d_free"`
		Projects []projXML `xml:"project"`
	}
	dTotal := int64(s.HostInfo.DTotal)
	dFree := int64(s.HostInfo.DFree)
	if dTotal == 0 {
		dTotal = di.Total
		dFree = di.Free
	}
	var totalUsed int64
	var projs []projXML
	for _, p := range di.Projects {
		projs = append(projs, projXML{URL: p.URL, DiskUsage: p.DiskUsage})
		totalUsed += p.DiskUsage
	}
	h.state.SetDiskUsage(totalUsed)
	return xml.MarshalIndent(diskXML{DTotal: dTotal, DFree: dFree, Projects: projs}, "", "  ")
}

func (h *clientHandler) GetDailyXferHistory() ([]byte, error) {
	type dxXML struct {
		When int64   `xml:"when"`
		Up   float64 `xml:"up"`
		Down float64 `xml:"down"`
	}
	type xfersXML struct {
		XMLName xml.Name `xml:"daily_xfers"`
		DX      []dxXML  `xml:"dx"`
	}
	var out xfersXML
	for _, d := range h.state.Snapshot().Xfers {
		out.DX = append(out.DX, dxXML{When: d.When*86400 + 43200, Up: d.Up, Down: d.Down})
	}
	sort.Slice(out.DX, func(a, b int) bool { return out.DX[a].When < out.DX[b].When })
	return xml.MarshalIndent(out, "", "  ")
}

func (h *clientHandler) GetPrefsOverride() ([]byte, error) {
	return h.prefs.XML(), nil
}

func (h *clientHandler) SetPrefsOverride(pairs [][2]string) error {
	if err := h.prefs.Set(pairs); err != nil {
		return err
	}
	if len(pairs) == 0 {
		h.state.AddMessage("Global preferences reset to defaults", "", 1)
	} else {
		h.state.AddMessage("Global preferences updated", "", 1)
	}
	return nil
}

func (h *clientHandler) SetRunMode(mode string) error {
	switch mode {
	case "always":
		h.state.SetTaskMode(1)
	case "auto":
		h.state.SetTaskMode(2)
	case "never":
		h.state.SetTaskMode(3)
	}
	h.state.AddMessage(fmt.Sprintf("Run mode: %s", mode), "", 1)
	return nil
}

func (h *clientHandler) SetNetworkMode(mode string) error {
	switch mode {
	case "always":
		h.state.SetNetworkMode(1)
	case "auto":
		h.state.SetNetworkMode(2)
	case "never":
		h.state.SetNetworkMode(3)
	}
	h.state.AddMessage(fmt.Sprintf("Network mode: %s", mode), "", 1)
	return nil
}

func (h *clientHandler) ResultOp(name, op string) error {
	switch op {
	case "abort":
		h.state.RemoveResult(name)
		h.state.AddMessage(fmt.Sprintf("Task aborted: %s", name), "", 1)
	case "suspend":
		h.state.SetSuspended(name, true)
	case "resume":
		h.state.SetSuspended(name, false)
	}
	return nil
}

func (h *clientHandler) ProjectOp(url, op string) error {
	switch op {
	case "detach":
		h.state.RemoveProject(url)
		h.state.AddMessage(fmt.Sprintf("Project detached: %s", url), url, 1)
	case "suspend":
		h.state.SetProjectSuspended(url, true)
	case "resume":
		h.state.SetProjectSuspended(url, false)
	case "update":
		h.state.SetProjectUpdate(url)
		if h.sched != nil {
			h.sched.RequestUpdate(url)
		}
	}
	return nil
}

func (h *clientHandler) FileTransferOp(name, op string) error {
	switch op {
	case "abort":
		if !h.xfers.Abort(name) {
			return fmt.Errorf("no transfer named %q", name)
		}
		h.state.AddMessage(fmt.Sprintf("Transfer aborted: %s", name), "", 1)
	case "retry":
		if err := h.xfers.Retry(name); err != nil {
			return err
		}
		h.state.AddMessage(fmt.Sprintf("Transfer queued for retry: %s", name), "", 1)
	default:
		return fmt.Errorf("unknown transfer operation %q", op)
	}
	return nil
}

func (h *clientHandler) ProjectAttach(url, auth, name string) error {
	pd := project.NewDir(h.dataDir, url)
	if err := pd.Init(); err != nil {
		return fmt.Errorf("init project dir: %w", err)
	}
	acct := &project.Account{
		ProjectURL:    url,
		UserName:      name,
		Authenticator: auth,
		Venue:         "default",
		Joined:        1,
	}
	if err := pd.SaveAccount(acct); err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	ccOpts := project.CCOptions{
		UserAgent:          product.UserAgent(),
		AllowRemoteGuiRPC:  true,
		MaxAppClients:      64,
		ReportResultsEarly: true,
		UseAllGPUs:         true,
	}
	pd.WriteConfig(project.GenCCConfig(ccOpts))

	h.state.AddProject(state.Project{
		Name:          name,
		MasterURL:     url,
		ProjectDir:    pd.Path(),
		Authenticator: auth,
		Venue:         "default",
	})
	h.state.AddMessage(fmt.Sprintf("Project attached: %s", name), url, 1)
	h.state.Save()
	return nil
}

// RunBenchmarks measures CPU throughput in the background and publishes the
// result as the host's p_fpops, which the schedulers use to size work.
func (h *clientHandler) RunBenchmarks() error {
	if !h.benching.CompareAndSwap(false, true) {
		return fmt.Errorf("benchmarks are already running")
	}
	h.state.AddMessage("Benchmarks started", "", 1)
	go func() {
		defer h.benching.Store(false)
		ncpu := runtime.NumCPU()
		flops := detect.Benchmark(ncpu, 3*time.Second)
		h.state.SetPFlops(flops)
		h.state.AddMessage(fmt.Sprintf("Benchmarks finished: %.1f GFLOPS across %d cores", flops/1e9, ncpu), "", 1)
		h.state.Save()
	}()
	return nil
}

func (h *clientHandler) GetHostInfo() ([]byte, error) {
	s := h.state.Snapshot()
	return xml.MarshalIndent(s.HostInfo, "", "  ")
}

type stateAdapter struct{ s *state.State }

func (a *stateAdapter) GetProjects() []scheduler.ProjectInfo {
	a.s.RLock()
	defer a.s.RUnlock()
	var out []scheduler.ProjectInfo
	for _, p := range a.s.Projects {
		out = append(out, scheduler.ProjectInfo{
			Name:                p.Name,
			URL:                 p.MasterURL,
			Authenticator:       p.Authenticator,
			TotalCredit:         p.UserTotalCredit,
			ResourceShare:       p.ResourceShare,
			HostID:              p.HostID,
			RPCSeqno:            p.RPCSeqno,
			SuspendedViaGUI:     p.SuspendedViaGUI,
			DontRequestMoreWork: p.DontRequestMoreWork,
		})
	}
	return out
}

func (a *stateAdapter) AddResult(r scheduler.ResultInfo) {
	var files []state.FileInfo
	for _, f := range r.Files {
		files = append(files, state.FileInfo{Name: f.Name, URL: f.URL, NBytes: f.NBytes, MD5: f.MD5, MainProgram: f.MainProgram})
	}
	resources := ""
	if r.GPU {
		resources = "gpu"
	}
	a.s.AddResult(state.Result{
		Name:          r.Name,
		WuName:        r.WuName,
		ProjectURL:    r.ProjectURL,
		State:         r.State,
		CmdLine:       r.CmdLine,
		AppVersionNum: r.AppVersionNum,
		AppName:       r.AppName,
		Resources:     resources,
		Files:         files,
	})
}

func (a *stateAdapter) GetResults() []scheduler.ResultInfo {
	a.s.RLock()
	defer a.s.RUnlock()
	var out []scheduler.ResultInfo
	for _, r := range a.s.Results {
		out = append(out, scheduler.ResultInfo{
			Name: r.Name, WuName: r.WuName, ProjectURL: r.ProjectURL,
			State: r.State, FracDone: r.FractionDone, CPUTime: r.CurrentCPUTime,
			GPU: r.IsGPU(), Deadline: r.ReportDeadline, ExitStatus: r.ExitStatus,
			CmdLine: r.CmdLine, AppVersionNum: r.AppVersionNum,
			ReadyToReport: r.ReadyToReport,
		})
	}
	return out
}

func (a *stateAdapter) RemoveResult(name string) { a.s.RemoveResult(name) }

func (a *stateAdapter) GetHostInfo() scheduler.HostInfoSnapshot {
	a.s.RLock()
	defer a.s.RUnlock()
	hi := a.s.HostInfo
	nvidiaName, atiName := "", ""
	if len(hi.Coprocs.NvidiaDeviceNames) > 0 {
		nvidiaName = hi.Coprocs.NvidiaDeviceNames[0]
	}
	if len(hi.Coprocs.AtiDeviceNames) > 0 {
		atiName = hi.Coprocs.AtiDeviceNames[0]
	}
	return scheduler.HostInfoSnapshot{
		OSName: hi.OSName, OSVersion: hi.OSVersion, Vendor: hi.PVendor,
		Model: hi.PModel, Ncpus: int(hi.PNcpus), PFlops: hi.PFlops,
		MNbytes: hi.MNbytes, DFree: hi.DFree, DTotal: hi.DTotal, HostCPID: hi.HostCPID,
		NvidiaCount: int(hi.Coprocs.NvidiaDevCount), NvidiaName: nvidiaName,
		AtiCount: int(hi.Coprocs.AtiDevCount), AtiName: atiName,
	}
}

func (a *stateAdapter) UpdateProjectCredit(url string, userTotal, userExpavg, hostTotal, hostExpavg float64) {
	a.s.UpdateProjectCredit(url, userTotal, userExpavg, hostTotal, hostExpavg)
}

func (a *stateAdapter) GetNetworkMode() int  { return a.s.GetNetworkMode() }
func (a *stateAdapter) GetDiskUsage() int64  { return a.s.GetDiskUsage() }
func (a *stateAdapter) SetDiskUsage(v int64) { a.s.SetDiskUsage(v) }
func (a *stateAdapter) UpdateStats(ok bool, cpu, gpu, credit float64) {
	a.s.UpdateStats(ok, cpu, gpu, credit)
}
func (a *stateAdapter) AddMessage(body, project string, pri int) { a.s.AddMessage(body, project, pri) }
func (a *stateAdapter) SetProjectSchedPending(url string, pending bool) {
	a.s.SetProjectSchedPending(url, pending)
}
func (a *stateAdapter) SetProjectName(url, name string) { a.s.SetProjectName(url, name) }
func (a *stateAdapter) SetProjectRPCState(url string, hostID, rpcSeqno int) {
	a.s.SetProjectRPCState(url, hostID, rpcSeqno)
}
func (a *stateAdapter) Save() { a.s.Save() }

type stateWorkerAdapter struct{ s *state.State }

func (a *stateWorkerAdapter) GetResults() []worker.ResultSnapshot {
	a.s.RLock()
	defer a.s.RUnlock()
	shares := make(map[string]float64, len(a.s.Projects))
	for _, p := range a.s.Projects {
		shares[p.MasterURL] = p.ResourceShare
	}
	var out []worker.ResultSnapshot
	for _, r := range a.s.Results {
		out = append(out, worker.ResultSnapshot{
			Name: r.Name, WuName: r.WuName, ProjectURL: r.ProjectURL,
			State: r.State, FracDone: r.FractionDone, CPUTime: r.CurrentCPUTime,
			Slot: r.SlotPath, GPU: r.IsGPU(),
			Deadline: r.ReportDeadline, ExitStatus: r.ExitStatus,
			CmdLine: r.CmdLine, AppVersionNum: r.AppVersionNum, AppName: r.AppName,
			Files: convertFiles(r.Files), Suspended: r.SuspendedViaGUI,
			ResourceShare: shares[r.ProjectURL],
		})
	}
	return out
}

// IsSuspended reports a task's current suspend flag by name, polled by the
// worker while a real OS process is running so a suspend/resume click made
// mid-task actually reaches it (see internal/worker's suspendTicker).
func (a *stateWorkerAdapter) IsSuspended(name string) bool {
	a.s.RLock()
	defer a.s.RUnlock()
	for _, r := range a.s.Results {
		if r.Name == name {
			return r.SuspendedViaGUI != 0
		}
	}
	return false
}

func convertFiles(files []state.FileInfo) []worker.FileRef {
	var out []worker.FileRef
	for _, f := range files {
		out = append(out, worker.FileRef{
			Name: f.Name, URL: f.URL, NBytes: f.NBytes, MD5: f.MD5, MainProgram: f.MainProgram,
		})
	}
	return out
}

// UpdateResult records a task's progress. The worker's own state numbers are
// not BOINC's, so this also fills the fields managers use to classify a task:
// active_task marks it as running, and a failure always carries a non-zero
// exit status.
func (a *stateWorkerAdapter) UpdateResult(name string, st int, fracDone float64, elapsed float64, exitStatus int) {
	a.s.Lock()
	defer a.s.Unlock()
	for i := range a.s.Results {
		r := &a.s.Results[i]
		if r.Name != name {
			continue
		}
		if st == worker.StateError && exitStatus == 0 {
			exitStatus = worker.ExitComputeError
		}
		if st == worker.StateReady {
			fracDone = 1
		}
		r.State = st
		r.FractionDone = fracDone
		r.CurrentCPUTime = elapsed
		r.ElapsedTime = elapsed
		r.ExitStatus = exitStatus
		r.EstimatedCPUTimeRemaining = 0
		if st == worker.StateCompute {
			r.ActiveTask = 1
			if fracDone > 0.001 && fracDone < 1 {
				r.EstimatedCPUTimeRemaining = elapsed * (1 - fracDone) / fracDone
			}
		} else {
			r.ActiveTask = 0
		}
		if st == worker.StateReady || st == worker.StateError {
			r.ReadyToReport = 1
		}
		return
	}
}

func (a *stateWorkerAdapter) SetSlotPath(name, slotPath string) {
	a.s.Lock()
	defer a.s.Unlock()
	for i := range a.s.Results {
		if a.s.Results[i].Name == name {
			a.s.Results[i].SlotPath = slotPath
			return
		}
	}
}

func (a *stateWorkerAdapter) RemoveResult(name string) { a.s.RemoveResult(name) }
func (a *stateWorkerAdapter) GetTaskMode() int         { return a.s.GetTaskMode() }
func (a *stateWorkerAdapter) GetDiskUsage() int64      { return a.s.GetDiskUsage() }
func (a *stateWorkerAdapter) GetDiskQuota() int64      { return a.s.GetDiskQuota() }
func (a *stateWorkerAdapter) SetDiskUsage(v int64)     { a.s.SetDiskUsage(v) }
func (a *stateWorkerAdapter) UpdateStats(ok bool, cpu, gpu, credit float64) {
	a.s.UpdateStats(ok, cpu, gpu, credit)
}
func (a *stateWorkerAdapter) RecordTaskDay(url string, ok bool, cpuTime float64) {
	a.s.RecordTaskDay(url, ok, cpuTime)
}
func (a *stateWorkerAdapter) AddMessage(body, project string, pri int) {
	a.s.AddMessage(body, project, pri)
}
func (a *stateWorkerAdapter) Save() { a.s.Save() }

type cacheAdapter struct{ c *cache.Manager }

func (a *cacheAdapter) Full(gpu bool) bool                 { return a.c.Full(gpu) }
func (a *cacheAdapter) AllocSlot(gpu bool) (string, error) { return a.c.AllocSlot(gpu) }
func (a *cacheAdapter) FreeSlot(slot string) error         { return a.c.FreeSlot(slot) }
func (a *cacheAdapter) SlotDir(gpu bool) string            { return a.c.SlotDir(gpu) }
func (a *cacheAdapter) ProjectDir(gpu bool) string         { return a.c.ProjectDir(gpu) }

type projectAdapter struct{ s *state.State }

func (a *projectAdapter) GetAuthInfo(projectURL string) (auth, name string, ok bool) {
	a.s.RLock()
	defer a.s.RUnlock()
	for _, p := range a.s.Projects {
		if p.MasterURL == projectURL {
			return p.Authenticator, p.Name, true
		}
	}
	return "", "", false
}
