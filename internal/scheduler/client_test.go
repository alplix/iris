package scheduler

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestDownloadReportsProgressAndWritesFile(t *testing.T) {
	payload := bytes.Repeat([]byte("iris"), 100_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "work.dat")
	var lastDone, lastTotal int64
	if err := DownloadFileByURLCtx(context.Background(), srv.URL, dest, func(done, total int64) {
		lastDone, lastTotal = done, total
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("downloaded file differs (err=%v, %d bytes)", err, len(got))
	}
	if lastDone != int64(len(payload)) || lastTotal != int64(len(payload)) {
		t.Errorf("last progress = %d/%d, want %d/%d", lastDone, lastTotal, len(payload), len(payload))
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error("temporary .part file should be gone after success")
	}
}

func TestDownloadHTTPErrorLeavesNothingBehind(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "x.dat")
	if err := DownloadFileByURL(srv.URL, dest); err == nil {
		t.Fatal("HTTP 404 must be an error")
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should not exist", p)
		}
	}
}

func TestDownloadCancelRemovesPartialFile(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 4096))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	dest := filepath.Join(t.TempDir(), "big.dat")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- DownloadFileByURLCtx(ctx, srv.URL, dest, func(done, total int64) {
			select {
			case started <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("a cancelled download must return an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the download")
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should not exist after cancel", p)
		}
	}
}

func TestUploadSendsFileAsMultipart(t *testing.T) {
	var gotName string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Errorf("content type: %v", err)
			return
		}
		part, err := multipart.NewReader(r.Body, params["boundary"]).NextPart()
		if err != nil {
			t.Errorf("multipart: %v", err)
			return
		}
		gotName = part.FileName()
		gotBody, _ = io.ReadAll(part)
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "out (1).txt")
	if err := os.WriteFile(file, []byte("result data"), 0o644); err != nil {
		t.Fatal(err)
	}
	var lastDone int64
	if err := NewClient(srv.URL).UploadFileCtx(context.Background(), file, "", func(done, total int64) { lastDone = done }); err != nil {
		t.Fatal(err)
	}
	if gotName != "out (1).txt" || string(gotBody) != "result data" {
		t.Errorf("server saw file %q with body %q", gotName, gotBody)
	}
	if lastDone != int64(len("result data")) {
		t.Errorf("upload progress = %d, want %d", lastDone, len("result data"))
	}
}

func TestPlatformIsKnown(t *testing.T) {
	if Platform() == "" {
		t.Fatal("Platform must not be empty")
	}
}

// Every architecture the release ships must have a proper BOINC platform name
// rather than the GOARCH-GOOS fallback.
func TestPlatformNamesForShippedArchitectures(t *testing.T) {
	want := map[string]string{
		"linux/386":     "i686-pc-linux-gnu",
		"linux/arm":     "arm-unknown-linux-gnueabihf",
		"linux/riscv64": "riscv64-unknown-linux-gnu",
		"linux/ppc64le": "powerpc64le-unknown-linux-gnu",
		"linux/ppc64":   "powerpc64-unknown-linux-gnu",
		"windows/386":   "windows_intelx86",
		"windows/amd64": "windows_x86_64",
		"windows/arm64": "windows_arm64",
		"freebsd/amd64": "x86_64-pc-freebsd",
	}
	for k, v := range want {
		if platforms[k] != v {
			t.Errorf("platform for %s = %q, want %q", k, platforms[k], v)
		}
	}
}
