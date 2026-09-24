package scheduler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAppInfo = `<app_info>
 <app><name>myapp</name></app>
 <file_info><name>myapp.exe</name><executable/></file_info>
 <app_version><app_name>myapp</app_name><version_num>105</version_num><avg_ncpus>2</avg_ncpus><cmdline>--fast</cmdline>
  <file_ref><file_name>myapp.exe</file_name><open_name>run</open_name><main_program/></file_ref>
 </app_version>
</app_info>`

// With an app_info.xml in the project's folder the request must say the
// platform is "anonymous" and list the person's own applications; work the
// project sends for one of them uses the local file, not a download.
func TestAnonymousPlatformUsesTheProjectFoldersApplication(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app_info.xml"), []byte(testAppInfo), 0o644); err != nil {
		t.Fatal(err)
	}
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, `<scheduler_reply>
<workunit><name>w</name><app_name>myapp</app_name><command_line>--input x</command_line>
<file_ref><file_name>w_in</file_name><open_name>in</open_name></file_ref></workunit>
<result><name>r</name><wu_name>w</wu_name><app_version_num>105</app_version_num>
<file_ref><file_name>r_0</file_name><open_name>out</open_name></file_ref></result>
<file_info><name>w_in</name><url>http://x/in</url><md5_cksum>m</md5_cksum><nbytes>1</nbytes></file_info>
<file_info><name>r_0</name><generated_locally/><upload_when_present/><max_nbytes>9</max_nbytes><url>http://x/up</url><xml_signature>S</xml_signature></file_info>
</scheduler_reply>`)
	}))
	defer srv.Close()
	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Anon", Dir: dir})
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})

	for _, want := range []string{"\n <platform_name>anonymous</platform_name>\n", "<app_versions>", "<app_name>myapp</app_name>", "<version_num>105</version_num>", "<avg_ncpus>2</avg_ncpus>"} {
		if !strings.Contains(body, want) {
			t.Errorf("request lacks %q:\n%s", want, body)
		}
	}
	added := fs.Added()
	if len(added) != 1 {
		t.Fatalf("expected one task, got %d (%v)", len(added), fs.Messages())
	}
	r := added[0]
	if r.Platform != "anonymous" || r.AppName != "myapp" || r.CmdLine != "--fast --input x" {
		t.Errorf("task fields wrong: %+v", r)
	}
	var exe *FileInfo
	for i := range r.Files {
		if r.Files[i].Name == "myapp.exe" {
			exe = &r.Files[i]
		}
	}
	if exe == nil || !exe.MainProgram || exe.OpenName != "run" || exe.LocalPath != filepath.Join(dir, "myapp.exe") || exe.URL != "" {
		t.Errorf("the application must be the local file, got %+v", exe)
	}
}

func TestAnonymousPlatformReportsAnUnusableAppInfo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app_info.xml"), []byte("<app_info><app_version></app_version></app_info>"), 0o644)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<scheduler_reply></scheduler_reply>")
	}))
	defer srv.Close()
	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Bad", Dir: dir})
	NewEngine(fs, fakeCache{}, EngineConfig{}).doRPC(&ProjectState{URL: srv.URL})
	found := false
	for _, m := range fs.Messages() {
		if strings.Contains(m, "app_info.xml") {
			found = true
		}
	}
	if !found {
		t.Errorf("a broken app_info.xml must be explained in the event log, got %v", fs.Messages())
	}
}
