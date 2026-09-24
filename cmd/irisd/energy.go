package main

import (
	"context"
	"encoding/xml"
	"os/exec"
	"runtime"
	"sort"
	"time"

	"github.com/alplix/iris/internal/energy"
	"github.com/alplix/iris/internal/prefs"
	"github.com/alplix/iris/internal/state"
)

// Preference keys (Global Preferences) behind the energy estimate.
const (
	prefCPUWatts = "cpu_watts"
	prefGPUWatts = "gpu_watts"
	prefGridGPer = "grid_gco2_kwh"
)

// energySettings reads the person's figures, falling back to the defaults.
func energySettings(p *prefs.Store) energy.Settings {
	var s energy.Settings
	if p != nil {
		s.CPUWatts, _ = p.Float(prefCPUWatts)
		s.GPUWatts, _ = p.Float(prefGPUWatts)
		s.GridGPerKWh, _ = p.Float(prefGridGPer)
	}
	return s.WithDefaults()
}

// nvidiaPowerDraw reads the GPU's current power in watts (0 = no reading).
func nvidiaPowerDraw() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=power.draw", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0
	}
	return energy.ParseNvidiaPower(string(out))
}

// runEnergyMeter samples what Iris's running tasks draw every 15 seconds and
// adds it to the day's total until stop is closed.
func runEnergyMeter(st *state.State, p *prefs.Store, stop <-chan struct{}) {
	const every = 15 * time.Second
	tick := time.NewTicker(every)
	defer tick.Stop()
	last := time.Now()
	saved := time.Now()
	for {
		select {
		case <-stop:
			return
		case now := <-tick.C:
			dt := now.Sub(last)
			last = now
			if dt > time.Minute {
				dt = every // the machine slept; do not bill the gap
			}
			cpuTasks, gpuTasks := 0, 0
			st.RLock()
			for i := range st.Results {
				r := &st.Results[i]
				if r.State != 2 || r.ActiveTask == 0 || r.SuspendedViaGUI != 0 {
					continue
				}
				if r.IsGPU() {
					gpuTasks++
				} else {
					cpuTasks++
				}
			}
			st.RUnlock()
			if cpuTasks == 0 && gpuTasks == 0 {
				continue
			}
			gpuRead := 0.0
			if gpuTasks > 0 {
				gpuRead = nvidiaPowerDraw()
			}
			cpuW, gpuW, measured := energy.Watts(energySettings(p), cpuTasks, runtime.NumCPU(), gpuTasks, gpuRead)
			h := dt.Hours()
			st.AddEnergy(now, cpuW*h, gpuW*h, measured)
			if now.Sub(saved) > 5*time.Minute {
				saved = now
				st.Save()
			}
		}
	}
}

// GetIrisEnergy reports the energy history and the figures it rests on.
func (h *clientHandler) GetIrisEnergy() ([]byte, error) {
	type dayXML struct {
		Day      int64   `xml:"d"`
		CPUWh    float64 `xml:"cpu_wh"`
		GPUWh    float64 `xml:"gpu_wh"`
		GPUEstWh float64 `xml:"gpu_est_wh"`
	}
	type outXML struct {
		XMLName xml.Name `xml:"iris_energy"`
		CPUW    float64  `xml:"cpu_watts"`
		GPUW    float64  `xml:"gpu_watts"`
		Grid    float64  `xml:"grid_g_per_kwh"`
		Days    []dayXML `xml:"day"`
	}
	s := energySettings(h.prefs)
	out := outXML{CPUW: s.CPUWatts, GPUW: s.GPUWatts, Grid: s.GridGPerKWh}
	for _, d := range h.state.Snapshot().Energy {
		out.Days = append(out.Days, dayXML{Day: d.Day, CPUWh: d.CPUWh, GPUWh: d.GPUWh, GPUEstWh: d.GPUEstWh})
	}
	sort.Slice(out.Days, func(a, b int) bool { return out.Days[a].Day < out.Days[b].Day })
	return xml.MarshalIndent(out, "", "  ")
}
