package app

import (
	"math"
	"testing"
	"time"

	"github.com/alplix/iris/internal/boinc"
)

func TestBuildEnergyTotalsAndCarbon(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	today := now.Unix() / 86400
	e := &boinc.IrisEnergy{CPUWatts: 65, GPUWatts: 150, Grid: 500, Days: []boinc.IrisEnergyDay{
		{Day: today, CPUWh: 1000, GPUWh: 1000, GPUEstWh: 1000},
		{Day: today - 1, CPUWh: 2000, GPUWh: 0},
	}}
	got := BuildEnergy(e, now)
	if !got.Supported || math.Abs(got.TotalKWh-4) > 1e-9 || math.Abs(got.TotalKgCO2-2) > 1e-9 {
		t.Fatalf("4 kWh at 500 g/kWh must be 2 kg, got %+v", got)
	}
	if math.Abs(got.TodayKWh-2) > 1e-9 || math.Abs(got.TodayKgCO2-1) > 1e-9 {
		t.Errorf("today wrong: %+v", got)
	}
	if !got.GPUModelled {
		t.Error("modelled GPU energy must be flagged")
	}
	if math.Abs(got.CarKm-10) > 1e-9 {
		t.Errorf("2 kg at 0.2 kg/km is 10 km, got %v", got.CarKm)
	}
	if len(got.Days) != 2 || got.Days[0].Day >= got.Days[1].Day {
		t.Errorf("days must be oldest first: %+v", got.Days)
	}
	if BuildEnergy(nil, now).Supported {
		t.Error("no data from the client means not supported")
	}
}
