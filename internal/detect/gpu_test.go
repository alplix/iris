package detect

import "testing"

func TestParseWinGPUsUsesTheRegistry64BitFigure(t *testing.T) {
	// A 16 GB card: AdapterRAM (32-bit) is stuck just under 4 GB, the driver's
	// qwMemorySize is right. The virtual adapter must be ignored.
	js := `[{"Name":"NVIDIA GeForce RTX 5070 Ti","PNP":"PCI\\VEN_10DE&DEV_2C05&SUBSYS_1","Adapter":4293918720,"Qw":17094934528},` +
		`{"Name":"Microsoft Basic Display Adapter","PNP":"ROOT\\BASICDISPLAY","Adapter":0,"Qw":0}]`
	gpus := parseWinGPUs([]byte(js))
	if len(gpus) != 1 {
		t.Fatalf("got %d GPUs, want 1: %+v", len(gpus), gpus)
	}
	g := gpus[0]
	if g.Name != "NVIDIA GeForce RTX 5070 Ti" || g.Vendor != "NVIDIA" {
		t.Errorf("gpu = %+v", g)
	}
	if g.DedicatedMB != 16303 {
		t.Errorf("VRAM = %d MiB, want 16303 (AdapterRAM would have said 4095)", g.DedicatedMB)
	}
}

func TestParseWinGPUsFallsBackToAdapterRAMAndAcceptsASingleObject(t *testing.T) {
	js := `{"Name":"Intel(R) UHD Graphics 770","PNP":"PCI\\VEN_8086&DEV_4680","Adapter":1073741824,"Qw":0}`
	gpus := parseWinGPUs([]byte(js))
	if len(gpus) != 1 || gpus[0].Vendor != "Intel" || gpus[0].DedicatedMB != 1024 {
		t.Fatalf("gpus = %+v", gpus)
	}
	if parseWinGPUs([]byte("not json")) != nil {
		t.Error("garbage must give no GPUs")
	}
}

const lspciSample = "Slot:\t01:00.0\nClass:\tVGA compatible controller\nVendor:\tNVIDIA Corporation\nDevice:\tGA102 [GeForce RTX 3090]\nSVendor:\tASUSTeK\n\n" +
	"Slot:\t00:02.0\nClass:\tVGA compatible controller\nVendor:\tIntel Corporation\nDevice:\tAlderLake-S GT1 [UHD Graphics 770]\n\n" +
	"Slot:\t00:1f.3\nClass:\tAudio device\nVendor:\tIntel Corporation\nDevice:\tRaptor Lake HD Audio\n\n" +
	"Slot:\t81:00.0\nClass:\t3D controller\nVendor:\tNVIDIA Corporation\nDevice:\tGA100 [A100 SXM4 40GB]\n"

func TestParseLspci(t *testing.T) {
	gpus := parseLspci(lspciSample)
	if len(gpus) != 3 {
		t.Fatalf("got %d GPUs, want 3 (VGA, VGA, 3D): %+v", len(gpus), gpus)
	}
	want := []struct{ name, vendor, addr string }{
		{"NVIDIA GeForce RTX 3090", "NVIDIA", "0000:01:00.0"},
		{"Intel UHD Graphics 770", "Intel", "0000:00:02.0"},
		{"NVIDIA A100 SXM4 40GB", "NVIDIA", "0000:81:00.0"},
	}
	for i, w := range want {
		if gpus[i].Name != w.name || gpus[i].Vendor != w.vendor || gpus[i].DeviceID != w.addr {
			t.Errorf("gpu %d = %+v, want %+v", i, gpus[i], w)
		}
	}
}

func TestParseNvidiaSMIAndMerge(t *testing.T) {
	names, mem := parseNvidiaSMI("NVIDIA GeForce RTX 3090, 24576\r\nNVIDIA A100 SXM4 40GB, 40960\r\n\r\n")
	if len(names) != 2 || mem[0] != 24576 || mem[1] != 40960 {
		t.Fatalf("parsed %v %v", names, mem)
	}
	gpus := mergeNvidia(parseLspci(lspciSample), names, mem)
	if gpus[0].DedicatedMB != 24576 || gpus[2].DedicatedMB != 40960 {
		t.Errorf("NVIDIA memory not applied: %+v", gpus)
	}
	if gpus[1].DedicatedMB != 0 {
		t.Errorf("the Intel GPU must be left alone: %+v", gpus[1])
	}
}

func TestMergeNvidiaTwoIdenticalCardsGetOneEntryEach(t *testing.T) {
	gpus := []GPU{{Name: "NVIDIA GeForce RTX 4090", Vendor: "NVIDIA"}, {Name: "NVIDIA GeForce RTX 4090", Vendor: "NVIDIA"}}
	out := mergeNvidia(gpus, []string{"NVIDIA GeForce RTX 4090", "NVIDIA GeForce RTX 4090"}, []int64{24564, 24564})
	if out[0].DedicatedMB != 24564 || out[1].DedicatedMB != 24564 || len(out) != 2 {
		t.Fatalf("out = %+v", out)
	}
}

func TestMergeNvidiaFindsGPUsTheOSQueryMissed(t *testing.T) {
	out := mergeNvidia(nil, []string{"NVIDIA L4"}, []int64{23034})
	if len(out) != 1 || out[0].Vendor != "NVIDIA" || out[0].DedicatedMB != 23034 {
		t.Fatalf("out = %+v", out)
	}
}

func TestParseSystemProfiler(t *testing.T) {
	intel := "Graphics/Displays:\n\n    AMD Radeon Pro 560X:\n\n      Chipset Model: AMD Radeon Pro 560X\n      Type: GPU\n      VRAM (Total): 4 GB\n"
	g := parseSystemProfiler(intel)
	if len(g) != 1 || g[0].Vendor != "AMD" || g[0].DedicatedMB != 4096 {
		t.Fatalf("intel mac = %+v", g)
	}
	silicon := "    Apple M3 Pro:\n\n      Chipset Model: Apple M3 Pro\n      Total Number of Cores: 18\n"
	g = parseSystemProfiler(silicon)
	if len(g) != 1 || g[0].Vendor != "Apple" || g[0].DedicatedMB != 0 {
		t.Fatalf("apple silicon (unified memory, no VRAM line) = %+v", g)
	}
	old := "      Chipset Model: Intel HD Graphics 4000\n      VRAM (Dynamic, Max): 1536 MB\n"
	if g := parseSystemProfiler(old); len(g) != 1 || g[0].DedicatedMB != 1536 {
		t.Fatalf("dynamic vram = %+v", g)
	}
}

func TestParseSizeMB(t *testing.T) {
	for in, want := range map[string]int64{"8 GB": 8192, " 1536 MB": 1536, "16303 MiB": 16303, "": 0, "n/a": 0} {
		if got := parseSizeMB(in); got != want {
			t.Errorf("parseSizeMB(%q) = %d, want %d", in, got, want)
		}
	}
}
