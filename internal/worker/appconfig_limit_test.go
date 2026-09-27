package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alplix/iris/internal/project"
)

func TestApplyAppConfigLimitsProjectAndPerApp(t *testing.T) {
	dataDir := t.TempDir()
	const url = "https://p.example/"
	dir := filepath.Join(dataDir, "projects", project.DirName(url))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app_config.xml"), []byte(`<app_config>
 <app><name>slow_app</name><max_concurrent>1</max_concurrent></app>
 <project_max_concurrent>2</project_max_concurrent>
</app_config>`), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &Engine{cfg: Config{DataDir: dataDir}, state: newMutableState(nil)}

	eligible := []ResultSnapshot{
		{Name: "a1", ProjectURL: url, AppName: "slow_app"},
		{Name: "a2", ProjectURL: url, AppName: "slow_app"}, // over the app's own cap of 1
		{Name: "b1", ProjectURL: url, AppName: "other_app"},
		{Name: "b2", ProjectURL: url, AppName: "other_app"}, // pushes the project over 2
	}
	got := e.applyAppConfigLimits(eligible, nil, nil)
	var names []string
	for _, r := range got {
		names = append(names, r.Name)
	}
	if want := []string{"a1", "b1"}; !equalNames(names, want) {
		t.Errorf("got %v, want %v (slow_app capped at 1, project capped at 2 total)", names, want)
	}

	// What is already running counts against both caps too.
	got = e.applyAppConfigLimits(eligible, map[string]int{url: 2}, nil)
	if len(got) != 0 {
		t.Errorf("the project is already at its cap of 2, nothing should be admitted, got %v", got)
	}
	got = e.applyAppConfigLimits(eligible, nil, map[string]int{url + "\x00slow_app": 1})
	if len(got) != 2 || got[0].Name != "b1" || got[1].Name != "b2" {
		t.Errorf("slow_app is already at its own cap, only other_app tasks should pass, got %+v", got)
	}
}

func TestApplyAppConfigLimitsWithoutAFilePassesEverythingThrough(t *testing.T) {
	e := &Engine{cfg: Config{DataDir: t.TempDir()}, state: newMutableState(nil)}
	eligible := []ResultSnapshot{{Name: "a", ProjectURL: "https://none.example/"}}
	got := e.applyAppConfigLimits(eligible, nil, nil)
	if len(got) != 1 {
		t.Errorf("no app_config.xml means no limiting, got %v", got)
	}
}

func equalNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
