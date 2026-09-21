package main

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/alplix/iris/internal/scheduler"
	"github.com/alplix/iris/internal/state"
	"github.com/alplix/iris/internal/worker"
)

// transferTracker runs file transfers while publishing their progress in the
// client state (what get_file_transfers reports), accounting the moved bytes
// to the daily history, and letting them be cancelled from the manager.
type transferTracker struct {
	st *state.State

	mu      sync.Mutex
	active  map[string]context.CancelFunc
	aborted map[string]bool
}

func newTransferTracker(st *state.State) *transferTracker {
	return &transferTracker{
		st:      st,
		active:  map[string]context.CancelFunc{},
		aborted: map[string]bool{},
	}
}

// claim reserves a display name, disambiguating concurrent transfers of
// identically named files (every task downloads its own copy of the app).
func (t *transferTracker) claim(name string, cancel context.CancelFunc) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	unique := name
	for n := 2; ; n++ {
		if _, busy := t.active[unique]; !busy {
			break
		}
		unique = fmt.Sprintf("%s (%d)", name, n)
	}
	t.active[unique] = cancel
	return unique
}

func (t *transferTracker) release(name string) (wasAborted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, name)
	wasAborted = t.aborted[name]
	delete(t.aborted, name)
	return wasAborted
}

func (t *transferTracker) run(name, projectURL string, upload bool,
	fn func(ctx context.Context, progress scheduler.ProgressFunc) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	name = t.claim(name, cancel)

	up := 0
	if upload {
		up = 1
	}
	t.st.BeginTransfer(state.Xfer{Name: name, ProjectURL: projectURL, IsUpload: up})

	var moved int64
	err := fn(ctx, func(done, total int64) {
		moved = done
		t.st.SetTransferProgress(name, float64(done), float64(total))
	})
	t.st.AddXfer(upload, moved)

	if t.release(name) {
		t.st.RemoveTransfer(name)
		if err == nil {
			return nil
		}
		return fmt.Errorf("transfer %s aborted", name)
	}
	t.st.EndTransfer(name, err == nil)
	return err
}

// Abort cancels a running transfer or discards a failed one. It reports
// whether anything by that name existed.
func (t *transferTracker) Abort(name string) bool {
	t.mu.Lock()
	cancel, running := t.active[name]
	if running {
		t.aborted[name] = true
	}
	t.mu.Unlock()
	if running {
		cancel()
		return true
	}
	if t.st.TransferFailed(name) {
		t.st.RemoveTransfer(name)
		return true
	}
	return false
}

// Retry requeues the tasks that were stopped by a failed transfer.
func (t *transferTracker) Retry(name string) error {
	if !t.st.TransferFailed(name) {
		return fmt.Errorf("transfer %q has not failed", name)
	}
	// Concurrent copies of one file are listed as "name (2)" and so on; the
	// tasks reference the plain file name.
	file := dupSuffix.ReplaceAllString(name, "")
	if t.st.ResetResultsWithFile(file, worker.StateError, worker.StateNew) == 0 {
		return fmt.Errorf("no task is waiting for %q; abort it to dismiss it", name)
	}
	t.st.RemoveTransfer(name)
	return nil
}

var dupSuffix = regexp.MustCompile(` \(\d+\)$`)

// downloaderAdapter implements worker.Downloader and worker.Uploader.
type downloaderAdapter struct {
	dataDir string
	xfers   *transferTracker
}

func (d *downloaderAdapter) DownloadFile(projectURL, filename, destPath string) error {
	client := scheduler.NewClient(projectURL)
	return d.xfers.run(filename, projectURL, false, func(ctx context.Context, p scheduler.ProgressFunc) error {
		return client.DownloadFileCtx(ctx, filename, destPath, p)
	})
}

func (d *downloaderAdapter) DownloadFileByURL(rawURL, destPath string) error {
	return d.xfers.run(filepath.Base(destPath), rawURL, false, func(ctx context.Context, p scheduler.ProgressFunc) error {
		return scheduler.DownloadFileByURLCtx(ctx, rawURL, destPath, p)
	})
}

func (d *downloaderAdapter) UploadFile(projectURL, filePath string) error {
	client := scheduler.NewClient(projectURL)
	name := filepath.Base(filepath.Dir(filePath)) + "_" + filepath.Base(filePath)
	return d.xfers.run(name, projectURL, true, func(ctx context.Context, p scheduler.ProgressFunc) error {
		return client.UploadFileCtx(ctx, filePath, "", p)
	})
}
