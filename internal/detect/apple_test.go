package detect

import (
	"os"
	"runtime"
	"testing"
)

// Real `system_profiler SPDisplaysDataType` output from an Apple M1.
func TestParseAppleGPUFromARealMac(t *testing.T) {
	b, err := os.ReadFile("testdata/system_profiler_displays_m1.txt")
	if err != nil {
		t.Fatal(err)
	}
	g, ok := parseAppleGPU(string(b))
	if !ok || g.Model != "Apple M1" || g.Cores != 7 || g.Metal != 4 {
		t.Errorf("got %+v %v", g, ok)
	}
	if _, ok := parseAppleGPU("Chipset Model: Intel Iris Plus\nTotal Number of Cores: 4\nMetal Support: Metal 3\n"); ok {
		t.Error("an Intel Mac's iGPU is not an Apple GPU")
	}
	if _, ok := parseAppleGPU("Chipset Model: Apple M1\n"); ok {
		t.Error("model, cores and Metal version are all required")
	}
	if runtime.GOOS != "darwin" {
		if _, ok := AppleGPU(); ok {
			t.Error("only macOS has Apple GPUs")
		}
	}
}

func TestClinfoAppleDeviceIsClassifiedAsApple(t *testing.T) {
	if k := (OpenCLDevice{Vendor: "Apple", Name: "Apple M1"}).Kind(); k != "apple" {
		t.Errorf("kind = %q", k)
	}
}

// On an Apple-silicon Mac the real system_profiler must yield a GPU.
func TestAppleGPURealDetectionOnAppleSilicon(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("needs an Apple-silicon Mac")
	}
	g, ok := AppleGPU()
	t.Logf("%+v %v", g, ok)
	if !ok || g.Cores <= 0 || g.Metal <= 0 {
		t.Errorf("no Apple GPU found: %+v %v", g, ok)
	}
	devs := clinfoDevices()
	t.Logf("clinfo: %d device(s)", len(devs))
}
