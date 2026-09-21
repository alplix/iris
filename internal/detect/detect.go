package detect

import (
	"math"
	"runtime"
	"strings"
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
	s.PFlops = estimateFLOPS(s.Ncpus, s.Model)
	s.DTotal, s.DFree = getDiskUsage()
	s.GPUs = detectGPUs()
	if s.Vendor == "" {
		s.Vendor = runtime.GOARCH
	}
	if s.Model == "" {
		s.Model = "Unknown CPU"
	}
	return s
}

func estimateFLOPS(ncpus int, model string) float64 {
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
		flopsPerCore = 10e9
	}
	return math.Round(float64(ncpus)*flopsPerCore/1e9) * 1e9
}
