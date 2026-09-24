package detect

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NvidiaDetails is what a project's scheduler checks before it offers CUDA
// work (a plan class such as BRP7-cuda55 demands a minimum compute
// capability), and what the manager lists. All zero when nvidia-smi is
// missing, in which case nothing is claimed.
type NvidiaDetails struct {
	CCMajor, CCMinor int
	Driver           string // e.g. "581.42"
	CudaVersion      int    // BOINC's encoding: 12080 for CUDA 12.8
}

var cudaVersionRe = regexp.MustCompile(`CUDA Version:\s*(\d+)\.(\d+)`)

// parseNvidiaDetails reads `nvidia-smi --query-gpu=compute_cap,driver_version
// --format=csv,noheader` (first device) and the plain nvidia-smi banner.
func parseNvidiaDetails(csv, banner string) NvidiaDetails {
	var d NvidiaDetails
	line := strings.TrimSpace(strings.SplitN(strings.ReplaceAll(csv, "\r\n", "\n"), "\n", 2)[0])
	if cc, drv, ok := strings.Cut(line, ","); ok {
		d.Driver = strings.TrimSpace(drv)
		if maj, min, ok := strings.Cut(strings.TrimSpace(cc), "."); ok {
			d.CCMajor, _ = strconv.Atoi(maj)
			d.CCMinor, _ = strconv.Atoi(min)
		}
	}
	if m := cudaVersionRe.FindStringSubmatch(banner); m != nil {
		maj, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		d.CudaVersion = maj*1000 + min*10
	}
	return d
}

func nvidiaDetails() NvidiaDetails {
	csv, err := run(10*time.Second, "nvidia-smi", "--query-gpu=compute_cap,driver_version", "--format=csv,noheader")
	if err != nil {
		return NvidiaDetails{}
	}
	banner, _ := run(10*time.Second, "nvidia-smi")
	return parseNvidiaDetails(string(csv), string(banner))
}
