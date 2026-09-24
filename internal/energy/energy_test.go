package energy

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestWattsScalesTheCPUByBusyCores(t *testing.T) {
	s := Settings{CPUWatts: 100, GPUWatts: 200, GridGPerKWh: 500}
	cpu, gpu, measured := Watts(s, 4, 16, 0, 0)
	if !near(cpu, 25) || gpu != 0 || measured {
		t.Errorf("4 of 16 cores at 100 W should be 25 W, got %v %v %v", cpu, gpu, measured)
	}
	if cpu, _, _ := Watts(s, 64, 16, 0, 0); !near(cpu, 100) {
		t.Errorf("more tasks than cores cannot exceed the full-load figure, got %v", cpu)
	}
}

func TestGPUUsesTheSensorWhenThereIsOne(t *testing.T) {
	s := Defaults
	if _, gpu, measured := Watts(s, 0, 8, 1, 231.5); !near(gpu, 231.5) || !measured {
		t.Errorf("a reading must win, got %v %v", gpu, measured)
	}
	if _, gpu, measured := Watts(s, 0, 8, 1, 0); !near(gpu, Defaults.GPUWatts) || measured {
		t.Errorf("without a sensor the configured figure is an estimate, got %v %v", gpu, measured)
	}
	if _, gpu, _ := Watts(s, 0, 8, 0, 300); gpu != 0 {
		t.Error("no GPU task means no GPU energy, whatever the card is doing")
	}
}

func TestDefaultsFillMissingFigures(t *testing.T) {
	if got := (Settings{}).WithDefaults(); got != Defaults {
		t.Errorf("got %+v", got)
	}
	if got := (Settings{CPUWatts: 30}).WithDefaults(); got.CPUWatts != 30 || got.GridGPerKWh != Defaults.GridGPerKWh {
		t.Errorf("a set figure must be kept: %+v", got)
	}
}

func TestGramsCO2(t *testing.T) {
	if got := GramsCO2(2000, 475); !near(got, 950) {
		t.Errorf("2 kWh at 475 g/kWh is 950 g, got %v", got)
	}
}

func TestParseNvidiaPower(t *testing.T) {
	if got := ParseNvidiaPower("120.50\r\n[N/A]\r\n80\r\n"); !near(got, 200.5) {
		t.Errorf("got %v", got)
	}
	if ParseNvidiaPower("") != 0 || ParseNvidiaPower("[N/A]") != 0 {
		t.Error("nothing readable must be 0")
	}
}
