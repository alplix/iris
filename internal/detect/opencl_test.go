package detect

import "testing"

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
