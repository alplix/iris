package detect

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// detectOS returns an operating system name and version that read like the
// reference client's ("Microsoft Windows 11", "Ubuntu", "Darwin"), instead of
// the Go build's GOOS/GOARCH words, which is what projects used to list.
// Empty strings mean "could not tell"; the caller keeps its fallback.
func detectOS() (name, version string) {
	switch runtime.GOOS {
	case "windows":
		out, err := run(10*time.Second, "reg", "query", `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`)
		if err != nil {
			return "", ""
		}
		return windowsOS(parseRegQuery(string(out)), runtime.GOARCH)
	case "darwin":
		out, err := run(5*time.Second, "uname", "-r")
		if err != nil {
			return "", ""
		}
		return "Darwin", strings.TrimSpace(string(out))
	case "linux":
		b, err := os.ReadFile("/etc/os-release")
		if err != nil {
			return "", ""
		}
		return parseOSRelease(string(b))
	}
	return "", ""
}

// parseRegQuery reads `reg query` output lines of the form
// "    ProductName    REG_SZ    Windows 10 Pro".
func parseRegQuery(text string) map[string]string {
	vals := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[1], "REG_") {
			continue
		}
		vals[f[0]] = strings.Join(f[2:], " ")
	}
	return vals
}

func windowsOS(v map[string]string, goarch string) (name, version string) {
	product := v["ProductName"]
	if product == "" {
		return "", ""
	}
	build := v["CurrentBuildNumber"]
	if build == "" {
		build = v["CurrentBuild"]
	}
	// The registry still says "Windows 10" on Windows 11.
	if n, _ := strconv.Atoi(build); n >= 22000 && strings.HasPrefix(product, "Windows 10") {
		product = "Windows 11" + strings.TrimPrefix(product, "Windows 10")
	}
	arch := map[string]string{"amd64": "x64", "arm64": "ARM64", "386": "x86"}[goarch]
	ver := "10.0." + build
	if ubr, err := strconv.ParseInt(strings.TrimPrefix(v["UBR"], "0x"), 16, 64); err == nil {
		ver += fmt.Sprintf(".%d", ubr)
	}
	if arch != "" {
		ver += " " + arch
	}
	return "Microsoft " + product, ver
}

// parseOSRelease turns /etc/os-release into the distribution's name and
// version ("Ubuntu", "26.04 LTS (Resolute Raccoon)").
func parseOSRelease(text string) (name, version string) {
	kv := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		k, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			kv[k] = strings.Trim(val, `"'`)
		}
	}
	name = kv["NAME"]
	version = kv["VERSION"]
	if version == "" {
		version = kv["VERSION_ID"]
	}
	return name, version
}
