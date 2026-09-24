package project

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppInfo is a project's app_info.xml: the person's own application builds
// ("anonymous platform" in BOINC's terms). When the file is present in a
// project's folder, Iris tells the project it runs these applications instead
// of downloading the project's own, and uses the files sitting in that folder.
type AppInfo struct {
	XMLName  xml.Name         `xml:"app_info"`
	Files    []AppInfoFile    `xml:"file_info"`
	Versions []AppInfoVersion `xml:"app_version"`
}

type AppInfoFile struct {
	Name       string    `xml:"name"`
	Executable *struct{} `xml:"executable"`
}

type AppInfoVersion struct {
	AppName    string           `xml:"app_name"`
	VersionNum int              `xml:"version_num"`
	PlanClass  string           `xml:"plan_class"`
	AvgNCPUs   float64          `xml:"avg_ncpus"`
	MaxNCPUs   float64          `xml:"max_ncpus"`
	Flops      float64          `xml:"flops"`
	CmdLine    string           `xml:"cmdline"`
	FileRefs   []AppInfoFileRef `xml:"file_ref"`
	Coproc     *AppInfoCoproc   `xml:"coproc"`
}

type AppInfoFileRef struct {
	FileName    string    `xml:"file_name"`
	OpenName    string    `xml:"open_name"`
	MainProgram *struct{} `xml:"main_program"`
}

type AppInfoCoproc struct {
	Type  string  `xml:"type"`
	Count float64 `xml:"count"`
}

// IsGPU reports whether the application version needs a GPU.
func (v AppInfoVersion) IsGPU() bool { return v.Coproc != nil && v.Coproc.Count > 0 }

// LoadAppInfo reads <dir>/app_info.xml. It returns (nil, nil) when the file
// does not exist, and an error naming the problem when it is unusable.
func LoadAppInfo(dir string) (*AppInfo, error) {
	if dir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "app_info.xml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ai AppInfo
	if err := xml.Unmarshal(data, &ai); err != nil {
		return nil, fmt.Errorf("app_info.xml: %w", err)
	}
	if len(ai.Versions) == 0 {
		return nil, fmt.Errorf("app_info.xml has no <app_version>")
	}
	for i, v := range ai.Versions {
		if strings.TrimSpace(v.AppName) == "" {
			return nil, fmt.Errorf("app_info.xml: app_version %d has no <app_name>", i+1)
		}
		main := 0
		for _, r := range v.FileRefs {
			if strings.ContainsAny(r.FileName, `/\`) || r.FileName == ".." {
				return nil, fmt.Errorf("app_info.xml: %q is not a plain file name", r.FileName)
			}
			if r.MainProgram != nil {
				main++
			}
		}
		if main != 1 {
			return nil, fmt.Errorf("app_info.xml: %s %d needs exactly one <main_program/> file_ref", v.AppName, v.VersionNum)
		}
	}
	return &ai, nil
}

// Find returns the application version for an app name and version number.
func (a *AppInfo) Find(appName string, versionNum int, planClass string) (AppInfoVersion, bool) {
	for _, v := range a.Versions {
		if v.AppName == appName && v.VersionNum == versionNum && v.PlanClass == planClass {
			return v, true
		}
	}
	for _, v := range a.Versions {
		if v.AppName == appName && v.VersionNum == versionNum {
			return v, true
		}
	}
	return AppInfoVersion{}, false
}
