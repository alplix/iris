package app

import (
	"fmt"
	"sort"
	"time"

	"github.com/alplix/iris/internal/boinc"
	"github.com/alplix/iris/internal/energy"
)

// carKgPerKm is the CO2 an average passenger car emits per kilometre, used
// only for the "about N km by car" comparison.
const carKgPerKm = 0.2

// EnergyDay is one day of estimated electricity use and its carbon.
type EnergyDay struct {
	Day    string  `json:"day"` // YYYYMMDD
	CPUKWh float64 `json:"cpuKWh"`
	GPUKWh float64 `json:"gpuKWh"`
	KWh    float64 `json:"kwh"`
	KgCO2  float64 `json:"kgCO2"`
}

// EnergyInfo is a host's energy and carbon estimate. Supported is false for a
// client that does not measure it (a stock BOINC client).
type EnergyInfo struct {
	Supported   bool        `json:"supported"`
	CPUWatts    float64     `json:"cpuWatts"`
	GPUWatts    float64     `json:"gpuWatts"`
	GridG       float64     `json:"gridG"`
	TotalKWh    float64     `json:"totalKWh"`
	CPUKWh      float64     `json:"cpuKWh"`
	GPUKWh      float64     `json:"gpuKWh"`
	TotalKgCO2  float64     `json:"totalKgCO2"`
	TodayKWh    float64     `json:"todayKWh"`
	TodayKgCO2  float64     `json:"todayKgCO2"`
	CarKm       float64     `json:"carKm"`
	GPUModelled bool        `json:"gpuModelled"` // some GPU energy is a configured figure, not a reading
	Days        []EnergyDay `json:"days"`
}

// BuildEnergy turns a client's raw history into totals and per-day carbon at
// the client's own grid figure. now decides which day is "today".
func BuildEnergy(e *boinc.IrisEnergy, now time.Time) *EnergyInfo {
	if e == nil {
		return &EnergyInfo{}
	}
	out := &EnergyInfo{Supported: true, CPUWatts: e.CPUWatts, GPUWatts: e.GPUWatts, GridG: e.Grid}
	today := now.Unix() / 86400
	days := append([]boinc.IrisEnergyDay(nil), e.Days...)
	sort.Slice(days, func(i, j int) bool { return days[i].Day < days[j].Day })
	for _, d := range days {
		cpu, gpu := d.CPUWh/1000, d.GPUWh/1000
		kg := energy.GramsCO2(d.CPUWh+d.GPUWh, e.Grid) / 1000
		out.CPUKWh += cpu
		out.GPUKWh += gpu
		out.TotalKgCO2 += kg
		if d.GPUEstWh > 0 {
			out.GPUModelled = true
		}
		if d.Day == today {
			out.TodayKWh = cpu + gpu
			out.TodayKgCO2 = kg
		}
		out.Days = append(out.Days, EnergyDay{
			Day: time.Unix(d.Day*86400, 0).UTC().Format("20060102"), CPUKWh: cpu, GPUKWh: gpu, KWh: cpu + gpu, KgCO2: kg,
		})
	}
	out.TotalKWh = out.CPUKWh + out.GPUKWh
	out.CarKm = out.TotalKgCO2 / carKgPerKm
	return out
}

// Energy returns a host's energy estimate; a client that cannot answer gives
// Supported=false rather than an error.
func (m *Manager) Energy(hostID string) (*EnergyInfo, error) {
	cfg, ok := m.Store.Get(hostID)
	if !ok {
		return nil, fmt.Errorf("host not found")
	}
	if cfg.Demo {
		return BuildEnergy(m.mockFor(cfg).Energy(), time.Now()), nil
	}
	c, err := m.clientFor(cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	e, err := c.GetIrisEnergy()
	if err != nil {
		return &EnergyInfo{}, nil
	}
	return BuildEnergy(e, time.Now()), nil
}

// Energy is the demo host's estimate: a plausible month of a busy machine.
func (m *Mock) Energy() *boinc.IrisEnergy {
	e := &boinc.IrisEnergy{CPUWatts: 65, GPUWatts: 150, Grid: 475}
	today := time.Now().Unix() / 86400
	for i := 29; i >= 0; i-- {
		f := 0.6 + 0.4*float64((i*7)%10)/10
		e.Days = append(e.Days, boinc.IrisEnergyDay{Day: today - int64(i), CPUWh: 900 * f, GPUWh: 1500 * f})
	}
	return e
}
