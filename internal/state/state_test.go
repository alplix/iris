package state

import (
	"testing"
	"time"
)

func TestUpdateProjectCreditWritesProjectAndHistory(t *testing.T) {
	s := New(t.TempDir())
	s.AddProject(Project{Name: "P", MasterURL: "https://p.example"})

	s.UpdateProjectCredit("https://p.example", 100, 5, 40, 2)
	s.UpdateProjectCredit("https://p.example", 150, 6, 60, 3) // same day: replaces

	p := s.Snapshot().Projects[0]
	if p.UserTotalCredit != 150 || p.UserExpavgCredit != 6 || p.HostTotalCredit != 60 || p.HostExpavgCredit != 3 {
		t.Fatalf("project credit not stored: %+v", p)
	}
	hist := s.Snapshot().Credits
	if len(hist) != 1 || hist[0].HostTotal != 60 {
		t.Fatalf("history should hold one entry per day, got %+v", hist)
	}

	// Credit for a project that was detached in the meantime must not panic.
	s.UpdateProjectCredit("https://gone.example", 1, 1, 1, 1)
}

func TestAddXferAccumulatesPerDay(t *testing.T) {
	s := New(t.TempDir())
	s.AddXfer(false, 1000)
	s.AddXfer(false, 500)
	s.AddXfer(true, 200)
	s.AddXfer(true, 0) // ignored
	x := s.Snapshot().Xfers
	if len(x) != 1 || x[0].Down != 1500 || x[0].Up != 200 {
		t.Fatalf("xfers = %+v", x)
	}
}

func TestTransferLifecycle(t *testing.T) {
	s := New(t.TempDir())
	s.BeginTransfer(Xfer{Name: "a", Nbytes: 10})
	s.SetTransferProgress("a", 4, 20)
	if x := s.Snapshot().Transfers[0]; x.BytesXferred != 4 || x.Nbytes != 20 {
		t.Fatalf("progress not recorded: %+v", x)
	}
	s.EndTransfer("a", false)
	if !s.TransferFailed("a") {
		t.Fatal("failed transfer should stay, paused")
	}
	s.BeginTransfer(Xfer{Name: "a"}) // a retry replaces the failed entry
	if s.TransferFailed("a") || len(s.Snapshot().Transfers) != 1 {
		t.Fatal("restarting a transfer should replace the failed one")
	}
	s.EndTransfer("a", true)
	if len(s.Snapshot().Transfers) != 0 {
		t.Fatal("a finished transfer should disappear")
	}
}

func TestLoadDropsStaleTransfersButKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	s.AddProject(Project{MasterURL: "u"})
	s.UpdateProjectCredit("u", 1, 1, 1, 1)
	s.AddXfer(false, 10)
	s.RecordTaskDay("u", true, 5)
	s.BeginTransfer(Xfer{Name: "stale"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	n := New(dir)
	if err := n.Load(); err != nil {
		t.Fatal(err)
	}
	got := n.Snapshot()
	if len(got.Transfers) != 0 {
		t.Errorf("stale transfers survived a restart: %+v", got.Transfers)
	}
	if len(got.Credits) != 1 || len(got.Xfers) != 1 || len(got.TaskDays) != 1 {
		t.Errorf("history lost: credits=%d xfers=%d taskDays=%d", len(got.Credits), len(got.Xfers), len(got.TaskDays))
	}
}

func TestRecordTaskDayAccumulatesPerProjectPerDay(t *testing.T) {
	s := New(t.TempDir())
	s.RecordTaskDay("https://einsteinathome.org", true, 100)
	s.RecordTaskDay("https://einsteinathome.org", true, 120)
	s.RecordTaskDay("https://einsteinathome.org", false, 30)
	s.RecordTaskDay("https://boinc.bakerlab.org/rosetta", true, 50)
	s.RecordTaskDay("", true, 999) // no project URL: must be ignored, not panic

	days := s.Snapshot().TaskDays
	if len(days) != 2 {
		t.Fatalf("got %d project/day buckets, want 2: %+v", len(days), days)
	}
	var einstein TaskDay
	for _, d := range days {
		if d.URL == "https://einsteinathome.org" {
			einstein = d
		}
	}
	if einstein.Success != 2 || einstein.Error != 1 || einstein.CPUTime != 250 {
		t.Errorf("einstein day = %+v, want success=2 error=1 cpu=250", einstein)
	}
}

func TestRecordTaskDayKeepsProjectsIndependent(t *testing.T) {
	s := New(t.TempDir())
	s.RecordTaskDay("https://a.example", true, 1)
	s.RecordTaskDay("https://b.example", false, 1)
	days := s.Snapshot().TaskDays
	if len(days) != 2 {
		t.Fatalf("got %d buckets, want 2 (one per project): %+v", len(days), days)
	}
}

func TestResetResultsWithFile(t *testing.T) {
	s := New(t.TempDir())
	s.AddResult(Result{Name: "r1", State: 5, ExitStatus: 1, ReadyToReport: 1, Files: []FileInfo{{Name: "f"}}})
	s.AddResult(Result{Name: "r2", State: 4, Files: []FileInfo{{Name: "f"}}}) // not in error
	s.AddResult(Result{Name: "r3", State: 5, Files: []FileInfo{{Name: "other"}}})
	if n := s.ResetResultsWithFile("f", 5, 0); n != 1 {
		t.Fatalf("reset %d results, want 1", n)
	}
	for _, r := range s.Snapshot().Results {
		switch r.Name {
		case "r1":
			if r.State != 0 || r.ExitStatus != 0 || r.ReadyToReport != 0 {
				t.Errorf("r1 not reset: %+v", r)
			}
		case "r2":
			if r.State != 4 {
				t.Errorf("r2 must be untouched: %+v", r)
			}
		case "r3":
			if r.State != 5 {
				t.Errorf("r3 must be untouched: %+v", r)
			}
		}
	}
}

// After a restart, new messages must be numbered after the saved ones; they
// used to start again at 1, so a manager that had seen message 800 never
// showed anything new (the event log looked frozen at the last run's end).
func TestMessageNumbersContinueAfterARestart(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	s.AddMessage("old 1", "", 1)
	s.AddMessage("old 2", "", 1)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := New(dir)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	s2.AddMessage("new", "", 1)
	got := s2.GetMessages(2)
	if len(got) != 1 || got[0].Body != "new" {
		t.Fatalf("a manager that has seen message 2 must get the new one, got %+v", got)
	}
}

func TestRetryDownloadFailuresRequeuesOnlyUnreportedOnes(t *testing.T) {
	s := New(t.TempDir())
	s.AddResult(Result{Name: "a", State: 5, ExitStatus: -186, ReadyToReport: 1})
	s.AddResult(Result{Name: "b", State: 5, ExitStatus: -163, ReadyToReport: 1})
	s.AddResult(Result{Name: "c", State: 4, ExitStatus: 0, ReadyToReport: 1})
	if n := s.RetryDownloadFailures(-186); n != 1 {
		t.Fatalf("re-queued %d, want 1", n)
	}
	for _, r := range s.Results {
		switch r.Name {
		case "a":
			if r.State != 0 || r.ExitStatus != 0 || r.ReadyToReport != 0 {
				t.Errorf("a not re-queued: %+v", r)
			}
		case "b", "c":
			if r.ReadyToReport != 1 {
				t.Errorf("%s must be left alone: %+v", r.Name, r)
			}
		}
	}
}

func TestAddEnergyAccumulatesPerDayAndFlagsModelledGPU(t *testing.T) {
	s := New(t.TempDir())
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	s.AddEnergy(now, 10, 20, true)
	s.AddEnergy(now.Add(time.Hour), 5, 30, false)
	s.AddEnergy(now.Add(48*time.Hour), 1, 0, true)
	s.AddEnergy(now, 0, 0, true) // nothing to add
	if len(s.Energy) != 2 {
		t.Fatalf("want 2 days, got %+v", s.Energy)
	}
	if e := s.Energy[0]; e.CPUWh != 15 || e.GPUWh != 50 || e.GPUEstWh != 30 {
		t.Errorf("day totals wrong: %+v", e)
	}
}
