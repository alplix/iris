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
	RPCSeqno       int
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
	// SetProjectName gives a project its name if it has none yet.
	SetProjectName(url, name string)
	// SetProjectRPCState stores the host id the server assigned and the number
	// of contacts made, so both are sent back next time.
	SetProjectRPCState(url string, hostID, rpcSeqno int)
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

	// Coprocessor fields for GPU work requests. BOINC groups a host's GPUs
	// of the same vendor into one aggregate <coproc_cuda>/<coproc_ati>
	// element (count + one representative name), not one element per
	// device — see matchAppVersion's neighbor, buildCoprocsXML.
	NvidiaCount int
	NvidiaName  string
	AtiCount    int
	AtiName     string
}

type ProjectInfo struct {
	Name                string
	URL                 string
	Authenticator       string
	TotalCredit         float64
	ExpAvgCredit        float64
	ResourceShare       float64
	HostID              int
	RPCSeqno            int
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
	// AppName is the matched app_version's own name, if the scheduler sent
	// one (see matchAppVersion) — empty for the demo/legacy stub path.
	AppName       string
	Files         []FileInfo
	ReadyToReport int
}

type FileInfo struct {
	Name   string
	URL    string
	NBytes float64
	MD5    string
	// MainProgram marks the one file (from a matched app_version) that is the
	// actual executable to launch, as opposed to an input/library file.
	MainProgram bool
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
		ps.HostID = info.HostID
		ps.RPCSeqno = info.RPCSeqno
		return ps
	}
	ps := &ProjectState{
		URL:            info.URL,
		Authenticator:  info.Authenticator,
		Name:           info.Name,
		MinRPCInterval: e.cfg.MinRPCInterval,
		HostID:         info.HostID,
		RPCSeqno:       info.RPCSeqno,
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

// targetBufferSecs is how much CPU time to try to keep queued per otherwise
// idle core, roughly matching real BOINC's default "store at least ~1 day
// of work" preference. Iris does not (yet) track per-task remaining time
// precisely enough to fetch exactly a full day's buffer, so this is a
// simple, honest heuristic rather than BOINC's own debt-based algorithm:
// ask for one full core's worth of work for every core that does not
// already have something queued for this project.
const targetBufferSecs = 24 * 60 * 60

// workFetchRequest sizes a scheduler request's work-fetch fields. Without
// these, a scheduler has no signal that Iris wants any work at all, and
// many will send none — a fresh attach could otherwise sit at zero tasks
// forever even once contact succeeds.
func workFetchRequest(ncpus, alreadyQueued int) (workReqSecs, cpuReqSecs, cpuReqInstances float64) {
	if ncpus <= 0 {
		ncpus = 1
	}
	idle := ncpus - alreadyQueued
	if idle <= 0 {
		return 0, 0, 0
	}
	cpuReqSecs = float64(idle) * targetBufferSecs
	return cpuReqSecs, cpuReqSecs, float64(idle)
}

// countQueuedForProject counts results for url that still occupy a work
// slot — downloading, queued or actively computing — as opposed to ones
// merely awaiting their report (state >= 4), which a scheduler will not
// re-send regardless.
func countQueuedForProject(results []ResultInfo, url string) int {
	n := 0
	for _, r := range results {
		if r.ProjectURL == url && r.State < 4 {
			n++
		}
	}
	return n
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
	shareFraction := resourceShareFraction(e.state.GetProjects(), ps.URL)
	workReqSecs, cpuReqSecs, cpuReqInstances := workFetchRequest(hostInfo.Ncpus, countQueuedForProject(e.state.GetResults(), ps.URL))

	req := &Request{
		Authenticator: ps.Authenticator,
		HostID:        ps.HostID,
		RPCSeqno:      ps.RPCSeqno,
		Platform:      Platform(),
		VersionNum:    802,
		Timestamp:     float64(time.Now().Unix()),
		TeamID:        ps.TeamID,
		TotalCredit:   ps.TotalCredit,
		Joined:        1,

		ResourceShareFraction: shareFraction,
		RRSFraction:           shareFraction,
		PRRSFraction:          shareFraction,
		// The reference client writes <coprocs> next to <host_info>, not inside it.
		Coprocs: buildCoprocsXML(hostInfo),
		HostInfo: &HostInfoXML{
			HostCPID:  e.cfg.HostCPID,
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
		CoreClientVer:   product.UserAgent(),
		WorkReqSeconds:  workReqSecs,
		CPUReqSecs:      cpuReqSecs,
		CPUReqInstances: cpuReqInstances,
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

	// A reply came back: remember the host id the server assigned (without it
	// every request would register a new host) and count this contact.
	if reply.HostID > 0 {
		ps.HostID = reply.HostID
	}
	ps.RPCSeqno++
	e.state.SetProjectRPCState(ps.URL, ps.HostID, ps.RPCSeqno)

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
	if reply.RequestDelay > 0 {
		// Honor the server's back-off (never poll faster than the engine's own
		// floor, either): a project that says "come back in a day" means it.
		d := time.Duration(reply.RequestDelay * float64(time.Second))
		if d < e.cfg.MinRPCInterval {
			d = e.cfg.MinRPCInterval
		}
		ps.MinRPCInterval = d
	}
	haveMessage := false
	for _, m := range reply.Messages {
		if text := strings.TrimSpace(m.Text); text != "" {
			haveMessage = true
			log.Printf("[Scheduler] %s says: %s", ps.URL, text)
			e.state.AddMessage(text, ps.URL, replyMessagePriority(m))
		}
	}
	if reply.ProjectName != "" && strings.TrimSpace(ps.Name) == "" {
		// A project attached without a name (only its URL) learns its real one here.
		e.state.SetProjectName(ps.URL, reply.ProjectName)
		ps.Name = reply.ProjectName
	}

	fileMap := make(map[string]FileInfoXML)
	for _, fi := range reply.FileInfos {
		fileMap[fi.Name] = fi
	}

	for _, rr := range reply.Results {
		e.handleWork(ps, rr, fileMap, reply.AppVersions)
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
	// The server's own reply messages (already logged above) usually explains
	// itself; a quiet, successful contact otherwise leaves no trace at all,
	// making it impossible to tell "never tried" apart from "tried, nothing
	// to do" — so a plain contact confirmation is always logged.
	if !haveMessage {
		e.state.AddMessage(fmt.Sprintf("Contacted %s: %d new task(s)", ps.URL, len(reply.Results)), ps.URL, 1)
	}
}

func (e *Engine) handleWork(ps *ProjectState, rr ReplyResult, fileMap map[string]FileInfoXML, appVersions []AppVersionXML) {
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

	appName := ""
	if av, ok := matchAppVersion(appVersions, rr); ok {
		appName = av.AppName
		for _, ref := range av.FileRef {
			fi, ok := fileMap[ref.FileName]
			fInfo := FileInfo{Name: ref.FileName, MainProgram: ref.MainProgram != nil}
			if ok {
				fInfo.URL, fInfo.NBytes, fInfo.MD5 = fi.URL, fi.NBytes, fi.MD5
			}
			files = append(files, fInfo)
		}
		log.Printf("[Scheduler] %s uses app_version %s v%d (plan=%s, %d file(s))",
			rr.Name, av.AppName, av.VersionNum, av.PlanClass, len(av.FileRef))
	} else if len(appVersions) > 0 {
		// The scheduler offered real app_versions but none of them lines up
		// with what this result asked for — the task will fall back to the
		// legacy demo-stub executable names in worker.findExecutable, which
		// won't exist, so it will visibly fail rather than silently stall.
		log.Printf("[Scheduler] %s: no app_version matches app_version_num=%d plan_class=%q",
			rr.Name, rr.AppVersionNum, rr.PlanClass)
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
		AppName:       appName,
		Files:         files,
	}
	e.state.AddResult(result)
	e.state.Save()
	log.Printf("[Scheduler] Assigned %s (gpu=%v, files=%d, cmdline=%q)", rr.Name, isGPU, len(files), rr.CmdLine)
}

// matchAppVersion finds the app_version a result belongs to. BOINC's own
// scheduler reply has no single field tying a <result> straight to one
// <app_version> — real clients resolve it through the workunit's app_id,
// which this codebase does not parse (see the "Not implemented yet" caveat
// this leaves in place). Instead this matches on what a <result> does carry:
// app_version_num and plan_class, which is exact whenever a project (as
// almost all do) only ever offers one app per plan class per platform to a
// given host in a single RPC. A project multiplexing several distinct apps
// under identical (version_num, plan_class) pairs in the same reply would
// defeat this — considered rare enough to accept for now.
func matchAppVersion(appVersions []AppVersionXML, rr ReplyResult) (AppVersionXML, bool) {
	for _, av := range appVersions {
		if av.VersionNum == rr.AppVersionNum && av.PlanClass == rr.PlanClass {
			return av, true
		}
	}
	// plan_class is often empty for a plain CPU app on both sides; still
	// require the version number to match rather than guessing blindly.
	for _, av := range appVersions {
		if av.VersionNum == rr.AppVersionNum {
			return av, true
		}
	}
	if len(appVersions) == 1 {
		return appVersions[0], true
	}
	return AppVersionXML{}, false
}

// buildCoprocsXML turns the host's detected GPUs into the <coprocs> block a
// scheduler reads to decide whether to offer GPU app_versions at all. It
// returns nil (omitting the element entirely) for a host with no GPU of a
// vendor BOINC recognizes here, matching how a real GPU-less host's request
// looks on the wire.
func buildCoprocsXML(hi HostInfoSnapshot) *CoprocsXML {
	if hi.NvidiaCount <= 0 && hi.AtiCount <= 0 {
		return nil
	}
	c := &CoprocsXML{}
	if hi.NvidiaCount > 0 {
		c.CUDA = &CoprocCudaXML{Count: hi.NvidiaCount, Name: hi.NvidiaName, HaveCUDA: 1, HaveOpenCL: 1}
	}
	if hi.AtiCount > 0 {
		c.ATI = &CoprocAtiXML{Count: hi.AtiCount, Name: hi.AtiName, HaveOpenCL: 1}
	}
	return c
}

// resourceShareFraction is this project's share of the total resource share
// across every attached project (BOINC's default of 100 for one never set),
// which is what a scheduler expects instead of a bare share number.
func resourceShareFraction(projects []ProjectInfo, url string) float64 {
	share := func(p ProjectInfo) float64 {
		if p.ResourceShare <= 0 {
			return 100
		}
		return p.ResourceShare
	}
	total, mine := 0.0, 0.0
	for _, p := range projects {
		total += share(p)
		if p.URL == url {
			mine = share(p)
		}
	}
	if total <= 0 || mine <= 0 {
		return 1
	}
	return mine / total
}

// replyMessagePriority ranks a scheduler's own message so an error is shown as
// one. Real schedulers mark even "Error in request message" and "Invalid or
// missing account key" as priority="low", so the text is checked as well.
func replyMessagePriority(m ReplyMessage) int {
	if strings.EqualFold(m.Priority, "high") {
		return 3
	}
	t := strings.ToLower(m.Text)
	for _, k := range []string{"error", "invalid", "need version", "missing", "not found", "fail", "unable", "cannot", "can't"} {
		if strings.Contains(t, k) {
			return 3
		}
	}
	return 1
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
