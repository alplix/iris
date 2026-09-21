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
