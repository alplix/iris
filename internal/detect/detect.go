package detect

import (
	"math"
	"runtime"
	"strings"
	"time"
)

type Specs struct {
	OSName    string
	OSVersion string
	Vendor    string
	Model     string
	Ncpus     int
	PFlops    float64
	MNbytes   float64
	DFree     float64
	DTotal    float64
	GPUs      []GPU
	// Nvidia is filled only when an NVIDIA GPU is present and nvidia-smi answers.
	Nvidia NvidiaDetails
}

type GPU struct {
	Name        string
	Vendor      string
	DeviceID    string
	DedicatedMB int64
}

func Detect() Specs {
	s := Specs{
		OSName:    runtime.GOOS,
		OSVersion: runtime.GOARCH,
		Ncpus:     runtime.NumCPU(),
		MNbytes:   getRAM(),
	}
	s.Vendor, s.Model = getCPUInfo()
	if flops, known := estimateFLOPS(s.Ncpus, s.Model); known {
		s.PFlops = flops
	} else {
		// Unknown CPUs (single-board computers, RISC-V, POWER, ...) range from a
		// fraction of a GFLOPS to hundreds. A guess would make a project hand out
		// far more work than the machine can finish, so measure it.
		s.PFlops = Benchmark(s.Ncpus, 400*time.Millisecond)
	}
	if rel := kernelRelease(); rel != "" && runtime.GOOS == "linux" {
		s.OSVersion = rel + " " + runtime.GOARCH
	}
	s.DTotal, s.DFree = getDiskUsage()
	s.GPUs = detectGPUs()
	for _, g := range s.GPUs {
		if g.Vendor == "NVIDIA" {
			s.Nvidia = nvidiaDetails()
			break
		}
	}
	if s.Vendor == "" {
		s.Vendor = runtime.GOARCH
	}
	if s.Model == "" {
		s.Model = "Unknown CPU"
	}
	return s
}

// estimateFLOPS guesses the speed of a few well-known CPUs from their name;
// known is false for anything else.
func estimateFLOPS(ncpus int, model string) (flops float64, known bool) {
	m := strings.ToLower(model)
	var flopsPerCore float64
	switch {
	case strings.Contains(m, "m4") || strings.Contains(m, "apple"):
		flopsPerCore = 40e9
	case strings.Contains(m, "14900") || strings.Contains(m, "13900"):
		flopsPerCore = 25e9
	case strings.Contains(m, "7950x") || strings.Contains(m, "7900x") || strings.Contains(m, "9950x"):
		flopsPerCore = 22e9
	case strings.Contains(m, "14700") || strings.Contains(m, "13700") || strings.Contains(m, "7700x") || strings.Contains(m, "5800x"):
		flopsPerCore = 18e9
	case strings.Contains(m, "14600") || strings.Contains(m, "13600") || strings.Contains(m, "7600x") || strings.Contains(m, "5600x"):
		flopsPerCore = 14e9
	default:
		return 0, false
	}
	return math.Round(float64(ncpus)*flopsPerCore/1e9) * 1e9, true
}
