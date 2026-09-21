package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alplix/iris/internal/boinc"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	if got := loadSettingsFrom(path); got.Lang != "" {
		t.Fatalf("missing file should give empty settings, got %+v", got)
	}
	if err := saveSettingsTo(path, Settings{Lang: "tr"}); err != nil {
		t.Fatal(err)
	}
	if got := loadSettingsFrom(path); got.Lang != "tr" {
		t.Fatalf("Lang = %q, want tr", got.Lang)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadSettingsFrom(path); got.Lang != "" {
		t.Fatalf("corrupt file should give empty settings, got %+v", got)
	}
}

func TestStatDayAcceptsSecondsAndDays(t *testing.T) {
	// 2026-01-02 00:00:00 UTC
	const sec = 1767312000
	if got := statDay(sec); got != "20260102" {
		t.Errorf("seconds: got %s", got)
	}
	if got := statDay(sec / 86400); got != "20260102" {
		t.Errorf("days: got %s", got)
	}
}

func TestDailyStatCumulativeDetection(t *testing.T) {
	if (boinc.DailyStat{TotalCredit: 5}).Cumulative() {
		t.Error("legacy per-day entries are not cumulative")
	}
	if !(boinc.DailyStat{HostTotalCredit: 5}).Cumulative() || !(boinc.DailyStat{UserTotalCredit: 5}).Cumulative() {
		t.Error("BOINC entries with host/user totals are cumulative")
	}
}

func TestManagerStartsEmptyAndDropsOldDemoHosts(t *testing.T) {
	t.Setenv("IRIS_DEMO", "")
	path := filepath.Join(t.TempDir(), "hosts.json")
	s := &Store{path: path}
	s.Upsert(HostCfg{Name: "Demo Server", Host: "localhost", Port: 31418, Demo: true})
	s.Upsert(HostCfg{Name: "My PC", Host: "192.168.1.5", Port: 31418})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	m := newManagerWith(LoadStoreFrom(path))
	hosts := m.Store.List()
	if len(hosts) != 1 || hosts[0].Name != "My PC" {
		t.Fatalf("hosts = %+v, want only the user's own", hosts)
	}
	if again := LoadStoreFrom(path).List(); len(again) != 1 {
		t.Fatalf("the cleanup must be saved, got %+v", again)
	}

	empty := newManagerWith(&Store{path: filepath.Join(t.TempDir(), "h.json")})
	if len(empty.Store.List()) != 0 {
		t.Fatal("a fresh manager must not invent any server")
	}
}

func TestDemoHostIsOptIn(t *testing.T) {
	t.Setenv("IRIS_DEMO", "1")
	m := newManagerWith(&Store{path: filepath.Join(t.TempDir(), "h.json")})
	hosts := m.Store.List()
	if len(hosts) != 1 || !hosts[0].Demo {
		t.Fatalf("IRIS_DEMO=1 should add exactly one demo host, got %+v", hosts)
	}
}

func TestUpsertKeepsAGivenID(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "h.json")}
	s.Upsert(HostCfg{ID: "local-iris", Name: "Local Iris"})
	if h, ok := s.Get("local-iris"); !ok || h.Name != "Local Iris" {
		t.Fatalf("host with a chosen ID was not stored under it: %+v", s.List())
	}
}
