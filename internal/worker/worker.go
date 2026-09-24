package worker

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Engine struct {
	state    StateAccessor
	cache    CacheAccessor
	projects ProjectAccessor
	dl       Downloader
	ul       Uploader
	mu       sync.RWMutex
	stop     chan struct{}
	stopping bool
	running  map[string]*exec.Cmd
	cfg      Config

	cacheWarned    map[bool]time.Time
	realAppsWarned time.Time

	// active holds tasks a goroutine is currently starting or running, so a
	// task left in "computing" by an earlier run (nothing active) is put back
	// in the queue instead of sitting there forever.
	active map[string]bool
	// uploading/uploadRetry track output uploads in flight and when a failed
	// one may be tried again.
	uploading   map[string]bool
	uploadRetry map[string]time.Time
	uploadFails map[string]int
	// downloadFails counts failed download attempts per task; a few are
	// retried before the task is given up on.
	downloadFails map[string]int
}

type Config struct {
	MaxConcurrent int
	// MaxConcurrentFn, when set, is consulted every cycle and overrides
	// MaxConcurrent so preference changes apply without a restart.
	MaxConcurrentFn func() int
	DataDir         string
	UserAgent       string
	SlotTimeout     time.Duration
	CheckpointSec   int
	MaxDiskUsage    int64

	// RealAppsEnabledFn, when set and returning true, lets the engine launch
	// a task's real downloaded application (see ResultSnapshot.Files'
	// MainProgram flag) instead of only the legacy demo-stub executable
	// names. This is unsandboxed — a downloaded project binary runs with
	// Iris's own privileges, same as the reference BOINC client — so it
	// defaults to off (nil or a func returning false) until the person
	// explicitly opts in from Settings.
	RealAppsEnabledFn func() bool
}

type StateAccessor interface {
	GetResults() []ResultSnapshot
	UpdateResult(name string, state int, fracDone float64, cpuTime float64, exitStatus int)
	SetSlotPath(name, slotPath string)
	RemoveResult(name string)
	GetTaskMode() int
	GetDiskUsage() int64
	GetDiskQuota() int64
	SetDiskUsage(v int64)
	UpdateStats(success bool, cpuTime, gpuTime, credit float64)
	// RecordTaskDay counts a finished task against its project's daily total,
	// the data behind the assistant's "how many X tasks per day" answers.
	RecordTaskDay(projectURL string, success bool, cpuTime float64)
	AddMessage(body, project string, pri int)
	Save()
	// IsSuspended reports a task's current suspend flag, polled while it runs
	// so a suspend/resume click reaches an already-started OS process instead
	// of only affecting which tasks the next cycle picks to start.
	IsSuspended(name string) bool
	// SetOutputs records what a finished task produced (which output files
	// exist, their sizes and MD5s).
	SetOutputs(name string, outs []OutputRef)
	// MarkOutputUploaded records one uploaded output and reports whether every
	// output is now uploaded, i.e. the result can be reported.
	MarkOutputUploaded(name, file string) bool
}

type CacheAccessor interface {
	// Full reports whether the disk share of one class of work (GPU or CPU)
	// is used up.
	Full(gpu bool) bool
	AllocSlot(gpu bool) (string, error)
	FreeSlot(slot string) error
	SlotDir(gpu bool) string
	ProjectDir(gpu bool) string
}

type ProjectAccessor interface {
	GetAuthInfo(projectURL string) (auth, name string, ok bool)
}

type ResultSnapshot struct {
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
	AppName       string
	// EstRuntime is the expected seconds of work (0 = unknown).
	EstRuntime float64
	Files      []FileRef
	Suspended  int
	Outputs    []OutputRef
	MaxElapSec float64
	// ResourceShare is the owning project's configured share (BOINC's
	// convention: 100 if never set), used to divide free slots fairly
	// across multiple attached projects instead of a FIFO that can starve
	// every project but whichever queued its tasks first.
	ResourceShare float64
}

// OutputRef is one file a task must produce and upload (see
// state.OutputFile). Name is the physical name its upload certificate was
// signed for; the application writes it as OpenName in its slot directory.
type OutputRef struct {
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

type FileRef struct {
	// OpenName is the logical name the application opens the file by.
	OpenName string
	Name     string
	URL      string
	NBytes   float64
	MD5      string
	// MainProgram marks the one file that is the real application executable
	// downloaded from the project, as opposed to an input or library file.
	MainProgram bool
}

type Downloader interface {
	DownloadFile(projectURL, filename, destPath string) error
	DownloadFileByURL(url, destPath string) error
}

type Uploader interface {
	UploadFile(projectURL, filePath string) error
}

// ResultUploader uploads a task's output file to the project's file upload
// handler with its signed certificate. An Uploader that also implements it is
// used for every task that declares outputs.
type ResultUploader interface {
	UploadResultFile(projectURL string, o OutputRef, path string) error
}

// PermanentError is implemented by upload errors that will never succeed on a
// retry (the server rejected the file for good).
type PermanentError interface {
	IsPermanent() bool
}

const (
	StateNew      = 0
	StateDownload = 1
	StateCompute  = 2
	StateReady    = 4
	StateError    = 5

	ExitOK            = 0
	ExitComputeError  = 1
	ExitNeedAbort     = 64
	ExitMaxReject     = 191
	ExitAborted       = 192
	ExitSwapMissing   = 193
	ExitUnstartable   = 194
	ExitBadTempDir    = 195
	ExitClientIdle    = 196
	ExitClientExiting = 197
	ExitBadWU         = 198
	ExitExceeded      = 199
	ExitAbortClaimed  = 200

	// BOINC's own client error numbers (lib/error_numbers.h), reported to the
	// project as the result's exit status.
	ExitFileTooBig     = -131 // ERR_FILE_TOO_BIG
	ExitFileMissing    = -163 // ERR_FILE_MISSING
	ExitResultDownload = -186 // ERR_RESULT_DOWNLOAD
	ExitResultUpload   = -187 // ERR_RESULT_UPLOAD
)

func NewEngine(state StateAccessor, cache CacheAccessor, projects ProjectAccessor, dl Downloader, ul Uploader, cfg Config) *Engine {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = runtime.NumCPU()
	}
	if cfg.SlotTimeout == 0 {
		cfg.SlotTimeout = 24 * time.Hour
	}
	if cfg.CheckpointSec <= 0 {
		cfg.CheckpointSec = 600
	}
	return &Engine{
		state:    state,
		cache:    cache,
		projects: projects,
		dl:       dl,
		ul:       ul,
		stop:     make(chan struct{}),
		running:  make(map[string]*exec.Cmd),
		cfg:      cfg,

		active:      make(map[string]bool),
		uploading:   make(map[string]bool),
		uploadRetry: make(map[string]time.Time),
		uploadFails: make(map[string]int),

		downloadFails: make(map[string]int),
	}
}

func (e *Engine) Start() {
	log.Printf("[Worker] Starting, max_concurrent=%d", e.cfg.MaxConcurrent)
	go e.loop()
}

func (e *Engine) Stop() {
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return
	}
	e.stopping = true
	e.mu.Unlock()
	close(e.stop)
	log.Println("[Worker] Stopping, waiting for tasks...")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(120 * time.Second)
	for {
		e.mu.RLock()
		n := len(e.running)
		e.mu.RUnlock()
		if n == 0 {
			log.Println("[Worker] All tasks finished.")
			return
		}
		select {
		case <-ticker.C:
			log.Printf("[Worker] Waiting for %d tasks to finish...", n)
		case <-timeout:
			e.mu.Lock()
			for name, cmd := range e.running {
				terminateProcess(cmd)
				log.Printf("[Worker] Force killed %s", name)
			}
			e.running = make(map[string]*exec.Cmd)
			e.mu.Unlock()
			log.Println("[Worker] Force stopped remaining tasks.")
			return
		}
	}
}

func (e *Engine) loop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.runCycle()
		case <-e.stop:
			return
		}
	}
}

func (e *Engine) runCycle() {
	e.mu.RLock()
	stopping := e.stopping
	e.mu.RUnlock()
	if stopping {
		return
	}

	taskMode := e.state.GetTaskMode()
	diskUsage := e.state.GetDiskUsage()
	diskQuota := e.state.GetDiskQuota()
	maxConcurrent := e.cfg.MaxConcurrent
	if e.cfg.MaxConcurrentFn != nil {
		if n := e.cfg.MaxConcurrentFn(); n > 0 {
			maxConcurrent = n
		}
	}

	results := e.state.GetResults()
	running := 0

	for _, r := range results {
		if (r.State == StateCompute || r.State == StateDownload) && !e.isActive(r.Name) {
			// Left over from an earlier run of the client: nothing is computing
			// it, so queue it again rather than leave it stuck.
			log.Printf("[Worker] Task %s was interrupted earlier, queueing it again", r.Name)
			e.state.UpdateResult(r.Name, StateNew, r.FracDone, r.CPUTime, 0)
			continue
		}
		if r.State == StateCompute && r.Suspended == 0 {
			running++
		}
	}

	e.startPendingUploads(results)

	if taskMode == 3 {
		return
	}
	if diskQuota > 0 && diskUsage >= diskQuota {
		log.Printf("[Worker] Disk quota reached (%d/%d), skipping downloads", diskUsage, diskQuota)
		return
	}
	slots := maxConcurrent - running
	if slots <= 0 {
		return
	}

	var eligible []ResultSnapshot
	for _, r := range results {
		if r.State != StateNew || r.Suspended != 0 {
			continue
		}
		if e.cache.Full(r.GPU) {
			e.warnCacheFull(r.GPU)
			continue // the other class of work may still have room
		}
		eligible = append(eligible, r)
	}

	for _, r := range pickTasksToStart(eligible, slots) {
		e.setActive(r.Name, true)
		go func(r ResultSnapshot) {
			defer e.setActive(r.Name, false)
			e.startTask(r)
		}(r)
	}
}

// pickTasksToStart chooses up to `slots` more results to start from
// eligible (already filtered to queued, not suspended, and not blocked by a
// full cache), dividing them fairly across projects by resource share
// instead of a flat FIFO — otherwise one project with a deep queue can
// starve every other attached project indefinitely, no matter how modest
// its own configured share. A project's own arrival order is preserved
// among its own tasks.
func pickTasksToStart(eligible []ResultSnapshot, slots int) []ResultSnapshot {
	if slots <= 0 || len(eligible) == 0 {
		return nil
	}

	type queue struct {
		share float64
		items []ResultSnapshot
	}
	order := make([]string, 0, 4)
	byProject := map[string]*queue{}
	for _, r := range eligible {
		q, ok := byProject[r.ProjectURL]
		if !ok {
			share := r.ResourceShare
			if share <= 0 {
				share = 100 // BOINC's own default when a project never sets one
			}
			q = &queue{share: share}
			byProject[r.ProjectURL] = q
			order = append(order, r.ProjectURL)
		}
		q.items = append(q.items, r)
	}
	if len(order) == 1 {
		// The common case: nothing to divide, just cap at what's queued.
		q := byProject[order[0]]
		if slots > len(q.items) {
			slots = len(q.items)
		}
		return append([]ResultSnapshot(nil), q.items[:slots]...)
	}

	type alloc struct {
		url  string
		want int
		frac float64
	}
	totalShare := 0.0
	for _, url := range order {
		totalShare += byProject[url].share
	}
	allocs := make([]alloc, len(order))
	assigned := 0
	for i, url := range order {
		q := byProject[url]
		exact := float64(slots) * q.share / totalShare
		want := int(exact)
		if want > len(q.items) {
			want = len(q.items)
		}
		allocs[i] = alloc{url: url, want: want, frac: exact - float64(int(exact))}
		assigned += want
	}

	// Largest-remainder method: hand out any leftover slots to the projects
	// closest to their next whole share first, then keep going round-robin
	// for whatever remains unclaimed (e.g. a low-share project's queue ran
	// dry, freeing its slots up for everyone else).
	remaining := slots - assigned
	sort.SliceStable(allocs, func(i, j int) bool { return allocs[i].frac > allocs[j].frac })
	for remaining > 0 {
		progress := false
		for i := range allocs {
			if remaining <= 0 {
				break
			}
			a := &allocs[i]
			if a.want < len(byProject[a.url].items) {
				a.want++
				remaining--
				progress = true
			}
		}
		if !progress {
			break // no project has any more queued work to give slots to
		}
	}

	var picked []ResultSnapshot
	for _, a := range allocs {
		picked = append(picked, byProject[a.url].items[:a.want]...)
	}
	return picked
}

func (e *Engine) isActive(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.active[name]
}

func (e *Engine) setActive(name string, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if on {
		e.active[name] = true
	} else {
		delete(e.active, name)
	}
}

// warnCacheFull logs a full cache at most once every ten minutes per class.
func (e *Engine) warnCacheFull(gpu bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cacheWarned == nil {
		e.cacheWarned = map[bool]time.Time{}
	}
	if time.Since(e.cacheWarned[gpu]) < 10*time.Minute {
		return
	}
	e.cacheWarned[gpu] = time.Now()
	kind := "CPU"
	if gpu {
		kind = "GPU"
	}
	log.Printf("[Worker] %s cache is full, not starting more %s work", kind, kind)
}

func (e *Engine) startTask(r ResultSnapshot) {
	log.Printf("[Worker] Starting task %s (gpu=%v, files=%d)", r.Name, r.GPU, len(r.Files))

	if r.Slot == "" {
		slot, err := e.cache.AllocSlot(r.GPU)
		if err != nil {
			log.Printf("[Worker] No slot for %s: %v", r.Name, err)
			e.state.UpdateResult(r.Name, StateError, 0, 0, 0)
			return
		}
		r.Slot = slot
		e.state.SetSlotPath(r.Name, slot)
	}

	if err := os.MkdirAll(r.Slot, 0o755); err != nil {
		log.Printf("[Worker] Cannot create slot %s: %v", r.Slot, err)
		e.state.UpdateResult(r.Name, StateError, 0, 0, 0)
		return
	}

	ckPath := filepath.Join(r.Slot, "checkpoint")
	if fileExists(ckPath) {
		log.Printf("[Worker] Found checkpoint for %s", r.Name)
		progress := readProgress(r.Slot)
		if progress > 0 {
			e.state.UpdateResult(r.Name, StateDownload, progress, 0, 0)
			log.Printf("[Worker] Resuming %s from checkpoint (progress=%.2f)", r.Name, progress)
		}
	}

	e.state.UpdateResult(r.Name, StateDownload, r.FracDone, 0, 0)

	if err := e.downloadFiles(r); err != nil {
		log.Printf("[Worker] Download failed for %s: %v", r.Name, err)
		e.mu.Lock()
		e.downloadFails[r.Name]++
		tries := e.downloadFails[r.Name]
		e.mu.Unlock()
		if tries < 3 {
			e.state.AddMessage(fmt.Sprintf("Download failed for %s (attempt %d of 3), trying again: %v", r.Name, tries, err), r.ProjectURL, 2)
			e.state.UpdateResult(r.Name, StateNew, 0, 0, 0)
			return
		}
		e.state.AddMessage(fmt.Sprintf("Download failed for %s: %v", r.Name, err), r.ProjectURL, 3)
		e.state.UpdateResult(r.Name, StateError, 0, 0, ExitResultDownload)
		return
	}

	exePath := e.findExecutable(r)
	if exePath == "" {
		log.Printf("[Worker] No executable found for %s", r.Name)
		e.state.UpdateResult(r.Name, StateError, 0, 0, 0)
		return
	}

	if isMainProgramFile(r, exePath) {
		e.writeInitDataFile(r)
	}

	log.Printf("[Worker] Task %s ready, launching %s", r.Name, exePath)
	e.state.UpdateResult(r.Name, StateCompute, r.FracDone, 0, 0)
	e.runApp(r, exePath)
}

// isMainProgramFile reports whether exePath is the flagged real-application
// executable, as opposed to one of Iris's own legacy stub filenames.
func isMainProgramFile(r ResultSnapshot, exePath string) bool {
	base := filepath.Base(exePath)
	for _, f := range r.Files {
		if f.MainProgram && f.Name == base {
			return true
		}
	}
	return false
}

// writeInitDataFile writes the slot-directory file a real BOINC application
// reads on boinc_init() (project/task identity, checkpoint period, etc — see
// https://github.com/BOINC/boinc/blob/master/lib/app_ipc.h, INIT_DATA_FILE).
// Iris has no shared-memory message channel to the app (that's the "no
// sandbox" scope this feature deliberately stayed inside — see README), so
// an app that insists on it will fall back to the BOINC API's own
// documented "standalone" behavior instead of receiving live suspend/resume
// or fraction_done polling through that channel; Iris still honors suspend
// at the OS process level (see pauseProcess) and reads fraction_done.txt
// exactly as it always has for the demo stub.
func (e *Engine) writeInitDataFile(r ResultSnapshot) {
	authenticator, _, _ := e.projects.GetAuthInfo(r.ProjectURL)
	fp := filepath.Join(r.Slot, "init_data.xml")
	if err := os.WriteFile(fp, buildInitDataXML(r, authenticator, e.cfg.DataDir, e.cfg.CheckpointSec), 0o644); err != nil {
		log.Printf("[Worker] Cannot write init_data.xml for %s: %v", r.Name, err)
	}
}

func buildInitDataXML(r ResultSnapshot, authenticator, boincDir string, checkpointSec int) []byte {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var b strings.Builder
	b.WriteString("<app_init_data>\n")
	fmt.Fprintf(&b, "  <app_version>%d</app_version>\n", r.AppVersionNum)
	if r.AppName != "" {
		fmt.Fprintf(&b, "  <app_name>%s</app_name>\n", esc(r.AppName))
	}
	// Iris keeps a task's input and application files directly in its own
	// slot directory rather than stock BOINC's shared per-project directory
	// full of symlinks, so project_dir and slot are the same path here.
	fmt.Fprintf(&b, "  <project_dir>%s</project_dir>\n", esc(r.Slot))
	fmt.Fprintf(&b, "  <boinc_dir>%s</boinc_dir>\n", esc(boincDir))
	if authenticator != "" {
		fmt.Fprintf(&b, "  <authenticator>%s</authenticator>\n", esc(authenticator))
	}
	fmt.Fprintf(&b, "  <wu_name>%s</wu_name>\n", esc(r.WuName))
	fmt.Fprintf(&b, "  <result_name>%s</result_name>\n", esc(r.Name))
	fmt.Fprintf(&b, "  <slot>%s</slot>\n", esc(r.Slot))
	fmt.Fprintf(&b, "  <client_pid>%d</client_pid>\n", os.Getpid())
	fmt.Fprintf(&b, "  <wu_cpu_time>%.6f</wu_cpu_time>\n", r.CPUTime)
	if checkpointSec <= 0 {
		checkpointSec = 600
	}
	fmt.Fprintf(&b, "  <checkpoint_period>%d</checkpoint_period>\n", checkpointSec)
	fmt.Fprintf(&b, "  <fraction_done_start>%.6f</fraction_done_start>\n", r.FracDone)
	b.WriteString("  <fraction_done_end>1.000000</fraction_done_end>\n")
	if r.GPU {
		// Always device 0: Iris does not track which of possibly several
		// installed GPUs is free the way the reference client's coproc
		// scheduler does, so multi-GPU hosts only ever offer up the first
		// device. The overwhelmingly common single-GPU case is unaffected.
		b.WriteString("  <gpu_device_num>0</gpu_device_num>\n")
		b.WriteString("  <gpu_opencl_dev_index>0</gpu_opencl_dev_index>\n")
	}
	b.WriteString("</app_init_data>\n")
	return []byte(b.String())
}

func (e *Engine) downloadFiles(r ResultSnapshot) error {
	for _, f := range r.Files {
		if !safeFileName(f.Name) {
			return fmt.Errorf("the project sent an unsafe file name %q", f.Name)
		}
		dest := filepath.Join(r.Slot, f.Name)
		if f.MD5 != "" && fileExists(dest) {
			if ok, _ := md5Matches(dest, f.MD5); ok {
				log.Printf("[Worker] %s already present and verified", f.Name)
				if err := linkOpenName(r.Slot, f); err != nil {
					return err
				}
				continue
			}
		}
		log.Printf("[Worker] Downloading %s (%.0f bytes)", f.Name, f.NBytes)
		if f.URL != "" {
			if err := e.dl.DownloadFileByURL(f.URL, dest); err != nil {
				return fmt.Errorf("download %s: %w", f.Name, err)
			}
		} else {
			if err := e.dl.DownloadFile(r.ProjectURL, f.Name, dest); err != nil {
				return fmt.Errorf("download %s: %w", f.Name, err)
			}
		}
		if f.MD5 != "" {
			ok, err := md5Matches(dest, f.MD5)
			if err != nil {
				return fmt.Errorf("verify %s: %w", f.Name, err)
			}
			if !ok {
				os.Remove(dest)
				return fmt.Errorf("verify %s: MD5 mismatch", f.Name)
			}
		}
		if f.MainProgram && runtime.GOOS != "windows" {
			// Downloaded files land with the download client's default
			// permissions, never the execute bit; a real project executable
			// needs it or exec.Command's Start() just fails with "permission
			// denied".
			if err := os.Chmod(dest, 0o755); err != nil {
				return fmt.Errorf("chmod %s: %w", f.Name, err)
			}
		}
		if err := linkOpenName(r.Slot, f); err != nil {
			return err
		}
	}
	return nil
}

// linkOpenName makes a downloaded file available under the logical name the
// application opens it by (BOINC's <open_name>), which differs from the
// file's physical name. A real BOINC client uses a link; a plain copy is the
// same to the application and works on every platform.
func linkOpenName(slot string, f FileRef) error {
	if f.OpenName == "" || f.OpenName == f.Name || !safeFileName(f.OpenName) {
		return nil
	}
	src := filepath.Join(slot, f.Name)
	dst := filepath.Join(slot, f.OpenName)
	if _, err := os.Stat(dst); err == nil {
		os.Remove(dst)
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", f.Name, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", f.OpenName, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", f.OpenName, err)
	}
	if f.MainProgram {
		out.Chmod(0o755)
	}
	return out.Close()
}

func md5Matches(path, want string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), strings.TrimSpace(want)), nil
}

// findExecutable returns the program to launch for a task. When a project
// sent a real app_version (see ResultSnapshot.Files' MainProgram flag), that
// downloaded, unsandboxed executable is used only once the person has turned
// on the experimental "real applications" preference; otherwise Iris falls
// back to its own legacy stub filenames (used by the demo host and, on a
// real project, will simply not exist — findExecutable then reports failure
// rather than silently stalling).
func (e *Engine) findExecutable(r ResultSnapshot) string {
	var mainProgram string
	for _, f := range r.Files {
		if f.MainProgram {
			mainProgram = f.Name
			break
		}
	}
	if mainProgram != "" {
		p := filepath.Join(r.Slot, mainProgram)
		if e.realAppsEnabled() {
			if fileExists(p) {
				return p
			}
		} else {
			e.warnRealAppsDisabled(r.Name)
		}
	}

	if runtime.GOOS == "windows" {
		candidates := []string{"app.exe", "main.exe"}
		for _, c := range candidates {
			p := filepath.Join(r.Slot, c)
			if fileExists(p) {
				return p
			}
		}
	} else {
		candidates := []string{"app", "main"}
		for _, c := range candidates {
			p := filepath.Join(r.Slot, c)
			if fileExists(p) {
				return p
			}
		}
	}
	return ""
}

func (e *Engine) realAppsEnabled() bool {
	return e.cfg.RealAppsEnabledFn != nil && e.cfg.RealAppsEnabledFn()
}

// warnRealAppsDisabled logs, at most once every ten minutes (the same
// throttle warnCacheFull uses for a full cache), that a task is stuck
// because it needs the experimental real-application toggle.
func (e *Engine) warnRealAppsDisabled(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Since(e.realAppsWarned) < 10*time.Minute {
		return
	}
	e.realAppsWarned = time.Now()
	log.Printf("[Worker] %s has a real application ready but the experimental \"run real applications\" setting is off (Settings > global preferences); the task cannot start", name)
}

// gpuEnv is a best-effort compatibility shim for GPU apps that read the
// vendor's own standard environment variable for device selection instead
// of (or in addition to) init_data.xml's gpu_device_num — always device 0,
// the same single-GPU-host assumption buildInitDataXML documents.
func gpuEnv() []string {
	return []string{"CUDA_VISIBLE_DEVICES=0", "GPU_DEVICE_ORDINAL=0"}
}

func (e *Engine) runApp(r ResultSnapshot, exePath string) {
	start := time.Now()

	var cmd *exec.Cmd
	if r.CmdLine != "" {
		args := parseCmdLine(r.CmdLine)
		cmd = exec.Command(exePath, args...)
	} else {
		cmd = exec.Command(exePath)
	}
	cmd.Dir = r.Slot
	if r.GPU {
		cmd.Env = append(os.Environ(), gpuEnv()...)
	}

	stdoutPath := filepath.Join(r.Slot, "stdout.txt")
	stderrPath := filepath.Join(r.Slot, "stderr.txt")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		log.Printf("[Worker] Cannot create stdout for %s: %v", r.Name, err)
	}
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		log.Printf("[Worker] Cannot create stderr for %s: %v", r.Name, err)
	}
	if stdoutFile != nil {
		cmd.Stdout = stdoutFile
	}
	if stderrFile != nil {
		cmd.Stderr = stderrFile
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[Worker] Failed to start %s: %v", r.Name, err)
		if stdoutFile != nil {
			stdoutFile.Close()
		}
		if stderrFile != nil {
			stderrFile.Close()
		}
		e.state.UpdateResult(r.Name, StateError, 0, 0, 0)
		return
	}
	if stdoutFile != nil {
		stdoutFile.Close()
	}
	if stderrFile != nil {
		stderrFile.Close()
	}

	e.mu.Lock()
	e.running[r.Name] = cmd
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.running, r.Name)
		e.mu.Unlock()
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	checkpointTicker := time.NewTicker(time.Duration(e.cfg.CheckpointSec) * time.Second)
	defer checkpointTicker.Stop()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// suspendTicker polls the (GUI-settable) suspend flag and pauses/resumes
	// the real OS process accordingly. This is Iris's whole suspend/resume
	// mechanism for an unsandboxed app — no shared-memory channel, so an
	// app's own boinc_time_to_checkpoint()/boinc_is_standalone() never sees
	// a cooperative suspend request the way it would under the reference
	// client; the process is simply stopped at the OS level (SIGSTOP/SIGCONT,
	// or the Windows NtSuspendProcess/NtResumeProcess equivalent) and
	// resumed later, exactly where it left off.
	suspendTicker := time.NewTicker(2 * time.Second)
	defer suspendTicker.Stop()
	var paused bool
	var pausedAccum time.Duration
	var pauseStart time.Time
	effectiveElapsed := func() time.Duration {
		d := time.Since(start) - pausedAccum
		if paused {
			d -= time.Since(pauseStart)
		}
		if d < 0 {
			d = 0
		}
		return d
	}
	resumeIfPaused := func() {
		if !paused {
			return
		}
		if err := resumeProcess(cmd); err != nil {
			log.Printf("[Worker] Failed to resume %s before stopping it: %v", r.Name, err)
		}
		pausedAccum += time.Since(pauseStart)
		paused = false
	}

	for {
		select {
		case err := <-done:
			elapsed := effectiveElapsed().Seconds()
			exitCode := classifyExit(err)

			e.mu.RLock()
			wasStopping := e.stopping
			e.mu.RUnlock()

			if wasStopping {
				log.Printf("[Worker] Task %s interrupted during shutdown", r.Name)
				e.writeCheckpoint(r)
				e.state.UpdateResult(r.Name, StateNew, readProgress(r.Slot), elapsed, 0)
				return
			}

			switch exitCode {
			case ExitOK:
				progress := readProgress(r.Slot)
				if code, msg := e.collectOutputs(r); code != 0 {
					log.Printf("[Worker] Task %s finished but its output is unusable: %s", r.Name, msg)
					e.state.AddMessage(fmt.Sprintf("Task %s finished but %s", r.Name, msg), r.ProjectURL, 3)
					e.state.UpdateResult(r.Name, StateError, 0, elapsed, code)
					e.state.UpdateStats(false, elapsed, 0, 0)
					e.state.RecordTaskDay(r.ProjectURL, false, elapsed)
					return
				}
				e.state.UpdateResult(r.Name, StateReady, progress, elapsed, exitCode)
				e.state.UpdateStats(true, elapsed, 0, 0)
				e.state.RecordTaskDay(r.ProjectURL, true, elapsed)
				log.Printf("[Worker] Task %s completed OK (%.1fs, progress=%.2f)", r.Name, elapsed, progress)

			case ExitNeedAbort:
				log.Printf("[Worker] Task %s requests abort (exit %d)", r.Name, exitCode)
				e.state.UpdateResult(r.Name, StateError, 0, elapsed, exitCode)
				e.state.UpdateStats(false, elapsed, 0, 0)
				e.state.RecordTaskDay(r.ProjectURL, false, elapsed)

			case ExitClientExiting:
				if wasStopping {
					log.Printf("[Worker] Task %s paused for shutdown (exit %d)", r.Name, exitCode)
					e.writeCheckpoint(r)
					e.state.UpdateResult(r.Name, StateNew, readProgress(r.Slot), elapsed, 0)
				} else {
					log.Printf("[Worker] Task %s requests client exit unexpectedly (exit %d)", r.Name, exitCode)
					e.state.UpdateResult(r.Name, StateError, 0, elapsed, exitCode)
					e.state.UpdateStats(false, elapsed, 0, 0)
					e.state.RecordTaskDay(r.ProjectURL, false, elapsed)
				}

			default:
				log.Printf("[Worker] Task %s failed with exit code %d (%.1fs)", r.Name, exitCode, elapsed)
				e.state.UpdateResult(r.Name, StateError, 0, elapsed, exitCode)
				e.state.UpdateStats(false, elapsed, 0, 0)
				e.state.RecordTaskDay(r.ProjectURL, false, elapsed)
			}
			return

		case <-checkpointTicker.C:
			e.writeCheckpoint(r)
			progress := readProgress(r.Slot)
			elapsed := time.Since(start).Seconds()
			e.state.UpdateResult(r.Name, StateCompute, progress, elapsed, 0)

		case <-ticker.C:
			elapsed := effectiveElapsed()
			maxElap := e.cfg.SlotTimeout
			if r.MaxElapSec > 0 && time.Duration(r.MaxElapSec*float64(time.Second)) < maxElap {
				maxElap = time.Duration(r.MaxElapSec * float64(time.Second))
			}
			if elapsed > maxElap {
				resumeIfPaused()
				terminateProcess(cmd)
				e.state.UpdateResult(r.Name, StateError, 0, maxElap.Seconds(), ExitExceeded)
				log.Printf("[Worker] Task %s timed out (%.1fs)", r.Name, maxElap.Seconds())
				return
			}
			if paused {
				continue // don't report bogus progress climbing while the process isn't actually running
			}
			progress := readProgress(r.Slot)
			frac := progress
			if frac < elapsed.Seconds()/maxElap.Seconds() {
				frac = elapsed.Seconds() / maxElap.Seconds()
			}
			if progress <= 0 && r.EstRuntime > 0 {
				// The app reports no progress (it has no shared-memory channel
				// to Iris), so estimate it from the project's own work estimate
				// like the reference client does, not from the 24 h time limit.
				frac = elapsed.Seconds() / r.EstRuntime
			}
			if frac > 0.99 {
				frac = 0.99
			}
			e.state.UpdateResult(r.Name, StateCompute, frac, elapsed.Seconds(), 0)

		case <-suspendTicker.C:
			suspended := e.state.IsSuspended(r.Name)
			switch {
			case suspended && !paused:
				if err := pauseProcess(cmd); err != nil {
					log.Printf("[Worker] Failed to suspend %s: %v", r.Name, err)
					continue
				}
				paused = true
				pauseStart = time.Now()
				log.Printf("[Worker] Suspended %s", r.Name)
			case !suspended && paused:
				resumeIfPaused()
				log.Printf("[Worker] Resumed %s", r.Name)
			}

		case <-e.stop:
			log.Printf("[Worker] Stopping task %s", r.Name)
			resumeIfPaused()
			terminateProcess(cmd)
			select {
			case <-time.After(30 * time.Second):
				cmd.Process.Kill()
			case <-done:
			}
			e.writeCheckpoint(r)
			e.state.UpdateResult(r.Name, StateNew, readProgress(r.Slot), effectiveElapsed().Seconds(), 0)
			return
		}
	}
}

func (e *Engine) writeCheckpoint(r ResultSnapshot) {
	if r.Slot == "" {
		return
	}
	progress := readProgress(r.Slot)
	fp := filepath.Join(r.Slot, "checkpoint")
	os.WriteFile(fp, []byte(fmt.Sprintf("%.6f", progress)), 0o644)
}

// safeFileName reports whether a name that came from a project server is a
// plain file name; anything with a path in it could otherwise make Iris read
// or write outside the task's slot.
func safeFileName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, "/\\\x00")
}

// outputPath is where a task wrote one of its output files: under its
// logical (open) name in the slot, or failing that its physical name.
func outputPath(slot string, o OutputRef) string {
	if o.OpenName != "" && safeFileName(o.OpenName) {
		if p := filepath.Join(slot, o.OpenName); fileExists(p) {
			return p
		}
	}
	return filepath.Join(slot, o.Name)
}

// collectOutputs runs when a task exits cleanly. A task that declares output
// files must have produced them (unless optional) within their size limit;
// otherwise it did not really succeed, and reporting it as done would earn
// nothing. It records each output's size and MD5 for the report and returns a
// non-zero BOINC error code with an explanation when the result is unusable.
// A task without declared outputs (the demo stub) takes the legacy path.
func (e *Engine) collectOutputs(r ResultSnapshot) (int, string) {
	if len(r.Outputs) == 0 {
		e.uploadLegacyOutputs(r)
		return 0, ""
	}
	outs := make([]OutputRef, len(r.Outputs))
	copy(outs, r.Outputs)
	for i := range outs {
		o := &outs[i]
		p := outputPath(r.Slot, *o)
		if !fileExists(p) {
			if o.Optional {
				continue
			}
			return ExitFileMissing, fmt.Sprintf("the output file %s was not produced", o.Name)
		}
		size, sum, err := fileDigest(p)
		if err != nil {
			return ExitFileMissing, fmt.Sprintf("the output file %s cannot be read: %v", o.Name, err)
		}
		if o.MaxNBytes > 0 && float64(size) > o.MaxNBytes {
			return ExitFileTooBig, fmt.Sprintf("the output file %s is too large (%d bytes, limit %.0f)", o.Name, size, o.MaxNBytes)
		}
		o.Present, o.NBytes, o.MD5 = true, float64(size), sum
	}
	e.state.SetOutputs(r.Name, outs)
	return 0, ""
}

func fileDigest(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := md5.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// uploadLegacyOutputs is the pre-existing behaviour for tasks that declare no
// outputs: the demo stub's log files go to the plain upload endpoint.
func (e *Engine) uploadLegacyOutputs(r ResultSnapshot) {
	if e.ul == nil {
		return
	}
	for _, name := range []string{"stdout.txt", "stderr.txt", "fraction_done.txt"} {
		fp := filepath.Join(r.Slot, name)
		if !fileExists(fp) {
			continue
		}
		if err := e.ul.UploadFile(r.ProjectURL, fp); err != nil {
			log.Printf("[Worker] Upload %s failed: %v", name, err)
		} else {
			log.Printf("[Worker] Uploaded %s", name)
		}
	}
}

// startPendingUploads begins uploading the outputs of every finished task that
// still has some, with a growing pause after each failure (the reference
// client's persistent file transfers behave the same way).
func (e *Engine) startPendingUploads(results []ResultSnapshot) {
	ru, ok := e.ul.(ResultUploader)
	if !ok {
		return
	}
	for _, r := range results {
		if r.State != StateReady {
			continue
		}
		pending := false
		for _, o := range r.Outputs {
			if o.Present && !o.Uploaded {
				pending = true
			}
		}
		if !pending {
			continue
		}
		e.mu.Lock()
		busy := e.uploading[r.Name] || time.Now().Before(e.uploadRetry[r.Name])
		if !busy {
			e.uploading[r.Name] = true
		}
		e.mu.Unlock()
		if busy {
			continue
		}
		go e.uploadResult(ru, r)
	}
}

func (e *Engine) uploadResult(ru ResultUploader, r ResultSnapshot) {
	defer func() {
		e.mu.Lock()
		delete(e.uploading, r.Name)
		e.mu.Unlock()
	}()
	for _, o := range r.Outputs {
		if !o.Present || o.Uploaded {
			continue
		}
		path := outputPath(r.Slot, o)
		log.Printf("[Worker] Uploading %s of %s (%.0f bytes)", o.Name, r.Name, o.NBytes)
		if err := ru.UploadResultFile(r.ProjectURL, o, path); err != nil {
			if pe, ok := err.(PermanentError); ok && pe.IsPermanent() {
				log.Printf("[Worker] Upload of %s refused for good: %v", o.Name, err)
				e.state.AddMessage(fmt.Sprintf("Upload of %s for %s was refused: %v", o.Name, r.Name, err), r.ProjectURL, 3)
				e.state.UpdateResult(r.Name, StateError, r.FracDone, r.CPUTime, ExitResultUpload)
				return
			}
			e.mu.Lock()
			e.uploadFails[r.Name]++
			delay := time.Duration(e.uploadFails[r.Name]) * time.Minute
			if delay > 30*time.Minute {
				delay = 30 * time.Minute
			}
			e.uploadRetry[r.Name] = time.Now().Add(delay)
			e.mu.Unlock()
			log.Printf("[Worker] Upload of %s failed, retrying in %s: %v", o.Name, delay, err)
			e.state.AddMessage(fmt.Sprintf("Upload of %s for %s failed, will retry in %s: %v", o.Name, r.Name, delay, err), r.ProjectURL, 2)
			return
		}
		log.Printf("[Worker] Uploaded %s of %s", o.Name, r.Name)
		if e.state.MarkOutputUploaded(r.Name, o.Name) {
			log.Printf("[Worker] All outputs of %s are uploaded; it is ready to report", r.Name)
			e.state.AddMessage(fmt.Sprintf("Finished uploading %s; it will be reported at the next contact with the project", r.Name), r.ProjectURL, 1)
			e.state.Save()
		}
	}
}

func readProgress(slotDir string) float64 {
	fp := filepath.Join(slotDir, "fraction_done.txt")
	data, err := os.ReadFile(fp)
	if err != nil {
		return 0
	}
	var v float64
	fmt.Sscanf(string(data), "%f", &v)
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return v
}

func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", cmd.Process.Pid)).Run()
		cmd.Process.Kill()
		return
	}
	cmd.Process.Signal(syscall.SIGTERM)
}

func classifyExit(err error) int {
	if err == nil {
		return ExitOK
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		switch {
		case code == 0:
			return ExitOK
		case code >= 64 && code <= 200:
			return code
		default:
			return ExitComputeError
		}
	}
	return ExitComputeError
}

func parseCmdLine(s string) []string {
	var args []string
	var current string
	inQuote := false
	for _, c := range s {
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ' ' && !inQuote:
			if current != "" {
				args = append(args, current)
				current = ""
			}
		default:
			current += string(c)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}

// Cleanup is called at shutdown. It deliberately removes nothing: finished
// tasks - including ones whose upload or report is still outstanding - are the
// user's completed work and must survive a restart until the project has
// acknowledged them (the scheduler engine frees their slot then).
func (e *Engine) Cleanup() {}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
