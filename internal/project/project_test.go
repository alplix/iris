package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Init once passed its own already-absolute base path as a second argument to
// filepath.Join, which on Windows produced a literal "C:" subdirectory. The
// folder now holds only what belongs there, so any stray entry is a bug.
func TestInitCreatesOnlyTheProjectFolder(t *testing.T) {
	dataDir := t.TempDir()
	pd := NewDir(dataDir, "https://einstein.phys.uwm.edu/")
	if err := pd.Init(); err != nil {
		t.Fatalf("Init() = %v", err)
	}
	info, err := os.Stat(pd.Path())
	if err != nil || !info.IsDir() {
		t.Fatalf("project dir %q was not created: %v", pd.Path(), err)
	}
	entries, err := os.ReadDir(pd.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a new project folder should start empty, found %d entries", len(entries))
	}
}

func TestDirNameIsReadable(t *testing.T) {
	for url, want := range map[string]string{
		"https://einstein.phys.uwm.edu/":             "einstein.phys.uwm.edu",
		"https://asteroidsathome.net/boinc/":         "asteroidsathome.net_boinc",
		"http://Example.org:8080/a/b?x=1":            "example.org_8080_a_b",
		"https://numberfields.asu.edu/NumberFields/": "numberfields.asu.edu_numberfields",
	} {
		if got := DirName(url); got != want {
			t.Errorf("DirName(%q) = %q, want %q", url, got, want)
		}
	}
	if DirName("") == "" || strings.ContainsAny(DirName("https://x.y/../../etc"), `/\`) {
		t.Error("a name must never be empty or contain a path separator")
	}
}

func TestNewDirUsesTheReadableName(t *testing.T) {
	dataDir := t.TempDir()
	pd := NewDir(dataDir, "https://einstein.phys.uwm.edu/")
	if want := filepath.Join(dataDir, "projects", "einstein.phys.uwm.edu"); pd.Path() != want {
		t.Errorf("Path() = %q, want %q", pd.Path(), want)
	}
}

func TestMigrateMovesTheOldHashFolder(t *testing.T) {
	dataDir := t.TempDir()
	const u = "https://einstein.phys.uwm.edu/"
	old := filepath.Join(dataDir, "projects", urlHash(u))
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(old, "account.xml"), []byte("x"), 0o644)
	got, err := Migrate(dataDir, u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got, "account.xml")); err != nil {
		t.Errorf("the old folder's files must follow the move: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the hash-named folder should be gone")
	}
	// A second call changes nothing.
	if again, err := Migrate(dataDir, u); err != nil || again != got {
		t.Errorf("Migrate should be idempotent: %v %q", err, again)
	}
}

func TestLoadAppInfo(t *testing.T) {
	dir := t.TempDir()
	if ai, err := LoadAppInfo(dir); ai != nil || err != nil {
		t.Fatalf("no file must mean (nil, nil), got %v %v", ai, err)
	}
	write := func(s string) { os.WriteFile(filepath.Join(dir, "app_info.xml"), []byte(s), 0o644) }
	write(`<app_info>
 <app><name>myapp</name></app>
 <file_info><name>myapp.exe</name><executable/></file_info>
 <app_version><app_name>myapp</app_name><version_num>105</version_num><cmdline>--fast</cmdline>
  <file_ref><file_name>myapp.exe</file_name><main_program/></file_ref>
  <coproc><type>NVIDIA</type><count>1</count></coproc>
 </app_version>
</app_info>`)
	ai, err := LoadAppInfo(dir)
	if err != nil || ai == nil {
		t.Fatalf("valid file rejected: %v", err)
	}
	v, ok := ai.Find("myapp", 105, "")
	if !ok || v.CmdLine != "--fast" || !v.IsGPU() || v.FileRefs[0].FileName != "myapp.exe" {
		t.Errorf("parsed wrongly: %+v ok=%v", v, ok)
	}
	if _, ok := ai.Find("myapp", 999, ""); ok {
		t.Error("an unknown version must not match")
	}
	write(`<app_info><app_version><app_name>x</app_name><version_num>1</version_num></app_version></app_info>`)
	if _, err := LoadAppInfo(dir); err == nil || !strings.Contains(err.Error(), "main_program") {
		t.Errorf("a version with no main program must be reported, got %v", err)
	}
	write(`<app_info><app_version><app_name>x</app_name><version_num>1</version_num><file_ref><file_name>../evil</file_name><main_program/></file_ref></app_version></app_info>`)
	if _, err := LoadAppInfo(dir); err == nil {
		t.Error("a file name with a path must be refused")
	}
}
