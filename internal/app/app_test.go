package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeStatuses(t *testing.T) {
	cases := []struct {
		st   TaskStatus
		want TaskStatus
	}{
		{StatusRunning, StatusRunning},
		{StatusPaused, StatusPaused},
		{StatusError, StatusError},
		{StatusQueued, StatusQueued},
		{StatusReady, StatusReady},
	}
	for _, c := range cases {
		m := NewMock(HostCfg{ID: "t", Name: "Test"})
		if len(m.tasks) == 0 {
			t.Fatal("mock created no tasks")
		}
		mt := m.tasks[0]
		mt.t.Status = c.st
		mt.t.Active = c.st == StatusRunning || c.st == StatusPaused
		mt.t.Suspended = c.st == StatusPaused
		mt.t.Ready = c.st == StatusReady
		snap := m.Snapshot()
		if len(snap.Tasks) == 0 {
			t.Fatalf("expected mock tasks, got none")
		}
		if snap.Tasks[0].Status != c.want {
			t.Fatalf("status %q -> got %q, want %q", c.st, snap.Tasks[0].Status, c.want)
		}
	}
}

func TestMockSnapshotHasData(t *testing.T) {
	m := NewMock(HostCfg{ID: "demo", Name: "Demo"})
	snap := m.Snapshot()
	if !snap.Online {
		t.Fatal("demo mock should report online")
	}
	if len(snap.Projects) == 0 {
		t.Error("demo mock should expose projects")
	}
	if len(snap.Tasks) == 0 {
		t.Error("demo mock should expose tasks")
	}
	if len(snap.Transfers) == 0 {
		t.Error("demo mock should expose at least one transfer")
	}
	if len(snap.Messages) == 0 {
		t.Error("demo mock should expose messages")
	}
	if snap.Totals.Running == 0 {
		t.Error("demo mock should have running tasks")
	}
	if snap.Totals.Credit <= 0 {
		t.Error("demo mock should aggregate credit")
	}
	if len(snap.HostInfo.GPUs) == 0 {
		t.Error("demo mock should report a GPU")
	}
	if snap.Version == "" {
		t.Error("demo mock should report a version")
	}
}

func TestMockOps(t *testing.T) {
	m := NewMock(HostCfg{ID: "demo", Name: "Demo"})
	snap := m.Snapshot()
	if len(snap.Tasks) == 0 {
		t.Fatal("no tasks to operate on")
	}
	name := snap.Tasks[0].Name
	if err := m.ResultOp(name, "suspend"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if err := m.ResultOp(name, "resume"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(snap.Projects) == 0 {
		t.Fatal("no projects to operate on")
	}
	purl := snap.Projects[0].URL
	if err := m.ProjectOp(purl, "suspend"); err != nil {
		t.Fatalf("project suspend: %v", err)
	}
	if err := m.ProjectOp(purl, "resume"); err != nil {
		t.Fatalf("project resume: %v", err)
	}
	if err := m.ProjectOp(purl, "detach"); err != nil {
		t.Fatalf("project detach: %v", err)
	}
	if st, err := m.Stats(); err != nil || len(st) == 0 {
		t.Fatalf("stats: %v, n=%d", err, len(st))
	}
}

func TestMsgTimeIsUnix(t *testing.T) {
	m := NewMock(HostCfg{ID: "demo", Name: "Demo"})
	snap := m.Snapshot()
	for _, msg := range snap.Messages {
		if msg.Time <= 0 {
			t.Fatalf("message time should be a unix timestamp, got %v", msg.Time)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Store{path: filepath.Join(dir, "hosts.json")}
	h := HostCfg{Name: "A", Host: "localhost", Port: 31416, Password: "pw"}
	h = s.Upsert(h)
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	s2 := &Store{path: filepath.Join(dir, "hosts.json")}
	if data, err := os.ReadFile(s2.path); err == nil {
		_ = json.Unmarshal(data, &s2.hosts)
	}
	list := s2.List()
	if len(list) != 1 || list[0].ID != h.ID || list[0].Password != "pw" {
		t.Fatalf("round trip mismatch: %+v", list)
	}
}

func TestFmtHelpers(t *testing.T) {
	if FmtBytes(1024) != "1.0 KB" {
		t.Errorf("FmtBytes: %s", FmtBytes(1024))
	}
	if FmtDuration(90) != "1m 30s" {
		t.Errorf("FmtDuration: %s", FmtDuration(90))
	}
	if FmtNum(5e6) != "5.00 M" {
		t.Errorf("FmtNum: %s", FmtNum(5e6))
	}
}

// TestSnapshotJSONKeys locks in the JSON contract the frontend depends on:
// the whole API surface must serialize to stable camelCase keys.
func TestSnapshotJSONKeys(t *testing.T) {
	m := NewMock(HostCfg{ID: "contract", Name: "Contract"})
	snap := m.Snapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"hostId", "demo", "online", "error", "version", "ts", "hostInfo", "projects", "tasks", "transfers", "messages", "totals", "taskMode", "netMode"}
	for _, k := range want {
		if _, ok := obj[k]; !ok {
			t.Errorf("snapshot JSON missing key %q", k)
		}
	}
	for k := range obj {
		found := false
		for _, w := range want {
			if k == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected snapshot JSON key %q (contract drift)", k)
		}
	}
	var tot map[string]json.RawMessage
	if err := json.Unmarshal(obj["totals"], &tot); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"running", "paused", "queued", "errors", "downloads", "uploads", "memory", "credit", "rac"} {
		if _, ok := tot[k]; !ok {
			t.Errorf("totals JSON missing key %q", k)
		}
	}
	var msgs []json.RawMessage
	if err := json.Unmarshal(obj["messages"], &msgs); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("contract mock should have messages")
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(msgs[0], &msg); err != nil {
		t.Fatalf("decode first message: %v", err)
	}
	if raw, ok := msg["time"]; !ok || len(raw) == 0 {
		t.Error("message JSON is missing numeric `time` key")
	}
}

func TestClientOpAll(t *testing.T) {
	dir := t.TempDir()
	m := newManagerWith(&Store{path: filepath.Join(dir, "hosts.json")})
	m.Store.Upsert(HostCfg{Name: "Demo A", Host: "localhost", Port: 31416, Demo: true})
	m.Store.Upsert(HostCfg{Name: "Demo B", Host: "localhost", Port: 31416, Demo: true})
	failed := m.ClientOpAll("setRunMode", "always")
	if len(failed) != 0 {
		t.Fatalf("unexpected failures: %v", failed)
	}
	if n := m.RefreshAll(); n != 2 {
		t.Fatalf("RefreshAll returned %d, want 2", n)
	}
}

// Manager.Stats carries task-completion counts through to StatPoint, the data
// behind questions like "how many tasks did I finish today".
func TestManagerStatsCarriesTaskCounts(t *testing.T) {
	dir := t.TempDir()
	m := newManagerWith(&Store{path: filepath.Join(dir, "hosts.json")})
	h := m.Store.Upsert(HostCfg{Name: "Demo", Host: "localhost", Port: 31416, Demo: true})

	series, err := m.Stats(h.ID)
	if err != nil || len(series) == 0 {
		t.Fatalf("Stats: %v, n=%d", err, len(series))
	}
	var sawSuccess bool
	for _, ss := range series {
		for _, pt := range ss.Daily {
			if pt.TasksSuccess > 0 {
				sawSuccess = true
			}
			if pt.TasksSuccess < 0 || pt.TasksError < 0 {
				t.Fatalf("negative task count in %+v", pt)
			}
		}
	}
	if !sawSuccess {
		t.Fatal("no day had any successful task; demo data should always have some")
	}
}
