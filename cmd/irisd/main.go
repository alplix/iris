package main

import (
	"encoding/xml"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alplix/iris/internal/cache"
	"github.com/alplix/iris/internal/config"
	"github.com/alplix/iris/internal/detect"
	"github.com/alplix/iris/internal/guirpc"
	"github.com/alplix/iris/internal/product"
	"github.com/alplix/iris/internal/project"
	"github.com/alplix/iris/internal/scheduler"
	"github.com/alplix/iris/internal/state"
	"github.com/alplix/iris/internal/worker"
)

func guiRPCPort() int {
	if v := os.Getenv("IRIS_GUI_RPC_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return p
		}
		fmt.Fprintf(os.Stderr, "Warning: invalid IRIS_GUI_RPC_PORT, using %d\n", product.DefaultGUIRPCPort)
	}
	return product.DefaultGUIRPCPort
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
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			printUsage()
			os.Exit(1)
		}
	}
	runDaemon()
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

	st.HostInfo = detectHostInfo()

	fmt.Println(banner())
	fmt.Println()
	fmt.Printf("Data directory: %s\n", dataDir)
	fmt.Printf("CPU: %s %s (%d cores)\n", st.HostInfo.PVendor, st.HostInfo.PModel, int(st.HostInfo.PNcpus))
	if st.HostInfo.MNbytes > 0 {
		fmt.Printf("RAM: %.1f GB\n", st.HostInfo.MNbytes/1073741824)
	}
	if st.HostInfo.DTotal > 0 {
		fmt.Printf("Disk: %.1f GB / %.1f GB free\n", st.HostInfo.DTotal/1073741824, st.HostInfo.DFree/1073741824)
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
	handler := &clientHandler{state: st, cache: cacheMgr, dataDir: dataDir, cfg: cfg}
	srv := guirpc.NewServer(handler, password)
	if err := srv.Start(guiRPCAddr); err != nil {
		fatal("GUI RPC server failed: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	srv.SetOnQuit(func() { sig <- syscall.SIGTERM })

	fmt.Printf("GUI RPC listening on %s\n", guiRPCAddr)
	fmt.Println("Ready.")

	schedEngine := scheduler.NewEngine(&stateAdapter{st}, &cacheAdapter{cacheMgr}, scheduler.EngineConfig{
		SchedulerInterval: 60 * time.Second,
		MinRPCInterval:    30 * time.Second,
		UserAgent:         product.UserAgent(),
		DataDir:           dataDir,
		HostCPID:          st.HostInfo.HostCPID,
	})
	schedEngine.Start()

	workerEngine := worker.NewEngine(&stateWorkerAdapter{st}, &cacheAdapter{cacheMgr}, &projectAdapter{st}, &downloaderAdapter{dataDir: dataDir}, &downloaderAdapter{dataDir: dataDir}, worker.Config{
		MaxConcurrent: runtime.NumCPU(),
		DataDir:       dataDir,
		UserAgent:     product.UserAgent(),
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

func detectHostInfo() state.HostInfo {
	specs := detect.Detect()
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
		HostCPID:  specs.HostCPID,
		CamVer:    product.Version,
	}
	for _, g := range specs.GPUs {
		hi.GPUs = append(hi.GPUs, g.Name)
	}
	return hi
}

func loadOrCreatePassword(dataDir string) string {
	fp := filepath.Join(dataDir, "gui_rpc_auth.cfg")
	data, err := os.ReadFile(fp)
	if err == nil {
		p := strings.TrimSpace(string(data))
		if p != "" {
			return p
		}
	}
	pass := fmt.Sprintf("iris_%d", syscall.Getpid())
	os.WriteFile(fp, []byte(pass+"\n"), 0o644)
	return pass
}

func stopDaemon() {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", guiRPCPort()), 3*time.Second)
	if err != nil {
		fmt.Println("Iris client is not running.")
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	conn.Write([]byte("<quit/>\n"))
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
	state   *state.State
	cache   *cache.Manager
	dataDir string
	cfg     *config.Config
}

func (h *clientHandler) GetState() ([]byte, error) {
	return h.state.MarshalState()
}

func (h *clientHandler) GetCcStatus() ([]byte, error) {
	s := h.state.Snapshot()
	return xml.MarshalIndent(struct {
		XMLName xml.Name     `xml:"cc_status"`
		Status  state.Status `xml:"cc_status"`
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

func (h *clientHandler) GetStats() ([]byte, error) {
	type dayXML struct {
		Date         string  `xml:"date"`
		Tasks        int     `xml:"tasks"`
		TasksSuccess int     `xml:"tasks_success"`
		TasksError   int     `xml:"tasks_error"`
		TotalCPU     float64 `xml:"total_cpu"`
		TotalGPU     float64 `xml:"total_gpu"`
		CreditEarned float64 `xml:"credit_earned"`
	}
	type statsXML struct {
		XMLName xml.Name `xml:"statistics"`
		Days    []dayXML `xml:"day"`
	}
	s := h.state.Snapshot()
	var days []dayXML
	for _, d := range s.Stats {
		days = append(days, dayXML{
			Date: d.Date, Tasks: d.Tasks, TasksSuccess: d.TasksSuccess,
			TasksError: d.TasksError, TotalCPU: d.TotalCPU, TotalGPU: d.TotalGPU,
			CreditEarned: d.CreditEarned,
		})
	}
	return xml.MarshalIndent(statsXML{Days: days}, "", "  ")
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
	return []byte(`<daily_xfers/>`), nil
}

func (h *clientHandler) GetPrefsOverride() ([]byte, error) {
	return []byte(`<global_prefs_override/>`), nil
}

func (h *clientHandler) SetPrefsOverride(pairs [][2]string) error { return nil }

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
	}
	return nil
}

func (h *clientHandler) FileTransferOp(name, op string) error { return nil }

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

func (h *clientHandler) RunBenchmarks() error {
	h.state.AddMessage("Benchmarks started", "", 1)
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
			SuspendedViaGUI:     p.SuspendedViaGUI,
			DontRequestMoreWork: p.DontRequestMoreWork,
		})
	}
	return out
}

func (a *stateAdapter) AddResult(r scheduler.ResultInfo) {
	var files []state.FileInfo
	for _, f := range r.Files {
		files = append(files, state.FileInfo{Name: f.Name, URL: f.URL, NBytes: f.NBytes, MD5: f.MD5})
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
	return scheduler.HostInfoSnapshot{
		OSName: hi.OSName, OSVersion: hi.OSVersion, Vendor: hi.PVendor,
		Model: hi.PModel, Ncpus: int(hi.PNcpus), PFlops: hi.PFlops,
		MNbytes: hi.MNbytes, DFree: hi.DFree, DTotal: hi.DTotal, HostCPID: hi.HostCPID,
	}
}

func (a *stateAdapter) GetNetworkMode() int  { return a.s.GetNetworkMode() }
func (a *stateAdapter) GetDiskUsage() int64  { return a.s.GetDiskUsage() }
func (a *stateAdapter) SetDiskUsage(v int64) { a.s.SetDiskUsage(v) }
func (a *stateAdapter) UpdateStats(ok bool, cpu, gpu, credit float64) {
	a.s.UpdateStats(ok, cpu, gpu, credit)
}
func (a *stateAdapter) AddMessage(body, project string, pri int) { a.s.AddMessage(body, project, pri) }
func (a *stateAdapter) Save()                                    { a.s.Save() }

type stateWorkerAdapter struct{ s *state.State }

func (a *stateWorkerAdapter) GetResults() []worker.ResultSnapshot {
	a.s.RLock()
	defer a.s.RUnlock()
	var out []worker.ResultSnapshot
	for _, r := range a.s.Results {
		out = append(out, worker.ResultSnapshot{
			Name: r.Name, WuName: r.WuName, ProjectURL: r.ProjectURL,
			State: r.State, FracDone: r.FractionDone, CPUTime: r.CurrentCPUTime,
			Slot: r.SlotPath, GPU: r.IsGPU(),
			Deadline: r.ReportDeadline, ExitStatus: r.ExitStatus,
			CmdLine: r.CmdLine, AppVersionNum: r.AppVersionNum,
			Files: convertFiles(r.Files), Suspended: r.SuspendedViaGUI,
		})
	}
	return out
}

func convertFiles(files []state.FileInfo) []worker.FileRef {
	var out []worker.FileRef
	for _, f := range files {
		out = append(out, worker.FileRef{
			Name: f.Name, URL: f.URL, NBytes: f.NBytes, MD5: f.MD5,
		})
	}
	return out
}

func (a *stateWorkerAdapter) UpdateResult(name string, state int, fracDone float64, cpuTime float64, exitStatus int) {
	a.s.Lock()
	defer a.s.Unlock()
	for i := range a.s.Results {
		if a.s.Results[i].Name == name {
			a.s.Results[i].State = state
			a.s.Results[i].FractionDone = fracDone
			a.s.Results[i].CurrentCPUTime = cpuTime
			a.s.Results[i].ExitStatus = exitStatus
			if state == worker.StateReady || state == worker.StateError {
				a.s.Results[i].ReadyToReport = 1
			}
			return
		}
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
func (a *stateWorkerAdapter) AddMessage(body, project string, pri int) {
	a.s.AddMessage(body, project, pri)
}
func (a *stateWorkerAdapter) Save() { a.s.Save() }

type cacheAdapter struct{ c *cache.Manager }

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

type downloaderAdapter struct{ dataDir string }

func (d *downloaderAdapter) DownloadFile(projectURL, filename, destPath string) error {
	client := scheduler.NewClient(projectURL)
	return client.DownloadFile(filename, destPath)
}

func (d *downloaderAdapter) DownloadFileByURL(rawURL, destPath string) error {
	return scheduler.DownloadFileByURL(rawURL, destPath)
}

func (d *downloaderAdapter) UploadFile(projectURL, filePath string) error {
	client := scheduler.NewClient(projectURL)
	return client.UploadFile(filePath, "")
}
