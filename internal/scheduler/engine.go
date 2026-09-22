package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/alplix/iris/internal/product"
)

type Engine struct {
	state       StateManager
	cache       CacheManager
	projects    map[string]*ProjectState
	mu          sync.RWMutex
	stop        chan struct{}
	forceUpdate chan string
	cfg         EngineConfig
}

type EngineConfig struct {
	SchedulerInterval time.Duration
	MinRPCInterval    time.Duration
	MaxResultsPerRPC  int
	UserAgent         string
	DataDir           string
	HostCPID          string
}

type ProjectState struct {
	URL            string
	Authenticator  string
	Name           string
	MinRPCInterval time.Duration
	LastRPC        time.Time
	ServerTime     float64
	TotalCredit    float64
	TeamID         int
	HostID         int
	Config         ProjectConfig
	// SchedulerURLs caches what DiscoverSchedulerURL found on the project's
	// master page, so it is scraped only once per run rather than before
	// every single contact.
	SchedulerURLs []string
}

type ProjectConfig struct {
	MinRPCTime  float64
	IdleDelDays float64
}

type StateManager interface {
	GetProjects() []ProjectInfo
	AddResult(r ResultInfo)
	GetResults() []ResultInfo
	RemoveResult(name string)
	GetHostInfo() HostInfoSnapshot
	GetNetworkMode() int
	GetDiskUsage() int64
	SetDiskUsage(v int64)
	UpdateStats(success bool, cpuTime, gpuTime, credit float64)
	UpdateProjectCredit(url string, userTotal, userExpavg, hostTotal, hostExpavg float64)
	AddMessage(body, project string, pri int)
	SetProjectSchedPending(url string, pending bool)
	Save()
}

type CacheManager interface {
	AllocSlot(gpu bool) (string, error)
	FreeSlot(slot string) error
	SlotDir(gpu bool) string
	ProjectDir(gpu bool) string
}

type HostInfoSnapshot struct {
	OSName    string
	OSVersion string
	Vendor    string
	Model     string
	Ncpus     int
	PFlops    float64
	MNbytes   float64
	DFree     float64
	DTotal    float64
	HostCPID  string
	GPUs      []GPUSnapshot
}

type GPUSnapshot struct {
	Name   string
	Vendor string
}

type ProjectInfo struct {
	Name                string
	URL                 string
	Authenticator       string
	TotalCredit         float64
	ExpAvgCredit        float64
	ResourceShare       float64
	HostID              int
	SuspendedViaGUI     int
	DontRequestMoreWork int
}

type ResultInfo struct {
	Name          string
	WuName        string
	ProjectURL    string
	State         int
	FracDone      float64
	CPUTime       float64
	Slot          string
	GPU           bool
	Deadline      float64
	ExitStatus    int
	CmdLine       string
	AppVersionNum int
	Files         []FileInfo
	ReadyToReport int
}

type FileInfo struct {
	Name   string
	URL    string
	NBytes float64
	MD5    string
}

func NewEngine(state StateManager, cache CacheManager, cfg EngineConfig) *Engine {
	if cfg.SchedulerInterval == 0 {
		cfg.SchedulerInterval = 60 * time.Second
	}
	if cfg.MinRPCInterval == 0 {
		cfg.MinRPCInterval = 30 * time.Second
	}
	if cfg.MaxResultsPerRPC == 0 {
		cfg.MaxResultsPerRPC = 8
	}
	return &Engine{
		state:       state,
		cache:       cache,
		projects:    make(map[string]*ProjectState),
		stop:        make(chan struct{}),
		forceUpdate: make(chan string, 8),
		cfg:         cfg,
	}
}

// RequestUpdate asks the engine to contact url's scheduler as soon as
// possible, bypassing the normal per-project rate limit — this is what the
// "Update" button in the manager triggers. It never blocks or runs the RPC
// itself: the request is handed to the engine's own loop goroutine, so it
// can never race with a periodic cycle already in progress.
func (e *Engine) RequestUpdate(url string) {
	select {
	case e.forceUpdate <- url:
	default:
		// Already full of pending requests; a periodic cycle is imminent
		// regardless (at most cfg.SchedulerInterval away).
	}
}

func (e *Engine) Start() {
	log.Printf("[Scheduler] Starting, interval=%s", e.cfg.SchedulerInterval)
	go e.loop()
}

func (e *Engine) Stop() {
	close(e.stop)
	log.Println("[Scheduler] Stopped")
}

func (e *Engine) loop() {
	ticker := time.NewTicker(e.cfg.SchedulerInterval)
	defer ticker.Stop()
	e.runCycle()
	for {
		select {
		case <-ticker.C:
			e.runCycle()
		case url := <-e.forceUpdate:
			e.forceOne(url)
		case <-e.stop:
			return
		}
	}
}

// forceOne contacts a single project's scheduler right away, on behalf of
// RequestUpdate. It looks the project up fresh from state rather than only
// e.projects, so a project updated moments after being attached — before
// any periodic cycle has had a chance to discover it — is still found.
func (e *Engine) forceOne(url string) {
	for _, info := range e.state.GetProjects() {
		if info.URL == url {
			e.doRPC(e.getOrCreateProject(info))
			return
		}
	}
}

func (e *Engine) runCycle() {
	if e.state.GetNetworkMode() == 3 {
		return
	}
	for _, proj := range e.state.GetProjects() {
		ps := e.getOrCreateProject(proj)
		if proj.SuspendedViaGUI != 0 {
			continue
		}
		if proj.DontRequestMoreWork != 0 {
			continue
		}
		if time.Since(ps.LastRPC) < ps.MinRPCInterval {
			continue
		}
		e.doRPC(ps)
	}
}

func (e *Engine) getOrCreateProject(info ProjectInfo) *ProjectState {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ps, ok := e.projects[info.URL]; ok {
		ps.Authenticator = info.Authenticator
		ps.Name = info.Name
		ps.TotalCredit = info.TotalCredit
		return ps
	}
	ps := &ProjectState{
		URL:            info.URL,
		Authenticator:  info.Authenticator,
		Name:           info.Name,
		MinRPCInterval: e.cfg.MinRPCInterval,
	}
	e.projects[info.URL] = ps
	return ps
}

// schedulerURLFor returns the address to actually contact for ps, caching a
// successful discovery on ps so later contacts skip re-fetching the master
// page. A project's master_url is essentially never also its scheduler's
// address on a real, actively-run project (see DiscoverSchedulerURL); if
// discovery fails for any reason, the old "cgi-bin/scheduler" convention is
// tried as a last resort rather than giving up before ever attempting a
// contact.
func (e *Engine) schedulerURLFor(ps *ProjectState, cli *Client) string {
	if len(ps.SchedulerURLs) > 0 {
		return ps.SchedulerURLs[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	urls, err := cli.DiscoverSchedulerURL(ctx)
	if err != nil || len(urls) == 0 {
		log.Printf("[Scheduler] Could not discover a scheduler address for %s, trying the conventional path instead: %v", ps.URL, err)
		return cli.GetSchedulerURL()
	}
	ps.SchedulerURLs = urls
	return urls[0]
}

func (e *Engine) doRPC(ps *ProjectState) {
	log.Printf("[Scheduler] RPC to %s", ps.URL)
	// Whatever happens below, this contact has now been attempted — clear
	// any "waiting for an update" flag so the UI never shows it stuck
	// forever; a failure is reported as a message instead, below.
	e.state.SetProjectSchedPending(ps.URL, false)

	cli := NewClient(ps.URL)
	cli.SetAuth(ps.Authenticator)
	cli.SetSchedulerURL(e.schedulerURLFor(ps, cli))

	hostInfo := e.state.GetHostInfo()

	req := &Request{
		Authenticator: ps.Authenticator,
		HostCPID:      e.cfg.HostCPID,
		Platform:      Platform(),
		VersionNum:    802,
		Timestamp:     float64(time.Now().Unix()),
		TeamID:        ps.TeamID,
		TotalCredit:   ps.TotalCredit,
		Joined:        1,
		ResourceShare: 100,
		HostInfo: &HostInfoXML{
			OsName:    hostInfo.OSName,
			OsVersion: hostInfo.OSVersion,
			PVendor:   hostInfo.Vendor,
			PModel:    hostInfo.Model,
			PNcpus:    hostInfo.Ncpus,
			PFlops:    hostInfo.PFlops,
			MNbytes:   hostInfo.MNbytes,
			DFree:     hostInfo.DFree,
			DTotal:    hostInfo.DTotal,
			ConnType:  3,
		},
		CoreClientVer: product.UserAgent(),
	}

	for _, r := range e.state.GetResults() {
		if r.ProjectURL != ps.URL {
			continue
		}
		req.Results = append(req.Results, ResultXML{
			Name:           r.Name,
			WuName:         r.WuName,
			ProjectURL:     r.ProjectURL,
			FractionDone:   r.FracDone,
			CPUTime:        r.CPUTime,
			ExitStatus:     r.ExitStatus,
			State:          r.State,
			Platform:       Platform(),
			VersionNum:     800,
			ReportDeadline: r.Deadline,
		})
	}

	reply, err := cli.SendRequest(req)
	if err != nil {
		log.Printf("[Scheduler] RPC failed for %s: %v", ps.URL, err)
		e.state.AddMessage(fmt.Sprintf("Couldn't reach %s: %v", ps.URL, err), ps.URL, 3)
		ps.LastRPC = time.Now()
		return
	}

	if reply.Error != "" {
		log.Printf("[Scheduler] Server error from %s: %s", ps.URL, reply.Error)
		e.state.AddMessage(reply.Error, ps.URL, 2)
		ps.LastRPC = time.Now()
		return
	}

	ps.LastRPC = time.Now()
	userTotal, userAvg := reply.UserTotalCredit, reply.UserExpavgCredit
	if userTotal == 0 {
		userTotal, userAvg = reply.TotalCredit, reply.ExpAvgCredit
	}
	ps.TotalCredit = userTotal
	if userTotal > 0 || reply.HostTotalCredit > 0 {
		e.state.UpdateProjectCredit(ps.URL, userTotal, userAvg, reply.HostTotalCredit, reply.HostExpavgCredit)
	}

	if reply.ServerTime > 0 {
		ps.ServerTime = reply.ServerTime
	}
	if reply.Delay > 0 {
		ps.MinRPCInterval = time.Duration(reply.Delay) * time.Second
	}
	if reply.Message != "" {
		e.state.AddMessage(reply.Message, ps.URL, 1)
	}

	fileMap := make(map[string]FileInfoXML)
	for _, fi := range reply.FileInfos {
		fileMap[fi.Name] = fi
	}

	for _, rr := range reply.Results {
		e.handleWork(ps, rr, fileMap)
	}

	echoed := make(map[string]bool)
	for _, rr := range reply.Results {
		echoed[rr.Name] = true
	}
	reported := make(map[string]bool)
	for _, r := range e.state.GetResults() {
		if r.ProjectURL == ps.URL && (r.State == 4 || r.State == 5) && r.ReadyToReport == 1 {
			reported[r.Name] = true
		}
	}
	removed := 0
	for name := range reported {
		if echoed[name] {
			continue
		}
		e.state.RemoveResult(name)
		removed++
	}

	log.Printf("[Scheduler] RPC done for %s, credit=%.1f, got %d tasks, reported %d, removed %d",
		ps.URL, userTotal, len(reply.Results), len(reported), removed)
	// The server's own reply.Message (already logged above) usually explains
	// itself; a quiet, successful contact otherwise leaves no trace at all,
	// making it impossible to tell "never tried" apart from "tried, nothing
	// to do" — so a plain contact confirmation is always logged.
	if reply.Message == "" {
		e.state.AddMessage(fmt.Sprintf("Contacted %s: %d new task(s)", ps.URL, len(reply.Results)), ps.URL, 1)
	}
}

func (e *Engine) handleWork(ps *ProjectState, rr ReplyResult, fileMap map[string]FileInfoXML) {
	log.Printf("[Scheduler] Work: %s (wu=%s, prio=%.2f, plan=%s)", rr.Name, rr.WuName, rr.Priority, rr.PlanClass)
	isGPU := detectGPUFromResult(rr)

	var files []FileInfo
	for _, ref := range rr.FileRef {
		fi, ok := fileMap[ref.Name]
		if ok {
			files = append(files, FileInfo{Name: fi.Name, URL: fi.URL, NBytes: fi.NBytes, MD5: fi.MD5})
		} else {
			files = append(files, FileInfo{Name: ref.Name, MD5: ref.MD5})
		}
	}

	result := ResultInfo{
		Name:          rr.Name,
		WuName:        rr.WuName,
		ProjectURL:    ps.URL,
		State:         0,
		GPU:           isGPU,
		Deadline:      rr.EarliestDeadline,
		CmdLine:       rr.CmdLine,
		AppVersionNum: rr.AppVersionNum,
		Files:         files,
	}
	e.state.AddResult(result)
	e.state.Save()
	log.Printf("[Scheduler] Assigned %s (gpu=%v, files=%d, cmdline=%q)", rr.Name, isGPU, len(files), rr.CmdLine)
}

func detectGPUFromResult(rr ReplyResult) bool {
	pc := strings.ToLower(rr.PlanClass)
	if strings.Contains(pc, "gpu") || strings.Contains(pc, "cuda") || strings.Contains(pc, "opencl") {
		return true
	}
	cl := strings.ToLower(rr.CmdLine)
	if strings.Contains(cl, "gpu") || strings.Contains(cl, "cuda") || strings.Contains(cl, "opencl") {
		return true
	}
	so := strings.ToLower(rr.StdOut)
	if strings.Contains(so, "cuda") || strings.Contains(so, "opencl") {
		return true
	}
	return false
}
