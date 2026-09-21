package worker

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	AddMessage(body, project string, pri int)
	Save()
}

type CacheAccessor interface {
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
	Files         []FileRef
	Suspended     int
	MaxElapSec    float64
}

type FileRef struct {
	Name   string
	URL    string
	NBytes float64
	MD5    string
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

	for _, r := range results {
		if r.State == StateNew && r.Suspended == 0 && running < maxConcurrent {
			if taskMode == 3 {
				break
			}
			if diskQuota > 0 && diskUsage >= diskQuota {
				log.Printf("[Worker] Disk quota reached (%d/%d), skipping downloads", diskUsage, diskQuota)
				break
			}
			go e.startTask(r)
			running++
		}
	}
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

	log.Printf("[Worker] Task %s ready, launching %s", r.Name, exePath)
	e.state.UpdateResult(r.Name, StateCompute, r.FracDone, 0, 0)
	e.runApp(r, exePath)
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

func (e *Engine) findExecutable(r ResultSnapshot) string {
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

	for {
		select {
		case err := <-done:
			elapsed := time.Since(start).Seconds()
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
				log.Printf("[Worker] Task %s completed OK (%.1fs, progress=%.2f)", r.Name, elapsed, progress)

			case ExitNeedAbort:
				log.Printf("[Worker] Task %s requests abort (exit %d)", r.Name, exitCode)
				e.state.UpdateResult(r.Name, StateError, 0, elapsed, exitCode)
				e.state.UpdateStats(false, elapsed, 0, 0)

			case ExitClientExiting:
				if wasStopping {
					log.Printf("[Worker] Task %s paused for shutdown (exit %d)", r.Name, exitCode)
					e.writeCheckpoint(r)
					e.state.UpdateResult(r.Name, StateNew, readProgress(r.Slot), elapsed, 0)
				} else {
					log.Printf("[Worker] Task %s requests client exit unexpectedly (exit %d)", r.Name, exitCode)
					e.state.UpdateResult(r.Name, StateError, 0, elapsed, exitCode)
					e.state.UpdateStats(false, elapsed, 0, 0)
				}

			default:
				log.Printf("[Worker] Task %s failed with exit code %d (%.1fs)", r.Name, exitCode, elapsed)
				e.state.UpdateResult(r.Name, StateError, 0, elapsed, exitCode)
				e.state.UpdateStats(false, elapsed, 0, 0)
			}
			return

		case <-checkpointTicker.C:
			e.writeCheckpoint(r)
			progress := readProgress(r.Slot)
			elapsed := time.Since(start).Seconds()
			e.state.UpdateResult(r.Name, StateCompute, progress, elapsed, 0)

		case <-ticker.C:
			elapsed := time.Since(start)
			maxElap := e.cfg.SlotTimeout
			if r.MaxElapSec > 0 && time.Duration(r.MaxElapSec*float64(time.Second)) < maxElap {
				maxElap = time.Duration(r.MaxElapSec * float64(time.Second))
			}
			if elapsed > maxElap {
				terminateProcess(cmd)
				e.state.UpdateResult(r.Name, StateError, 0, maxElap.Seconds(), ExitExceeded)
				log.Printf("[Worker] Task %s timed out (%.1fs)", r.Name, maxElap.Seconds())
				return
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

		case <-e.stop:
			log.Printf("[Worker] Stopping task %s", r.Name)
			terminateProcess(cmd)
			select {
			case <-time.After(30 * time.Second):
				cmd.Process.Kill()
			case <-done:
			}
			e.writeCheckpoint(r)
			e.state.UpdateResult(r.Name, StateNew, readProgress(r.Slot), time.Since(start).Seconds(), 0)
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
