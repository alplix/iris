package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInitCreatesTheProjectDirAndItsSubdirs guards against a regression where
// Init() passed its own already-absolute base path as a second argument to
// filepath.Join(pd.base, d) — which on Windows produced a literal "C:"
// subdirectory (the drive letter of the duplicated absolute path) and failed
// with "The filename, directory name, or volume label syntax is incorrect."
func TestInitCreatesTheProjectDirAndItsSubdirs(t *testing.T) {
	dataDir := t.TempDir()
	pd := NewDir(dataDir, "https://einstein.phys.uwm.edu/")

	if err := pd.Init(); err != nil {
		t.Fatalf("Init() = %v", err)
	}

	info, err := os.Stat(pd.Path())
	if err != nil || !info.IsDir() {
		t.Fatalf("project dir %q was not created: %v", pd.Path(), err)
	}

	for _, sub := range []string{"slots", "apps", "templates", "download", "upload"} {
		p := filepath.Join(pd.Path(), sub)
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("expected subdirectory %q to exist: %v", p, err)
		}
	}

	entries, err := os.ReadDir(pd.Path())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"slots": true, "apps": true, "templates": true, "download": true, "upload": true}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("unexpected entry %q in project dir (regression: a stray subdirectory from double-joining the base path)", e.Name())
		}
	}
}

func TestNewDirHashesTheURLIntoTheBasePath(t *testing.T) {
	dataDir := t.TempDir()
	pd := NewDir(dataDir, "https://einstein.phys.uwm.edu/")
	want := filepath.Join(dataDir, "projects", urlHash("https://einstein.phys.uwm.edu/"))
	if pd.Path() != want {
		t.Errorf("Path() = %q, want %q", pd.Path(), want)
	}
}
