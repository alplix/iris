package detect

import (
	"os"
	"strings"
	"testing"
)

func TestParseProbeOutputAndKind(t *testing.T) {
	devs := ParseProbeOutput([]byte(`[{"name":"NVIDIA GeForce RTX 5070 Ti","vendor":"NVIDIA Corporation","globalMem":17094934528,"deviceVersion":"OpenCL 3.0 CUDA","nvCcMajor":12},{"name":"","vendor":"x"}]`))
	if len(devs) != 1 || devs[0].Kind() != "nvidia" || devs[0].NvCCMajor != 12 || devs[0].DeviceVersion != "OpenCL 3.0 CUDA" {
		t.Errorf("got %+v", devs)
	}
	if amd := (OpenCLDevice{Vendor: "Advanced Micro Devices, Inc.", Name: "gfx1100"}); amd.Kind() != "amd" {
		t.Error("AMD must be recognised by its vendor string")
	}
	if intel := (OpenCLDevice{Vendor: "Intel(R) Corporation"}); intel.Kind() != "intel" {
		t.Error("Intel must be recognised")
	}
	if ParseProbeOutput([]byte("garbage")) != nil || ParseProbeOutput(nil) != nil {
		t.Error("unreadable probe output means no devices")
	}
}

// On a machine with an OpenCL GPU this prints what was found; anywhere else it
// simply finds nothing. Either way it must not crash.
func TestProbeOpenCLDoesNotCrash(t *testing.T) {
	out := ProbeOpenCL()
	t.Logf("%s", out)
	if devs := ParseProbeOutput(out); devs != nil {
		for _, d := range devs {
			if d.Name == "" {
				t.Errorf("a listed device needs a name: %+v", d)
			}
		}
	}
}

// Real `clinfo --raw` output from an Apple M1 (macOS).
func TestParseClinfoRawFromARealMac(t *testing.T) {
	b, err := os.ReadFile("testdata/clinfo_macos_m1.txt")
	if err != nil {
		t.Fatal(err)
	}
	devs := parseClinfoRaw(string(b))
	if len(devs) != 1 {
		t.Fatalf("want the one GPU, got %d: %+v", len(devs), devs)
	}
	d := devs[0]
	if d.Name != "Apple M1" || d.Vendor != "Apple" || d.VendorID != 0x1027f00 || !d.Available || d.MaxComputeUnits != 8 ||
		d.GlobalMem != 5726633984 || d.MaxClockMHz != 1000 || d.LocalMem != 32768 || !d.EndianLittle || d.ExecutionCaps != 1 ||
		d.DeviceVersion != "OpenCL 1.2" || d.DriverVersion != "1.2 1.0" || !strings.HasPrefix(d.PlatformVersion, "OpenCL 1.2") {
		t.Errorf("parsed wrongly: %+v", d)
	}
	// INF_NAN | ROUND_TO_NEAREST | ROUND_TO_ZERO | ROUND_TO_INF | FMA | CORRECTLY_ROUNDED_DIVIDE_SQRT
	if d.SingleFPConfig != 2+4+8+16+32+128 {
		t.Errorf("fp config bits wrong: %d", d.SingleFPConfig)
	}
	if parseClinfoRaw("") != nil || parseClinfoRaw("garbage") != nil {
		t.Error("no clinfo output means no devices")
	}
}
