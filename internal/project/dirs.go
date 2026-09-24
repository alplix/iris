package project

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var unsafeDirChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// DirName is the folder name a project gets under <data>/projects: its address
// without the scheme, as the reference client names it ("einstein.phys.uwm.edu",
// "asteroidsathome.net_boinc"), so a person can tell the folders apart and
// knows where to put a project's own files such as app_info.xml.
func DirName(projectURL string) string {
	s := strings.TrimSpace(projectURL)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		s = u.Host + u.Path
	}
	s = strings.ToLower(strings.Trim(s, "/"))
	s = strings.ReplaceAll(s, "/", "_")
	s = unsafeDirChars.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if s == "" {
		return "project"
	}
	return s
}

// Migrate makes sure a project has its readable folder, moving over the one an
// older version named after a hash of the address (so nothing already
// downloaded is lost). It returns the folder's path.
func Migrate(dataDir, projectURL string) (string, error) {
	base := filepath.Join(dataDir, "projects")
	want := filepath.Join(base, DirName(projectURL))
	old := filepath.Join(base, urlHash(projectURL))
	if _, err := os.Stat(want); os.IsNotExist(err) {
		if _, err := os.Stat(old); err == nil {
			if err := os.Rename(old, want); err != nil {
				return old, err
			}
			return want, nil
		}
	}
	return want, os.MkdirAll(want, 0o755)
}
