// Package energy estimates how much electricity the work Iris runs uses and
// the carbon that corresponds to. It is an estimate, not a meter: a CPU's draw
// is modelled from a full-load figure the person can set, and only a GPU with
// a readable sensor (NVIDIA's nvidia-smi) is actually measured.
package energy

import (
	"strconv"
	"strings"
)

// Settings are the figures the estimate rests on.
type Settings struct {
	// CPUWatts is the whole CPU's power at full load. Every running CPU task
	// is taken to keep one logical core busy.
	CPUWatts float64
	// GPUWatts is the GPU's power while it computes, used only when the GPU
	// cannot be read; a readable sensor overrides it.
	GPUWatts float64
	// GridGPerKWh is the carbon intensity of the local electricity, in grams
	// of CO2-equivalent per kilowatt-hour.
	GridGPerKWh float64
}

// Defaults are deliberately round, middle-of-the-road numbers; the interface
// tells the person to replace them with their own.
var Defaults = Settings{CPUWatts: 65, GPUWatts: 150, GridGPerKWh: 475}

// WithDefaults fills any figure that is missing or not positive.
func (s Settings) WithDefaults() Settings {
	if s.CPUWatts <= 0 {
		s.CPUWatts = Defaults.CPUWatts
	}
	if s.GPUWatts <= 0 {
		s.GPUWatts = Defaults.GPUWatts
	}
	if s.GridGPerKWh <= 0 {
		s.GridGPerKWh = Defaults.GridGPerKWh
	}
	return s
}

// Watts is the power drawn by the tasks running right now. cpuTasks and
// gpuTasks count running tasks, ncpu the logical cores; gpuMeasuredW is the
// GPU sensor's reading (0 when unavailable). measured reports whether the GPU
// figure is a real reading rather than the configured estimate.
func Watts(s Settings, cpuTasks, ncpu, gpuTasks int, gpuMeasuredW float64) (cpuW, gpuW float64, measured bool) {
	s = s.WithDefaults()
	if ncpu <= 0 {
		ncpu = 1
	}
	if cpuTasks > ncpu {
		cpuTasks = ncpu
	}
	if cpuTasks > 0 {
		cpuW = s.CPUWatts * float64(cpuTasks) / float64(ncpu)
	}
	if gpuTasks > 0 {
		if gpuMeasuredW > 0 {
			return cpuW, gpuMeasuredW, true
		}
		gpuW = s.GPUWatts
	}
	return cpuW, gpuW, false
}

// GramsCO2 converts watt-hours to grams of CO2-equivalent.
func GramsCO2(wh, gridGPerKWh float64) float64 {
	return wh / 1000 * gridGPerKWh
}

// ParseNvidiaPower sums the per-GPU lines of
// `nvidia-smi --query-gpu=power.draw --format=csv,noheader,nounits`; a line
// that is not a number (a card without a sensor prints "[N/A]") counts as 0.
func ParseNvidiaPower(text string) float64 {
	total := 0.0
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if v, err := strconv.ParseFloat(strings.TrimSpace(line), 64); err == nil && v > 0 {
			total += v
		}
	}
	return total
}
