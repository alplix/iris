package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var (
	vboxVersionRe   = regexp.MustCompile(`^(\d+\.\d+\.\d+)`)
	dockerVersionRe = regexp.MustCompile(`(?i)docker version\s+(\d+\.\d+\.\d+)`)
)

// parseVBoxVersion turns `VBoxManage --version` ("7.0.14r161095") into the
// dotted version projects compare ("7.0.14").
func parseVBoxVersion(out string) string {
	if m := vboxVersionRe.FindStringSubmatch(strings.TrimSpace(out)); m != nil {
		return m[1]
	}
	return ""
}

// parseDockerVersion turns `docker --version` ("Docker version 24.0.7, build
// afdd53b") into "24.0.7".
func parseDockerVersion(out string) string {
	if m := dockerVersionRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// VirtualBoxVersion is the installed VirtualBox's version, or "" if there is none.
func VirtualBoxVersion() string {
	cands := []string{"VBoxManage"}
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("VBOX_MSI_INSTALL_PATH"), os.Getenv("ProgramFiles") + `\Oracle\VirtualBox\`} {
			if root != "" {
				cands = append(cands, filepath.Join(root, "VBoxManage.exe"))
			}
		}
	}
	for _, c := range cands {
		if out, err := run(8*time.Second, c, "--version"); err == nil {
			if v := parseVBoxVersion(string(out)); v != "" {
				return v
			}
		}
	}
	return ""
}

// DockerVersion is the installed Docker's version, or "" if there is none.
func DockerVersion() string {
	out, err := run(8*time.Second, "docker", "--version")
	if err != nil {
		return ""
	}
	return parseDockerVersion(string(out))
}
