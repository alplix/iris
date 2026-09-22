package prefs

import (
	"strings"
	"testing"
)

func TestSetPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if len(s.Get()) != 0 {
		t.Fatal("new store should be empty")
	}
	if err := s.Set([][2]string{{"max_ncpus_pct", "50"}, {"work_buf_min_days", "0.5"}, {"empty", ""}}); err != nil {
		t.Fatal(err)
	}
	got := Open(dir).Get()
	if len(got) != 2 || got["max_ncpus_pct"] != "50" || got["work_buf_min_days"] != "0.5" {
		t.Fatalf("reloaded overrides = %v", got)
	}
	if err := s.Set(nil); err != nil {
		t.Fatal(err)
	}
	if len(Open(dir).Get()) != 0 {
		t.Fatal("empty Set should clear every override")
	}
}

func TestSetRejectsBadInput(t *testing.T) {
	s := Open(t.TempDir())
	for _, k := range []string{"", "1abc", "a b", "a>b", "<x/>"} {
		if err := s.Set([][2]string{{k, "1"}}); err == nil {
			t.Errorf("key %q should be rejected", k)
		}
	}
	if err := s.Set([][2]string{{"k", strings.Repeat("x", maxValueLen+1)}}); err == nil {
		t.Error("oversized value should be rejected")
	}
}

func TestXMLEscapesValues(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.Set([][2]string{{"note", "a<b&c"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(s.XML()), "<note>a&lt;b&amp;c</note>") {
		t.Fatalf("value not escaped: %s", s.XML())
	}
	if Open(s.path[:len(s.path)-len(fileName)]).Get()["note"] != "a<b&c" {
		t.Fatal("escaped value should round-trip")
	}
}

func TestBoolReadsTruthyValuesAndDefaultsFalse(t *testing.T) {
	s := Open(t.TempDir())
	if s.Bool(RealAppsKey) {
		t.Error("an unset key should default to false")
	}
	s.Set([][2]string{{RealAppsKey, "1"}})
	if !s.Bool(RealAppsKey) {
		t.Error("1 should be truthy")
	}
	s.Set([][2]string{{RealAppsKey, "0"}})
	if s.Bool(RealAppsKey) {
		t.Error("0 should not be truthy")
	}
}

func TestMaxCPUs(t *testing.T) {
	s := Open(t.TempDir())
	if got := s.MaxCPUs(16); got != 16 {
		t.Errorf("default = %d, want 16", got)
	}
	s.Set([][2]string{{"max_ncpus_pct", "25"}})
	if got := s.MaxCPUs(16); got != 4 {
		t.Errorf("25%% of 16 = %d, want 4", got)
	}
	s.Set([][2]string{{"max_ncpus_pct", "50"}, {"max_ncpus", "3"}})
	if got := s.MaxCPUs(16); got != 3 {
		t.Errorf("max_ncpus should win: got %d, want 3", got)
	}
	s.Set([][2]string{{"max_ncpus_pct", "1"}})
	if got := s.MaxCPUs(4); got != 1 {
		t.Errorf("never below 1: got %d", got)
	}
}
