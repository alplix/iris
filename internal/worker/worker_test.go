package worker

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeDownloader struct{ content string }

func (f fakeDownloader) DownloadFile(projectURL, filename, dest string) error {
	return os.WriteFile(dest, []byte(f.content), 0o644)
}

func (f fakeDownloader) DownloadFileByURL(url, dest string) error {
	return os.WriteFile(dest, []byte(f.content), 0o644)
}

func sum(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestDownloadFilesVerifiesMD5(t *testing.T) {
	slot := t.TempDir()
	e := &Engine{dl: fakeDownloader{content: "payload"}}

	good := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "app", URL: "http://x/app", MD5: strings.ToUpper(sum("payload"))}}}
	if err := e.downloadFiles(good); err != nil {
		t.Fatalf("matching MD5 must pass: %v", err)
	}

	bad := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "evil", URL: "http://x/evil", MD5: sum("something else")}}}
	if err := e.downloadFiles(bad); err == nil {
		t.Fatal("a file with the wrong MD5 must be rejected")
	}
	if _, err := os.Stat(filepath.Join(slot, "evil")); !os.IsNotExist(err) {
		t.Error("a rejected file must be deleted so it can never be executed")
	}

	none := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "nohash", URL: "http://x/n"}}}
	if err := e.downloadFiles(none); err != nil {
		t.Fatalf("files without a hash are accepted: %v", err)
	}
}

func TestDownloadFilesSkipsVerifiedExistingFile(t *testing.T) {
	slot := t.TempDir()
	if err := os.WriteFile(filepath.Join(slot, "app"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The downloader would write different bytes; a verified file must not be
	// fetched again.
	e := &Engine{dl: fakeDownloader{content: "changed"}}
	r := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "app", URL: "http://x/app", MD5: sum("payload")}}}
	if err := e.downloadFiles(r); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(slot, "app")); string(b) != "payload" {
		t.Errorf("verified file was overwritten: %q", b)
	}
}

// TestDownloadFilesSetsTheExecuteBitOnTheMainProgram guards a real, easy to
// miss failure mode: a project's real executable downloads fine but then
// exec.Command's Start() fails with "permission denied" because the
// downloaded bytes never got the execute bit non-Windows requires.
func TestDownloadFilesSetsTheExecuteBitOnTheMainProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the execute bit is a non-Windows concept")
	}
	slot := t.TempDir()
	e := &Engine{dl: fakeDownloader{content: "#!/bin/sh\necho hi\n"}}
	r := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: "real_app", URL: "http://x/real_app", MainProgram: true}}}
	if err := e.downloadFiles(r); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(slot, "real_app"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("main program file mode = %v, want the execute bit set", info.Mode())
	}
}

// TestFindExecutablePrefersTheRealAppOnlyWhenEnabled covers the experimental
// toggle end to end: with it off, a downloaded real application must be
// ignored (and the legacy stub used instead, or nothing found at all) so
// nothing runs unsandboxed until the person explicitly opts in; with it on,
// the real application wins.
func TestFindExecutablePrefersTheRealAppOnlyWhenEnabled(t *testing.T) {
	slot := t.TempDir()
	realApp := "real_app"
	if runtime.GOOS == "windows" {
		realApp = "real_app.exe"
	}
	if err := os.WriteFile(filepath.Join(slot, realApp), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "app"
	if runtime.GOOS == "windows" {
		stub = "app.exe"
	}
	if err := os.WriteFile(filepath.Join(slot, stub), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := ResultSnapshot{Slot: slot, Files: []FileRef{{Name: realApp, MainProgram: true}}}

	off := &Engine{cfg: Config{RealAppsEnabledFn: func() bool { return false }}}
	if got := off.findExecutable(r); filepath.Base(got) != stub {
		t.Errorf("with the toggle off, findExecutable = %q, want the legacy stub %q", got, stub)
	}

	on := &Engine{cfg: Config{RealAppsEnabledFn: func() bool { return true }}}
	if got := on.findExecutable(r); filepath.Base(got) != realApp {
		t.Errorf("with the toggle on, findExecutable = %q, want the real app %q", got, realApp)
	}
}

// TestBuildInitDataXMLIncludesCoreFields checks the subset of BOINC's real
// init_data.xml (lib/app_ipc.h's INIT_DATA_FILE) this deliberately-partial
// implementation writes: enough for an app using the BOINC API's standalone
// fallback to find its own identity and slot, documented as a limitation
// where it stops short of the full shared-memory API.
func TestBuildInitDataXMLIncludesCoreFields(t *testing.T) {
	r := ResultSnapshot{
		Name: "wu_1_0", WuName: "wu_1", AppVersionNum: 7, AppName: "my_app",
		Slot: filepath.Join("data", "slots", "0"), FracDone: 0.25,
	}
	data := string(buildInitDataXML(r, "some-auth-token", "data", 300))
	for _, want := range []string{
		"<app_init_data>", "</app_init_data>",
		"<app_version>7</app_version>",
		"<app_name>my_app</app_name>",
		"<wu_name>wu_1</wu_name>",
		"<result_name>wu_1_0</result_name>",
		"<checkpoint_period>300</checkpoint_period>",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("init_data.xml missing %q, got:\n%s", want, data)
		}
	}
	if strings.Contains(data, "some-auth-token") == false {
		t.Errorf("expected the authenticator to be included, got:\n%s", data)
	}
}

// TestBuildInitDataXMLAdvertisesTheFirstGPUOnlyForGPUTasks guards the
// (deliberately simplistic) device-selection wiring: a GPU task gets a
// device index so its app can find its GPU at all, a CPU task gets neither
// tag, since a stray gpu_device_num could make a CPU app that happens to
// link the BOINC API misbehave.
func TestBuildInitDataXMLAdvertisesTheFirstGPUOnlyForGPUTasks(t *testing.T) {
	gpuTask := ResultSnapshot{Name: "g", GPU: true}
	data := string(buildInitDataXML(gpuTask, "", "data", 300))
	for _, want := range []string{"<gpu_device_num>0</gpu_device_num>", "<gpu_opencl_dev_index>0</gpu_opencl_dev_index>"} {
		if !strings.Contains(data, want) {
			t.Errorf("GPU task init_data.xml missing %q, got:\n%s", want, data)
		}
	}

	cpuTask := ResultSnapshot{Name: "c", GPU: false}
	data = string(buildInitDataXML(cpuTask, "", "data", 300))
	if strings.Contains(data, "gpu_device_num") {
		t.Errorf("a CPU task must not get a gpu_device_num, got:\n%s", data)
	}
}

func TestGPUEnvAdvertisesDeviceZero(t *testing.T) {
	env := gpuEnv()
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "CUDA_VISIBLE_DEVICES=0") || !strings.Contains(joined, "GPU_DEVICE_ORDINAL=0") {
		t.Errorf("gpuEnv() = %v, want both vendor device-selection vars set to device 0", env)
	}
}

func TestIsMainProgramFileMatchesOnlyTheFlaggedFile(t *testing.T) {
	r := ResultSnapshot{Slot: "/slot", Files: []FileRef{
		{Name: "input.dat"},
		{Name: "real_app", MainProgram: true},
	}}
	if !isMainProgramFile(r, filepath.Join("/slot", "real_app")) {
		t.Error("expected the flagged file to be recognized as the main program")
	}
	if isMainProgramFile(r, filepath.Join("/slot", "input.dat")) {
		t.Error("an unflagged file must not be treated as the main program")
	}
}
