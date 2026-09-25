package scheduler

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alplix/iris/internal/detect"
	"github.com/alplix/iris/internal/product"
	"github.com/alplix/iris/internal/project"
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
	// HostNameFn, when set and non-empty, is the computer name reported to
	// projects instead of the machine's own (a Settings choice).
	HostNameFn func() string
	// GPUEnabledFn, when set and returning false, hides the GPUs from the
	// project so it offers no GPU work.
	GPUEnabledFn func() bool
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
	// NoWorkStreak counts consecutive contacts that asked for work and got
	// none; it drives the exponential back-off below.
	NoWorkStreak int
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
	// Video memory in bytes of the first device of each vendor (0 = unknown).
	NvidiaMem float64
	AtiMem    float64
	// CUDA details from nvidia-smi (zero = unknown, nothing is claimed).
	NvidiaCL, AtiCL              *detect.OpenCLDevice // what the OpenCL driver reported, if anything
	NvidiaCCMajor, NvidiaCCMinor int
	CudaVersion                  int
	NvidiaDriver                 string
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
	// Dir is the project's folder, where a person's app_info.xml lives.
	Dir string
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
	VersionNum    int
	// AppName is the matched app_version's own name, if the scheduler sent
	// one (see matchAppVersion) — empty for the demo/legacy stub path.
	AppName       string
	Files         []FileInfo
	ReadyToReport int
	// What a completed result's report needs: which build ran it, where its
	// slot (stderr) is, and the output files that were uploaded.
	Platform  string
	PlanClass string
	Outputs   []OutputInfo
	// EstRuntime is the expected run time in seconds on this host (0 = unknown).
	EstRuntime float64
}

// OutputInfo is one file a task produces and uploads (see state.OutputFile).
type OutputInfo struct {
	Name      string
	OpenName  string
	URLs      []string
	MaxNBytes float64
	Signature string
	Optional  bool
	Present   bool
	NBytes    float64
	MD5       string
	Uploaded  bool
}

type FileInfo struct {
	Name   string
	URL    string
	NBytes float64
	MD5    string
	// MainProgram marks the one file (from a matched app_version) that is the
	// actual executable to launch, as opposed to an input/library file.
	MainProgram bool
	// OpenName is the logical name the application opens the file by.
	OpenName string
	// LocalPath is set for a file that is already on disk (an application
	// named in app_info.xml) and must be copied rather than downloaded.
	LocalPath string
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
		// "No new tasks" still contacts the project to report finished work.
		if proj.DontRequestMoreWork != 0 && !hasFinishedResult(e.state.GetResults(), proj.URL) {
			continue
		}
		since := time.Since(ps.LastRPC)
		if since < ps.MinRPCInterval && !(hasFinishedResult(e.state.GetResults(), proj.URL) && since >= reportRetryInterval(e.cfg.MinRPCInterval)) {
			continue
		}
		e.doRPC(ps)
	}
}

// noWorkBackoff is the pause after n consecutive empty replies: 10 minutes,
// doubling, capped at 4 hours.
func noWorkBackoff(n int) time.Duration {
	d := 10 * time.Minute
	for i := 1; i < n && d < 4*time.Hour; i++ {
		d *= 2
	}
	if d > 4*time.Hour {
		d = 4 * time.Hour
	}
	return d
}

// hasFinishedResult reports whether a completed, uploaded result of the
// project is waiting to be reported.
func hasFinishedResult(results []ResultInfo, url string) bool {
	for _, r := range results {
		if r.ProjectURL == url && r.ReadyToReport == 1 && (r.State == 4 || r.State == 5) {
			return true
		}
	}
	return false
}

// reportRetryInterval is how soon a waiting report may go out even while the
// project is in a back-off: the engine's normal minimum, so a finished task
// is reported promptly instead of waiting out a no-work pause.
func reportRetryInterval(min time.Duration) time.Duration {
	return min
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
	if e.cfg.GPUEnabledFn != nil && !e.cfg.GPUEnabledFn() {
		hostInfo.NvidiaCount, hostInfo.AtiCount = 0, 0
	}
	shareFraction := resourceShareFraction(e.state.GetProjects(), ps.URL)
	workReqSecs, cpuReqSecs, cpuReqInstances := workFetchRequest(hostInfo.Ncpus, countQueuedForProject(e.state.GetResults(), ps.URL))

	gpuQueued := countGPUQueued(e.state.GetResults(), ps.URL)
	projDir := ""
	for _, p := range e.state.GetProjects() {
		if p.URL == ps.URL {
			projDir = p.Dir
		}
		if p.URL == ps.URL && p.DontRequestMoreWork != 0 {
			// Report only: ask for no work of any kind.
			workReqSecs, cpuReqSecs, cpuReqInstances = 0, 0, 0
			gpuQueued = 1 << 20
		}
	}

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
		Coprocs: buildCoprocsXML(hostInfo, gpuQueued),
		HostInfo: &HostInfoXML{
			HostCPID:    e.cfg.HostCPID,
			Timezone:    localTimezone(),
			DomainName:  e.hostName(),
			ProductName: reportedProductName,
			OsName:      reportedOSName(hostInfo.OSName),
			OsVersion:   hostInfo.OSVersion,
			PVendor:     hostInfo.Vendor,
			PModel:      hostInfo.Model,
			PNcpus:      hostInfo.Ncpus,
			PFlops:      hostInfo.PFlops,
			MNbytes:     hostInfo.MNbytes,
			DFree:       hostInfo.DFree,
			DTotal:      hostInfo.DTotal,
			ConnType:    3,
		},
		CoreClientVer:   product.UserAgent(),
		WorkReqSeconds:  workReqSecs,
		CPUReqSecs:      cpuReqSecs,
		CPUReqInstances: cpuReqInstances,
	}

	// The person's own applications (app_info.xml in the project's folder)
	// replace the project's: say so, and list them.
	ai, aiErr := project.LoadAppInfo(projDir)
	if aiErr != nil {
		e.state.AddMessage("Ignoring the app_info.xml of "+ps.URL+": "+aiErr.Error(), ps.URL, 3)
	}
	if ai != nil {
		req.Platform = "anonymous"
		req.AppVersions = clientAppVersions(ai, hostInfo.PFlops)
	}

	var reportedNow []ResultInfo
	for _, r := range e.state.GetResults() {
		// Only finished results whose output files are safely uploaded are
		// reported; a running or still-uploading task must not be.
		if r.ProjectURL != ps.URL || r.ReadyToReport != 1 || (r.State != 4 && r.State != 5) {
			continue
		}
		req.Results = append(req.Results, buildReport(r))
		reportedNow = append(reportedNow, r)
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
	// A project with nothing to send is asked less and less often (the
	// reference client backs off from 10 minutes up to 4 hours), instead of
	// every minute forever. Getting work resets it.
	if len(reply.Results) > 0 {
		ps.NoWorkStreak = 0
		if reply.RequestDelay == 0 {
			ps.MinRPCInterval = e.cfg.MinRPCInterval
		}
	} else if workReqSecs > 0 {
		ps.NoWorkStreak++
		if d := noWorkBackoff(ps.NoWorkStreak); d > ps.MinRPCInterval {
			ps.MinRPCInterval = d
		}
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

	wuMap := make(map[string]WorkunitXML)
	for _, wu := range reply.Workunits {
		wuMap[wu.Name] = wu
	}
	have := make(map[string]bool)
	for _, r := range e.state.GetResults() {
		have[r.Name] = true
	}
	for _, rr := range reply.Results {
		if have[rr.Name] {
			// A resent copy of a task we already hold must not overwrite it.
			continue
		}
		e.handleWork(ps, rr, fileMap, wuMap, reply.AppVersions, ai, projDir)
	}

	// Only results the server acknowledged are forgotten; anything else is
	// reported again next time (BOINC's own rule — a lost reply must not lose
	// a finished task).
	acked := make(map[string]bool)
	for _, a := range reply.ResultAcks {
		acked[a.Name] = true
	}
	removed := 0
	for _, r := range reportedNow {
		if !acked[r.Name] {
			continue
		}
		if r.Slot != "" {
			e.cache.FreeSlot(r.Slot)
		}
		e.state.RemoveResult(r.Name)
		removed++
		e.state.AddMessage(fmt.Sprintf("Reported completed task %s to the project", r.Name), ps.URL, 1)
	}
	if len(reportedNow) > 0 && removed < len(reportedNow) {
		e.state.AddMessage(fmt.Sprintf("Reported %d task(s); the server has not confirmed %d of them yet", len(reportedNow), len(reportedNow)-removed), ps.URL, 2)
	}
	// Tasks the server no longer wants are dropped if they have not started.
	aborted := make(map[string]bool)
	for _, a := range reply.ResultAborts {
		aborted[a.Name] = true
	}
	for _, r := range e.state.GetResults() {
		if aborted[r.Name] && r.ProjectURL == ps.URL && r.State < 2 {
			e.state.RemoveResult(r.Name)
			e.state.AddMessage(fmt.Sprintf("Task %s was cancelled by the project", r.Name), ps.URL, 1)
		}
	}
	reported := reportedNow

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

func (e *Engine) handleWork(ps *ProjectState, rr ReplyResult, fileMap map[string]FileInfoXML, wuMap map[string]WorkunitXML, appVersions []AppVersionXML, ai *project.AppInfo, projDir string) {
	log.Printf("[Scheduler] Work: %s (wu=%s, prio=%.2f, plan=%s)", rr.Name, rr.WuName, rr.Priority, rr.PlanClass)
	isGPU := detectGPUFromResult(rr)
	wu, haveWU := wuMap[rr.WuName]

	inputFile := func(ref FileRefXML) FileInfo {
		if fi, ok := fileMap[ref.Name]; ok {
			return FileInfo{Name: fi.Name, URL: fi.DownloadURL(), NBytes: fi.NBytes, MD5: fi.Checksum(), OpenName: ref.OpenName}
		}
		return FileInfo{Name: ref.Name, MD5: ref.MD5, OpenName: ref.OpenName}
	}

	var files []FileInfo
	var outputs []OutputInfo
	// A real reply lists a task's inputs in its <workunit> and only the OUTPUT
	// files (generated_locally, with an upload address and signed certificate)
	// in the <result>. Older/simple servers put plain download refs straight
	// in the result, which is still accepted.
	if haveWU {
		for _, ref := range wu.FileRef {
			files = append(files, inputFile(ref))
		}
	}
	for _, ref := range rr.FileRef {
		fi, ok := fileMap[ref.Name]
		if ok && fi.IsOutput() {
			open := ref.OpenName
			if open == "" {
				open = fi.Name
			}
			outputs = append(outputs, OutputInfo{
				Name: fi.Name, OpenName: open, URLs: fi.UploadTargets(),
				MaxNBytes: fi.MaxNBytes, Signature: fi.XMLSignature, Optional: ref.Optional != nil,
			})
			continue
		}
		if !haveWU {
			files = append(files, inputFile(ref))
		}
	}

	appName := ""
	if haveWU {
		appName = wu.AppName
	}
	platform := Platform()
	planClass := rr.PlanClass
	versionNum := rr.AppVersionNum
	if versionNum == 0 {
		versionNum = rr.VersionNum
	}
	extraCmd := ""
	if ai != nil {
		// Anonymous platform: the application is the person's own, sitting
		// in the project's folder, not something the project sends.
		if v, ok := ai.Find(appName, versionNum, rr.PlanClass); ok {
			appName, planClass, platform = v.AppName, v.PlanClass, "anonymous"
			extraCmd = strings.TrimSpace(v.CmdLine)
			isGPU = isGPU || v.IsGPU()
			for _, ref := range v.FileRefs {
				files = append(files, FileInfo{Name: ref.FileName, OpenName: ref.OpenName, MainProgram: ref.MainProgram != nil,
					LocalPath: filepath.Join(projDir, ref.FileName)})
			}
			log.Printf("[Scheduler] %s uses your own %s v%d from app_info.xml", rr.Name, v.AppName, v.VersionNum)
		} else {
			e.state.AddMessage(fmt.Sprintf("The project sent %s for %q version %d, which your app_info.xml does not describe", rr.Name, appName, versionNum), ps.URL, 3)
		}
	} else if av, ok := matchAppVersion(appVersions, rr, appName); ok {
		appName = av.AppName
		if av.Platform != "" {
			platform = av.Platform
		}
		if av.VersionNum != 0 {
			versionNum = av.VersionNum
		}
		planClass = av.PlanClass
		for _, ref := range av.FileRef {
			fi, ok := fileMap[ref.FileName]
			fInfo := FileInfo{Name: ref.FileName, OpenName: ref.OpenName, MainProgram: ref.MainProgram != nil}
			if ok {
				fInfo.URL, fInfo.NBytes, fInfo.MD5 = fi.DownloadURL(), fi.NBytes, fi.Checksum()
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
		log.Printf("[Scheduler] %s: no app_version matches version_num=%d plan_class=%q",
			rr.Name, versionNum, rr.PlanClass)
	}

	cmdLine := rr.CmdLine
	if cmdLine == "" && haveWU {
		cmdLine = wu.CmdLine
	}
	if extraCmd != "" {
		cmdLine = strings.TrimSpace(extraCmd + " " + cmdLine)
	}
	result := ResultInfo{
		Name:          rr.Name,
		WuName:        rr.WuName,
		ProjectURL:    ps.URL,
		State:         0,
		GPU:           isGPU,
		Deadline:      rr.ReportDeadline,
		CmdLine:       cmdLine,
		AppVersionNum: versionNum,
		VersionNum:    versionNum,
		AppName:       appName,
		Platform:      platform,
		PlanClass:     planClass,
		Files:         files,
		Outputs:       outputs,
	}
	if haveWU && wu.RscFpopsEst > 0 {
		if flops := e.state.GetHostInfo().PFlops; flops > 0 {
			result.EstRuntime = wu.RscFpopsEst / flops
		}
	}
	if result.Deadline == 0 {
		result.Deadline = rr.EarliestDeadline
	}
	e.state.AddResult(result)
	e.state.Save()
	log.Printf("[Scheduler] Assigned %s (gpu=%v, files=%d, outputs=%d, cmdline=%q)", rr.Name, isGPU, len(files), len(outputs), cmdLine)
}

// matchAppVersion finds the app_version a result belongs to. The reply ties a
// task to one through its workunit's app_name plus the result's version_num
// and plan_class; when an older reply carries none of that, a single offered
// app_version is taken as the only possibility.
func matchAppVersion(appVersions []AppVersionXML, rr ReplyResult, appName string) (AppVersionXML, bool) {
	ver := rr.AppVersionNum
	if ver == 0 {
		ver = rr.VersionNum
	}
	for _, av := range appVersions {
		if (appName == "" || av.AppName == appName) && av.VersionNum == ver && av.PlanClass == rr.PlanClass {
			return av, true
		}
	}
	for _, av := range appVersions {
		if (appName == "" || av.AppName == appName) && av.VersionNum == ver {
			return av, true
		}
	}
	if appName != "" {
		var only []AppVersionXML
		for _, av := range appVersions {
			if av.AppName == appName {
				only = append(only, av)
			}
		}
		if len(only) == 1 {
			return only[0], true
		}
	}
	if len(appVersions) == 1 {
		return appVersions[0], true
	}
	return AppVersionXML{}, false
}

// buildReport turns a finished, fully uploaded result into what a scheduler
// expects in its request.
func buildReport(r ResultInfo) ResultXML {
	state := ReportStateFilesUploaded
	if r.State == 5 {
		state = ReportStateComputeError
	}
	platform := r.Platform
	if platform == "" {
		platform = Platform()
	}
	ver := r.VersionNum
	if ver == 0 {
		ver = r.AppVersionNum
	}
	if ver == 0 {
		ver = 800
	}
	var stderr string
	if r.Slot != "" {
		if b, err := os.ReadFile(filepath.Join(r.Slot, "stderr.txt")); err == nil {
			stderr = string(b)
		}
	}
	x := ResultXML{
		Name:             r.Name,
		FinalCPUTime:     r.CPUTime,
		FinalElapsedTime: r.CPUTime,
		ExitStatus:       r.ExitStatus,
		State:            state,
		Platform:         platform,
		VersionNum:       ver,
		PlanClass:        r.PlanClass,
		AppVersionNum:    ver,
		StderrOut:        NewStderrOut(product.Version, stderr),
	}
	for _, o := range r.Outputs {
		if o.Present && o.Uploaded {
			x.FileInfos = append(x.FileInfos, ResultFileXML{Name: o.Name, NBytes: o.NBytes, MaxNBytes: o.MaxNBytes, MD5: o.MD5})
		}
	}
	return x
}

// buildCoprocsXML turns the host's detected GPUs into the <coprocs> block a
// scheduler reads to decide whether to offer GPU app_versions at all. It
// returns nil (omitting the element entirely) for a host with no GPU of a
// vendor BOINC recognizes here, matching how a real GPU-less host's request
// looks on the wire.
func buildCoprocsXML(hi HostInfoSnapshot, gpuQueued int) *CoprocsXML {
	if hi.NvidiaCount <= 0 && hi.AtiCount <= 0 {
		return nil
	}
	c := &CoprocsXML{}
	if hi.NvidiaCount > 0 {
		c.CUDA = &CoprocCudaXML{Count: hi.NvidiaCount, Name: coprocName(hi.NvidiaName), HaveCUDA: 1, TotalGlobalMem: hi.NvidiaMem,
			Major: hi.NvidiaCCMajor, Minor: hi.NvidiaCCMinor, CudaVersion: hi.CudaVersion, DrvVersion: driverInt(hi.NvidiaDriver)}
		if cl := hi.NvidiaCL; cl != nil {
			c.CUDA.HaveOpenCL = 1
			c.CUDA.OpenCL = openCLXML(cl)
			if c.CUDA.TotalGlobalMem == 0 {
				c.CUDA.TotalGlobalMem = float64(cl.GlobalMem)
			}
			if c.CUDA.Major == 0 {
				c.CUDA.Major, c.CUDA.Minor = int(cl.NvCCMajor), int(cl.NvCCMinor)
			}
			// With these the server can work out the card's speed itself.
			c.CUDA.MultiProcessorCount = int(cl.MaxComputeUnits)
			c.CUDA.ClockRate = int(cl.MaxClockMHz * 1000)
		}
		c.CUDA.ReqSecs, c.CUDA.ReqInstances = gpuWorkRequest(hi.NvidiaCount, gpuQueued)
	}
	if hi.AtiCount > 0 {
		c.ATI = &CoprocAtiXML{Count: hi.AtiCount, Name: coprocName(hi.AtiName), LocalRAM: hi.AtiMem / (1 << 20)}
		if cl := hi.AtiCL; cl != nil {
			c.ATI.HaveOpenCL = 1
			c.ATI.OpenCL = openCLXML(cl)
			if c.ATI.LocalRAM == 0 {
				c.ATI.LocalRAM = float64(cl.GlobalMem) / (1 << 20)
			}
		}
		if hi.NvidiaCount <= 0 {
			c.ATI.ReqSecs, c.ATI.ReqInstances = gpuWorkRequest(hi.AtiCount, gpuQueued)
		}
	}
	return c
}

// localTimezone is the offset from UTC in seconds, as BOINC's host record has it.
func localTimezone() int {
	_, off := time.Now().Zone()
	return off
}

// clientAppVersions turns app_info.xml into the list sent to the project.
func clientAppVersions(ai *project.AppInfo, hostFlops float64) *ClientAppVersionsXML {
	out := &ClientAppVersionsXML{}
	for _, v := range ai.Versions {
		cpus := v.AvgNCPUs
		if cpus <= 0 {
			cpus = 1
		}
		flops := v.Flops
		if flops <= 0 {
			flops = hostFlops * cpus
		}
		cv := ClientAppVersionXML{AppName: v.AppName, VersionNum: v.VersionNum, Platform: "anonymous", PlanClass: v.PlanClass, AvgNCPUs: cpus, Flops: flops}
		if v.Coproc != nil && v.Coproc.Count > 0 {
			cv.Coproc = &ClientCoprocXML{Type: v.Coproc.Type, Count: v.Coproc.Count}
		}
		out.Versions = append(out.Versions, cv)
	}
	return out
}

// reportedProductName is what projects list under "Model".
const reportedProductName = "athena.org.tr"

// reportedOSName puts the client's identity in front of the operating system
// so a project's host list says what is running there.
func reportedOSName(os string) string {
	const brand = "Iris client - athena.org.tr"
	if strings.TrimSpace(os) == "" {
		return brand
	}
	return brand + " | " + os
}

// hostName is the computer name to report: the one chosen in Settings, else
// the machine's own.
func (e *Engine) hostName() string {
	if e.cfg.HostNameFn != nil {
		if n := strings.TrimSpace(e.cfg.HostNameFn()); n != "" {
			return n
		}
	}
	return localHostname()
}

func localHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// openCLXML is the <coproc_opencl> block of a GPU (OPENCL_DEVICE_PROP::write_xml).
func openCLXML(d *detect.OpenCLDevice) *CoprocOpenCLXML {
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	return &CoprocOpenCLXML{
		Name: d.Name, Vendor: d.Vendor, VendorID: d.VendorID, Available: b2i(d.Available),
		HalfFP: d.HalfFPConfig, SingleFP: d.SingleFPConfig, DoubleFP: d.DoubleFPConfig,
		EndianLittle: b2i(d.EndianLittle), ExecCaps: d.ExecutionCaps, Extensions: d.Extensions,
		GlobalMem: d.GlobalMem, LocalMem: d.LocalMem, MaxClock: d.MaxClockMHz, MaxCUs: d.MaxComputeUnits,
		NvCCMajor: d.NvCCMajor, NvCCMinor: d.NvCCMinor,
		AmdSimdPerCU: d.AmdSimdPerCU, AmdSimdWidth: d.AmdSimdWidth, AmdSimdInstrWidth: d.AmdSimdInstrWidth,
		PlatformVersion: d.PlatformVersion, DeviceVersion: d.DeviceVersion, DriverVersion: d.DriverVersion,
	}
}

// gpuWorkRequest asks for work for every GPU that has nothing queued, the same
// sizing the CPU request uses. Without it the request says "0 seconds of GPU
// work" and a project never offers any.
func gpuWorkRequest(count, queued int) (secs, instances float64) {
	idle := count - queued
	if idle <= 0 {
		return 0, 0
	}
	return float64(idle) * targetBufferSecs, float64(idle)
}

// driverInt encodes "581.42" as 58142, the form CUDA plan classes compare.
func driverInt(v string) int {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int(f*100 + 0.5)
}

// countGPUQueued counts a project's GPU tasks that still occupy a GPU.
func countGPUQueued(results []ResultInfo, url string) int {
	n := 0
	for _, r := range results {
		if r.ProjectURL == url && r.GPU && r.State < 4 {
			n++
		}
	}
	return n
}

// coprocName drops a leading vendor word: project pages already put the
// vendor in front of the model, so "NVIDIA GeForce ..." was listed as
// "NVIDIA NVIDIA GeForce ...".
func coprocName(n string) string {
	n = strings.TrimSpace(n)
	for _, v := range []string{"NVIDIA ", "AMD ", "ATI "} {
		if len(n) > len(v) && strings.EqualFold(n[:len(v)], v) {
			return strings.TrimSpace(n[len(v):])
		}
	}
	return n
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
