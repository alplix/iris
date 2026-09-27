package project

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppConfig is a project's app_config.xml (lib/cc_config.h's APP_CONFIGS):
// a person's own limit on how many jobs of a project — or of one named
// application within it — may run at once. Iris supports the widely-used
// subset of the format: <project_max_concurrent> and each <app>'s
// <max_concurrent>. The <app_version>, <gpu_versions>, <fraction_done_exact>
// and <report_results_immediately> elements are accepted (never an error to
// have them) but have no effect yet.
type AppConfig struct {
	// ProjectMaxConcurrent caps how many of this project's jobs may run at
	// once, across every application; 0 means no project-wide cap.
	ProjectMaxConcurrent int
	// Apps maps an application's name (as the workunit names it) to its own
	// concurrent-jobs cap; an app missing from the map, or mapped to 0, has
	// no cap of its own.
	Apps map[string]int
}

type appConfigXML struct {
	XMLName xml.Name `xml:"app_config"`
	Apps    []struct {
		Name          string `xml:"name"`
		MaxConcurrent int    `xml:"max_concurrent"`
	} `xml:"app"`
	ProjectMaxConcurrent int `xml:"project_max_concurrent"`
}

// LoadAppConfig reads <dir>/app_config.xml. It returns (nil, nil) when the
// file does not exist, and an error naming the problem when it is unusable;
// an <app> with an empty name is skipped rather than treated as an error,
// matching BOINC's own tolerant parsing.
func LoadAppConfig(dir string) (*AppConfig, error) {
	if dir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "app_config.xml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var x appConfigXML
	if err := xml.Unmarshal(data, &x); err != nil {
		return nil, fmt.Errorf("app_config.xml: %w", err)
	}
	ac := &AppConfig{ProjectMaxConcurrent: x.ProjectMaxConcurrent, Apps: map[string]int{}}
	for _, a := range x.Apps {
		if name := strings.TrimSpace(a.Name); name != "" {
			ac.Apps[name] = a.MaxConcurrent
		}
	}
	return ac, nil
}
