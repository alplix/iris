package worker

import "testing"

func names(rs []ResultSnapshot) map[string]bool {
	m := make(map[string]bool, len(rs))
	for _, r := range rs {
		m[r.Name] = true
	}
	return m
}

func TestPickTasksToStartSingleProjectCapsAtSlotsAndQueueLength(t *testing.T) {
	items := []ResultSnapshot{
		{Name: "a", ProjectURL: "p1"}, {Name: "b", ProjectURL: "p1"}, {Name: "c", ProjectURL: "p1"},
	}
	if got := pickTasksToStart(items, 2); len(got) != 2 {
		t.Fatalf("want 2 picked, got %d: %v", len(got), got)
	}
	if got := pickTasksToStart(items, 10); len(got) != 3 {
		t.Fatalf("want capped at queue length 3, got %d", len(got))
	}
	if got := pickTasksToStart(items, 0); got != nil {
		t.Fatalf("0 slots should pick nothing, got %v", got)
	}
}

// TestPickTasksToStartDoesNotStarveASmallerQueue is the actual bug this
// exists to fix: a project with a deep backlog must not indefinitely starve
// a second, equally-shared project that only has a couple of tasks queued —
// a plain FIFO over all results would do exactly that.
func TestPickTasksToStartDoesNotStarveASmallerQueue(t *testing.T) {
	var items []ResultSnapshot
	for i := 0; i < 20; i++ {
		items = append(items, ResultSnapshot{Name: "deep", ProjectURL: "big"})
	}
	items = append(items, ResultSnapshot{Name: "shallow1", ProjectURL: "small"}, ResultSnapshot{Name: "shallow2", ProjectURL: "small"})

	picked := pickTasksToStart(items, 4)
	got := names(picked)
	if !got["shallow1"] || !got["shallow2"] {
		t.Errorf("the small project's own 2 tasks should both be picked (equal share), got %v", got)
	}
	if len(picked) != 4 {
		t.Errorf("want exactly 4 picked, got %d: %v", len(picked), picked)
	}
}

func TestPickTasksToStartRespectsDifferingResourceShares(t *testing.T) {
	var items []ResultSnapshot
	for i := 0; i < 10; i++ {
		items = append(items, ResultSnapshot{Name: "hi", ProjectURL: "high-share", ResourceShare: 300})
	}
	for i := 0; i < 10; i++ {
		items = append(items, ResultSnapshot{Name: "lo", ProjectURL: "low-share", ResourceShare: 100})
	}

	got := pickTasksToStart(items, 8)
	hi, lo := 0, 0
	for _, r := range got {
		if r.ProjectURL == "high-share" {
			hi++
		} else {
			lo++
		}
	}
	// 300:100 share ratio over 8 slots should give roughly 6:2.
	if hi != 6 || lo != 2 {
		t.Errorf("want a 6:2 split for a 300:100 share ratio over 8 slots, got %d:%d", hi, lo)
	}
}

func TestPickTasksToStartGivesUnclaimedSlotsToWhoeverStillHasWork(t *testing.T) {
	items := []ResultSnapshot{
		{Name: "only-one", ProjectURL: "small", ResourceShare: 100},
		{Name: "d1", ProjectURL: "big", ResourceShare: 100},
		{Name: "d2", ProjectURL: "big", ResourceShare: 100},
		{Name: "d3", ProjectURL: "big", ResourceShare: 100},
	}
	// 4 slots, equal share: exact split would be 2/2, but "small" only has
	// 1 task — its other slot must go to "big" rather than being wasted.
	got := pickTasksToStart(items, 4)
	if len(got) != 4 {
		t.Fatalf("all 4 queued tasks should be picked when slots allow it, got %d: %v", len(got), got)
	}
}

func TestPickTasksToStartTreatsAnUnsetShareAsTheBoincDefaultOfOneHundred(t *testing.T) {
	items := []ResultSnapshot{
		{Name: "a", ProjectURL: "p1", ResourceShare: 0},
		{Name: "b", ProjectURL: "p2", ResourceShare: 100},
	}
	got := pickTasksToStart(items, 2)
	if len(got) != 2 {
		t.Errorf("an unset (zero) share must not be treated as zero weight, got %v", got)
	}
}
