package scheduler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNoWorkBackoffGrowsAndIsCapped(t *testing.T) {
	want := []time.Duration{10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 160 * time.Minute, 4 * time.Hour, 4 * time.Hour}
	for i, w := range want {
		if got := noWorkBackoff(i + 1); got != w {
			t.Errorf("streak %d: got %s want %s", i+1, got, w)
		}
	}
}

func TestEmptyReplyBacksTheProjectOffAndWorkResetsIt(t *testing.T) {
	work := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if work {
			io.WriteString(w, "<scheduler_reply><result><name>r</name><wu_name>w</wu_name></result></scheduler_reply>")
			return
		}
		io.WriteString(w, "<scheduler_reply><message priority=\"low\">Project has no tasks available</message></scheduler_reply>")
	}))
	defer srv.Close()
	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "Empty"})
	e := NewEngine(fs, fakeCache{}, EngineConfig{MinRPCInterval: 30 * time.Second})
	ps := &ProjectState{URL: srv.URL, MinRPCInterval: 30 * time.Second}
	e.doRPC(ps)
	if ps.MinRPCInterval != 10*time.Minute {
		t.Errorf("first empty reply should wait 10 minutes, got %s", ps.MinRPCInterval)
	}
	e.doRPC(ps)
	if ps.MinRPCInterval != 20*time.Minute {
		t.Errorf("second empty reply should wait 20 minutes, got %s", ps.MinRPCInterval)
	}
	work = true
	e.doRPC(ps)
	if ps.NoWorkStreak != 0 || ps.MinRPCInterval != 30*time.Second {
		t.Errorf("getting work must reset the back-off, got streak %d interval %s", ps.NoWorkStreak, ps.MinRPCInterval)
	}
}

func TestAFinishedResultIsNotHeldBackByTheBackoff(t *testing.T) {
	rs := []ResultInfo{{ProjectURL: "p", State: 4, ReadyToReport: 1}}
	if !hasFinishedResult(rs, "p") || hasFinishedResult(rs, "other") {
		t.Error("finished result detection wrong")
	}
	if reportRetryInterval(30*time.Second) != 30*time.Second {
		t.Error("a waiting report must not wait out a no-work back-off")
	}
}

func TestNoMoreWorkAsksForNoWorkButStillReports(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, "<scheduler_reply></scheduler_reply>")
	}))
	defer srv.Close()
	fs := newFakeState(ProjectInfo{URL: srv.URL, Name: "P", DontRequestMoreWork: 1})
	fs.results = []ResultInfo{{Name: "done", ProjectURL: srv.URL, State: 4, ReadyToReport: 1}}
	e := NewEngine(fs, fakeCache{}, EngineConfig{})
	e.doRPC(&ProjectState{URL: srv.URL})
	if !strings.Contains(body, "<work_req_seconds>0</work_req_seconds>") || !strings.Contains(body, "<name>done</name>") {
		t.Errorf("must report the finished result and request no work:\n%s", body)
	}
}
