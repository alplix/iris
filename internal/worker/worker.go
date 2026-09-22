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
	Files         []FileRef
	Suspended     int
	MaxElapSec    float64
	// ResourceShare is the owning project's configured share (BOINC's
	// convention: 100 if never set), used to divide free slots fairly
	// across multiple attached projects instead of a FIFO that can starve
	// every project but whichever queued its tasks first.
	ResourceShare float64
}

type FileRef struct {
	Name   string
	URL    string
	NBytes float64
	MD5    string
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
		if r.State == StateCompute && r.Suspended == 0 {
			running++
			e.checkRunning(r)
		}
	}

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
		go e.startTask(r)
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
		e.state.UpdateResult(r.Name, StateError, 0, 0, 0)
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
	b.WriteString("</app_init_data>\n")
	return []byte(b.String())
}

func (e *Engine) downloadFiles(r ResultSnapshot) error {
	for _, f := range r.Files {
		dest := filepath.Join(r.Slot, f.Name)
		if f.MD5 != "" && fileExists(dest) {
			if ok, _ := md5Matches(dest, f.MD5); ok {
				log.Printf("[Worker] %s already present and verified", f.Name)
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
	}
	return nil
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
				e.uploadOutputs(r)
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

func (e *Engine) uploadOutputs(r ResultSnapshot) {
	if e.ul == nil {
		return
	}
	outputs := []string{"stdout.txt", "stderr.txt", "fraction_done.txt"}
	for _, name := range outputs {
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

func (e *Engine) checkRunning(r ResultSnapshot) {
	if r.Slot == "" {
		return
	}
	e.mu.RLock()
	_, ok := e.running[r.Name]
	e.mu.RUnlock()
	if !ok {
		exePath := e.findExecutable(r)
		if exePath == "" {
			log.Printf("[Worker] Task %s exe missing, marking ready", r.Name)
			e.state.UpdateResult(r.Name, StateReady, 1.0, r.CPUTime, 0)
		}
	}
}

func (e *Engine) Cleanup() {
	results := e.state.GetResults()
	for _, r := range results {
		if (r.State == StateError || r.State == StateReady) && r.Slot != "" {
			log.Printf("[Worker] Cleaning slot %s", r.Slot)
			e.cache.FreeSlot(r.Slot)
			e.state.RemoveResult(r.Name)
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
