package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alplix/iris/internal/cache"
	"github.com/alplix/iris/internal/scheduler"
	"github.com/alplix/iris/internal/state"
	"github.com/alplix/iris/internal/worker"
)

// TestHelperProcess is not a test: the end-to-end test below launches this
// very test binary as the "project application". It reads its input under the
// logical name in.txt, writes the upper-cased text to out.txt and exits.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("IRIS_E2E_HELPER") != "1" {
		return
	}
	in, err := os.ReadFile("in.txt")
	if err != nil {
		fmt.Fprintln(os.Stderr, "no input:", err)
		os.Exit(3)
	}
	os.WriteFile("out.txt", []byte(strings.ToUpper(string(in))), 0o644)
	os.WriteFile("fraction_done.txt", []byte("1"), 0o644)
	fmt.Fprintln(os.Stderr, "helper done")
	os.Exit(0)
}

func hexMD5(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

// fakeProject is a BOINC project that follows the real wire formats: a master
// page naming the scheduler, a scheduler that hands out one task and expects
// it back in a report, downloads, and a file upload handler that checks the
// signed certificate and the MD5 exactly the way the real one does.
type fakeProject struct {
	t   *testing.T
	srv *httptest.Server
	app []byte
	in  []byte

	mu        sync.Mutex
	sent      bool
	uploaded  []byte
	uploadOK  bool
	reports   []string
	acked     bool
	badFormat []string
}

const fakeSignature = "abcdef0123456789abcdef0123456789\n0123456789abcdef"

func (p *fakeProject) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<html><scheduler>%s/cgi-bin/scheduler</scheduler></html>", p.srv.URL)
	})
	mux.HandleFunc("/download/app", func(w http.ResponseWriter, r *http.Request) { w.Write(p.app) })
	mux.HandleFunc("/download/in", func(w http.ResponseWriter, r *http.Request) { w.Write(p.in) })
	mux.HandleFunc("/cgi-bin/scheduler", p.scheduler)
	mux.HandleFunc("/upload", p.upload)
	return mux
}

func (p *fakeProject) scheduler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := string(raw)
	p.mu.Lock()
	defer p.mu.Unlock()

	// Only completed results may be reported, with the real field set.
	if strings.Contains(body, "<result>") {
		p.reports = append(p.reports, body)
		if !p.uploadOK {
			p.badFormat = append(p.badFormat, "result reported before its file was uploaded")
		}
		for _, need := range []string{"<name>e2e_result_0</name>", "<final_cpu_time>", "<exit_status>0</exit_status>",
			"<file_info>", "<name>e2e_result_0_0</name>", "<md5_cksum>" + hexMD5([]byte(strings.ToUpper(string(p.in)))) + "</md5_cksum>",
			"<stderr_out>", "<core_client_version>"} {
			if !strings.Contains(body, need) {
				p.badFormat = append(p.badFormat, "report lacks "+need)
			}
		}
		if strings.Contains(body, "<fraction_done>") || strings.Contains(body, "<wu_name>") {
			p.badFormat = append(p.badFormat, "report carries fields the reference client never sends")
		}
	}

	w.Header().Set("Content-Type", "text/xml")
	var b strings.Builder
	b.WriteString("<scheduler_reply>\n<project_name>E2E Project</project_name>\n")
	if strings.Contains(body, "<name>e2e_result_0</name>") {
		p.acked = true
		b.WriteString("<result_ack>\n<name>e2e_result_0</name>\n</result_ack>\n")
	}
	if !p.sent {
		p.sent = true
		plat := scheduler.Platform()
		b.WriteString("<app_version>\n<app_name>helper</app_name>\n<version_num>100</version_num>\n")
		b.WriteString("<platform>" + plat + "</platform>\n<plan_class></plan_class>\n")
		b.WriteString("<file_ref>\n<file_name>helper.exe</file_name>\n<open_name>helper.exe</open_name>\n<main_program/>\n</file_ref>\n</app_version>\n")
		b.WriteString("<workunit>\n<name>wu0</name>\n<app_name>helper</app_name>\n<command_line>-test.run=^TestHelperProcess$</command_line>\n")
		b.WriteString("<file_ref>\n<file_name>wu0_in</file_name>\n<open_name>in.txt</open_name>\n</file_ref>\n</workunit>\n")
		b.WriteString("<result>\n<name>e2e_result_0</name>\n<wu_name>wu0</wu_name>\n<report_deadline>9999999999</report_deadline>\n")
		b.WriteString("<platform>" + plat + "</platform>\n<version_num>100</version_num>\n")
		b.WriteString("<file_ref>\n<file_name>e2e_result_0_0</file_name>\n<open_name>out.txt</open_name>\n</file_ref>\n</result>\n")
		fmt.Fprintf(&b, "<file_info>\n<name>helper.exe</name>\n<url>%s/download/app</url>\n<md5_cksum>%s</md5_cksum>\n<nbytes>%d</nbytes>\n<executable/>\n</file_info>\n",
			p.srv.URL, hexMD5(p.app), len(p.app))
		fmt.Fprintf(&b, "<file_info>\n<name>wu0_in</name>\n<url>%s/download/in</url>\n<md5_cksum>%s</md5_cksum>\n<nbytes>%d</nbytes>\n</file_info>\n",
			p.srv.URL, hexMD5(p.in), len(p.in))
		fmt.Fprintf(&b, "<file_info>\n<name>e2e_result_0_0</name>\n<generated_locally/>\n<upload_when_present/>\n<max_nbytes>1000</max_nbytes>\n<url>%s/upload</url>\n<xml_signature>\n%s\n</xml_signature>\n</file_info>\n",
			p.srv.URL, fakeSignature)
	}
	b.WriteString("</scheduler_reply>\n")
	io.WriteString(w, b.String())
}

var (
	tagLine  = regexp.MustCompile(`<([a-z0-9_]+)>([^<]*)</[a-z0-9_]+>`)
	sizeQuer = regexp.MustCompile(`<get_file_size>([^<]+)</get_file_size>`)
)

// upload mirrors sched/file_upload_handler.cpp: header tags read line by line,
// the certificate checked, raw bytes after "<data>".
func (p *fakeProject) upload(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	text := string(raw)
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Header.Get("Content-Type") != "text/xml" {
		p.badFormat = append(p.badFormat, "upload without text/xml content type")
	}
	if m := sizeQuer.FindStringSubmatch(text); m != nil {
		fmt.Fprintf(w, "<data_server_reply>\n    <file_size>%d</file_size>\n</data_server_reply>\n", len(p.uploaded))
		return
	}
	i := strings.Index(text, "<data>\n")
	if i < 0 || !strings.Contains(text, "<file_upload>") {
		fmt.Fprint(w, "<data_server_reply>\n<status>-1</status>\n<message>malformed</message>\n</data_server_reply>\n")
		return
	}
	head, data := text[:i], text[i+len("<data>\n"):]
	fields := map[string]string{}
	for _, m := range tagLine.FindAllStringSubmatch(head, -1) {
		fields[m[1]] = m[2]
	}
	sigOK := strings.Contains(head, "<xml_signature>\n"+fakeSignature+"\n</xml_signature>")
	maxN, _ := strconv.ParseFloat(fields["max_nbytes"], 64)
	nbytes, _ := strconv.Atoi(fields["nbytes"])
	if fields["name"] != "e2e_result_0_0" || !sigOK || maxN != 1000 || nbytes != len(data) || fields["md5_cksum"] != hexMD5([]byte(data)) {
		fmt.Fprint(w, "<data_server_reply>\n<status>-1</status>\n<message>bad certificate or size</message>\n</data_server_reply>\n")
		p.badFormat = append(p.badFormat, fmt.Sprintf("upload rejected: name=%q sigOK=%v max=%v nbytes=%d/%d", fields["name"], sigOK, maxN, nbytes, len(data)))
		return
	}
	p.uploaded = []byte(data)
	p.uploadOK = true
	fmt.Fprint(w, "<data_server_reply>\n    <status>0</status>\n</data_server_reply>\n")
}

// TestEndToEndTaskIsComputedUploadedAndReported runs the real scheduler and
// worker engines against a project that speaks the real protocol: a task is
// fetched, its input and application downloaded, the application actually
// launched, its output file uploaded with the project's certificate, the
// finished result reported, and the project's acknowledgement clears it.
func TestEndToEndTaskIsComputedUploadedAndReported(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProject{t: t, app: app, in: []byte("hello boinc world")}
	p.srv = httptest.NewServer(p.handler())
	defer p.srv.Close()
	t.Setenv("IRIS_E2E_HELPER", "1")

	dir := t.TempDir()
	st := state.New(dir)
	cm := cache.New(dir, cache.Config{Enabled: true, CacheSizeMB: 512, CPUCacheSizeMB: 512, SeparateSlots: true, SeparateProjects: true})
	if err := cm.Init(); err != nil {
		t.Fatal(err)
	}
	st.AddProject(state.Project{Name: "E2E", MasterURL: p.srv.URL, Authenticator: "bogus", ResourceShare: 100})

	xfers := newTransferTracker(st)
	sched := scheduler.NewEngine(&stateAdapter{st}, &cacheAdapter{cm}, scheduler.EngineConfig{
		SchedulerInterval: 300 * time.Millisecond, MinRPCInterval: 50 * time.Millisecond, DataDir: dir,
	})
	dl := &downloaderAdapter{dataDir: dir, xfers: xfers}
	wk := worker.NewEngine(&stateWorkerAdapter{st}, &cacheAdapter{cm}, &projectAdapter{st}, dl, dl, worker.Config{
		MaxConcurrent: 1, DataDir: dir, RealAppsEnabledFn: func() bool { return true },
	})
	sched.Start()
	wk.Start()
	defer func() { wk.Stop(); sched.Stop() }()

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		done := p.acked
		p.mu.Unlock()
		st.RLock()
		left := len(st.Results)
		st.RUnlock()
		if done && left == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.badFormat {
		t.Error(m)
	}
	if got, want := string(p.uploaded), "HELLO BOINC WORLD"; got != want {
		t.Fatalf("uploaded output = %q, want %q", got, want)
	}
	if !p.acked || len(p.reports) == 0 {
		t.Fatalf("the finished task was never reported (acked=%v, reports=%d)", p.acked, len(p.reports))
	}
	st.RLock()
	defer st.RUnlock()
	if len(st.Results) != 0 {
		t.Errorf("the acknowledged result should have been removed, still have %d", len(st.Results))
	}
}
