package detect

import (
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// AppleGPUInfo is an Apple-silicon GPU as BOINC describes it
// (COPROC_APPLE::get in client/gpu_detect.cpp).
type AppleGPUInfo struct {
	Model string // "Apple M1"
	Cores int    // GPU cores
	Metal int    // Metal version, from "Metal Support: Metal 4"
}

var (
	appleModelRe = regexp.MustCompile(`(?m)^\s*Chipset Model:\s*(.+?)\s*$`)
	appleCoresRe = regexp.MustCompile(`(?m)^\s*Total Number of Cores:\s*(\d+)`)
	appleMetalRe = regexp.MustCompile(`(?m)^\s*Metal Support:\s*Metal\s+(\d+)`)
)

// parseAppleGPU reads `system_profiler SPDisplaysDataType`. Like the
// reference client it needs the model, the core count and the Metal version,
// and ignores anything not made by Apple (Intel Macs list an Intel iGPU).
func parseAppleGPU(text string) (AppleGPUInfo, bool) {
	var g AppleGPUInfo
	if m := appleModelRe.FindStringSubmatch(text); m != nil {
		g.Model = m[1]
	}
	c := appleCoresRe.FindStringSubmatch(text)
	v := appleMetalRe.FindStringSubmatch(text)
	if g.Model == "" || c == nil || v == nil || !strings.Contains(g.Model, "Apple") {
		return AppleGPUInfo{}, false
	}
	g.Cores, _ = strconv.Atoi(c[1])
	g.Metal, _ = strconv.Atoi(v[1])
	return g, true
}

// AppleGPU detects the Apple-silicon GPU (macOS only).
func AppleGPU() (AppleGPUInfo, bool) {
	if runtime.GOOS != "darwin" {
		return AppleGPUInfo{}, false
	}
	out, err := run(20*time.Second, "/usr/sbin/system_profiler", "SPDisplaysDataType")
	if err != nil {
		return AppleGPUInfo{}, false
	}
	return parseAppleGPU(string(out))
}
