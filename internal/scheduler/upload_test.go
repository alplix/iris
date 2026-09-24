package scheduler

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uploadServer is a strict stand-in for file_upload_handler: it answers the
// size query and accepts a <file_upload> only with the certificate present.
func uploadServer(t *testing.T, have int, status string, got *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		text := string(raw)
		if strings.Contains(text, "<get_file_size>") {
			fmt.Fprintf(w, "<data_server_reply>\n<file_size>%d</file_size>\n</data_server_reply>\n", have)
			return
		}
		i := strings.Index(text, "<data>\n")
		if i < 0 || !strings.Contains(text, "<xml_signature>\nSIG\n</xml_signature>") {
			t.Errorf("upload lacks certificate or data marker:\n%s", text)
		}
		if got != nil {
			*got = text[i+len("<data>\n"):] + "|" + text[:i]
		}
		fmt.Fprintf(w, "<data_server_reply>\n<status>%s</status>\n<message>nope</message>\n</data_server_reply>\n", status)
	}))
}

func writeTemp(t *testing.T, content string) string {
	p := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUploadResultFileResumesFromServerSize(t *testing.T) {
	var got string
	srv := uploadServer(t, 4, "0", &got)
	defer srv.Close()
	err := UploadResultFile(context.Background(), ResultUpload{Name: "f", Path: writeTemp(t, "0123456789"), URLs: []string{srv.URL}, MaxNBytes: 100, Signature: "SIG"}, "8.0.2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "456789|") || !strings.Contains(got, "<offset>4</offset>") || !strings.Contains(got, "<nbytes>10</nbytes>") {
		t.Errorf("expected only the remaining bytes with offset 4 of 10, got %q", got)
	}
}

func TestUploadResultFileSkipsAlreadyCompleteFile(t *testing.T) {
	srv := uploadServer(t, 10, "0", nil)
	defer srv.Close()
	if err := UploadResultFile(context.Background(), ResultUpload{Name: "f", Path: writeTemp(t, "0123456789"), URLs: []string{srv.URL}, Signature: "SIG"}, "8.0.2", nil); err != nil {
		t.Fatal(err)
	}
}

func TestUploadResultFileStatusMeaning(t *testing.T) {
	for status, permanent := range map[string]bool{"-1": true, "1": false} {
		srv := uploadServer(t, 0, status, nil)
		err := UploadResultFile(context.Background(), ResultUpload{Name: "f", Path: writeTemp(t, "x"), URLs: []string{srv.URL}, Signature: "SIG"}, "8.0.2", nil)
		srv.Close()
		ue, ok := err.(*UploadError)
		if !ok || ue.Permanent != permanent || !strings.Contains(ue.Msg, "nope") {
			t.Errorf("status %s: got %v (permanent should be %v)", status, err, permanent)
		}
	}
}

func TestUploadResultFileTriesTheNextAddress(t *testing.T) {
	good := uploadServer(t, 0, "0", nil)
	defer good.Close()
	err := UploadResultFile(context.Background(), ResultUpload{Name: "f", Path: writeTemp(t, "x"), URLs: []string{"http://127.0.0.1:1/x", good.URL}, Signature: "SIG"}, "8.0.2", nil)
	if err != nil {
		t.Fatalf("second address should have worked: %v", err)
	}
}

func TestBuildReportUsesTheReferenceClientsFields(t *testing.T) {
	r := buildReport(ResultInfo{
		Name: "r1", State: 4, CPUTime: 12.5, ExitStatus: 0, VersionNum: 812, PlanClass: "cuda",
		Platform: "x86_64-pc-linux-gnu",
		Outputs:  []OutputInfo{{Name: "o1", Present: true, Uploaded: true, NBytes: 5, MaxNBytes: 9, MD5: "abc"}, {Name: "skipped"}},
	})
	out, err := xml.MarshalIndent(r, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"<name>r1</name>", "<final_cpu_time>12.5</final_cpu_time>", "<exit_status>0</exit_status>",
		"<state>5</state>", "<version_num>812</version_num>", "<plan_class>cuda</plan_class>", "<app_version_num>812</app_version_num>",
		"<md5_cksum>abc</md5_cksum>", "<core_client_version>"} {
		if !strings.Contains(s, want) {
			t.Errorf("report lacks %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, "skipped") {
		t.Error("an output that was never uploaded must not be listed")
	}
	if errR := buildReport(ResultInfo{Name: "r2", State: 5, ExitStatus: -163}); errR.State != ReportStateComputeError || errR.ExitStatus != -163 {
		t.Errorf("a failed task reports state 3 with its error code, got %+v", errR)
	}
}

func TestStderrOutIsCappedAndCannotBreakOutOfCDATA(t *testing.T) {
	x := NewStderrOut("1.2.3", "bad ]]> text "+strings.Repeat("y", 100000))
	if len(x.Inner) > 64*1024+200 || strings.Contains(x.Inner, "bad ]]>") {
		t.Errorf("stderr must be capped and CDATA-safe (len %d)", len(x.Inner))
	}
}
