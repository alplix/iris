package state

import "testing"

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
	if len(got.Credits) != 1 || len(got.Xfers) != 1 {
		t.Errorf("history lost: credits=%d xfers=%d", len(got.Credits), len(got.Xfers))
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
