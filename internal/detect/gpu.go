package detect

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func detectGPUs() []GPU {
	var gpus []GPU
	switch runtime.GOOS {
	case "windows":
		gpus = detectGPUsWin()
	case "linux":
		gpus = detectGPUsLinux()
	case "darwin":
		gpus = detectGPUsDarwin()
	}
	return applyNvidiaSMI(gpus)
}

// run executes a helper tool with a deadline; GPU tools can hang on a
// half-initialised driver and must never block the client's start-up.
func run(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

const mib = 1024 * 1024

// ---------------------------------------------------------------- Windows

// psGPUs lists the display adapters with both memory figures. Win32_VideoController
// .AdapterRAM is a 32-bit field and tops out at 4 GB, so the driver's 64-bit
// HardwareInformation.qwMemorySize from the registry is read as well.
const psGPUs = `$cls='HKLM:\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}'
$reg=@{}
Get-ChildItem $cls -ErrorAction SilentlyContinue | ForEach-Object {
  $p=Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
  if($p.DriverDesc){ $q=$p.'HardwareInformation.qwMemorySize'; if($q){ $reg[$p.DriverDesc]=[uint64]$q } }
}
$list=@(Get-CimInstance Win32_VideoController | ForEach-Object {
  [pscustomobject]@{Name=$_.Name; PNP=$_.PNPDeviceID; Adapter=[uint64]$_.AdapterRAM; Qw=[uint64]$reg[$_.Name]}
})
ConvertTo-Json -InputObject $list -Compress`

type winGPU struct {
	Name    string
	PNP     string
	Adapter uint64
	Qw      uint64
}

// parseWinGPUs turns the JSON of psGPUs into GPUs. Virtual and remote display
// adapters are skipped: they are not compute devices.
func parseWinGPUs(data []byte) []GPU {
	var raw []winGPU
	if err := json.Unmarshal(data, &raw); err != nil {
		var one winGPU
		if json.Unmarshal(data, &one) != nil {
			return nil
		}
		raw = []winGPU{one}
	}
	var gpus []GPU
	for _, r := range raw {
		if r.Name == "" || isVirtualAdapter(r.Name) {
			continue
		}
		gpu := GPU{Name: r.Name, DeviceID: r.PNP, Vendor: vendorFromPNP(r.PNP)}
		mem := r.Qw
		if mem == 0 {
			mem = r.Adapter // capped at 4 GB, but better than nothing
		}
		gpu.DedicatedMB = int64(mem / mib)
		gpus = append(gpus, gpu)
	}
	return gpus
}

func vendorFromPNP(pnp string) string {
	switch {
	case strings.Contains(pnp, "VEN_10DE"):
		return "NVIDIA"
	case strings.Contains(pnp, "VEN_1002"):
		return "AMD"
	case strings.Contains(pnp, "VEN_8086"):
		return "Intel"
	}
	return ""
}

func isVirtualAdapter(name string) bool {
	n := strings.ToLower(name)
	for _, v := range []string{"microsoft basic", "remote display", "virtual display", "parsec", "meta virtual", "hyper-v video"} {
		if strings.Contains(n, v) {
			return true
		}
	}
	return false
}

func detectGPUsWin() []GPU {
	out, err := run(20*time.Second, "powershell", "-NoProfile", "-Command", psGPUs)
	if err != nil {
		return nil
	}
	return parseWinGPUs(out)
}

// ------------------------------------------------------------------ Linux

// parseLspci reads `lspci -vmm` (blank-line separated blocks). Display,
// 3D and VGA controllers count as GPUs.
func parseLspci(text string) []GPU {
	var gpus []GPU
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		kv := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok {
				if _, seen := kv[strings.TrimSpace(k)]; !seen {
					kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
		}
		class := strings.ToLower(kv["Class"])
		if !(strings.Contains(class, "vga") || strings.Contains(class, "3d controller") || strings.Contains(class, "display controller")) {
			continue
		}
		vendor := shortVendor(kv["Vendor"])
		gpus = append(gpus, GPU{
			Name:     strings.TrimSpace(vendor + " " + deviceName(kv["Device"])),
			Vendor:   vendor,
			DeviceID: pciAddress(kv["Slot"]),
		})
	}
	return gpus
}

func shortVendor(v string) string {
	l := strings.ToLower(v)
	switch {
	case strings.Contains(l, "nvidia"):
		return "NVIDIA"
	case strings.Contains(l, "intel"): // before AMD: "Intel Corporation" contains "ati"
		return "Intel"
	case strings.Contains(l, "advanced micro") || strings.Contains(l, "amd") || strings.Contains(l, "ati technologies"):
		return "AMD"
	}
	return v
}

// deviceName prefers the marketing name lspci puts in brackets:
// "GA102 [GeForce RTX 3090]" -> "GeForce RTX 3090".
func deviceName(d string) string {
	if i := strings.LastIndex(d, "["); i >= 0 {
		if j := strings.LastIndex(d, "]"); j > i {
			return d[i+1 : j]
		}
	}
	return d
}

// pciAddress normalises "03:00.0" to the sysfs form "0000:03:00.0".
func pciAddress(slot string) string {
	if slot == "" {
		return ""
	}
	if strings.Count(slot, ":") == 1 {
		return "0000:" + strings.ToLower(slot)
	}
	return strings.ToLower(slot)
}

func detectGPUsLinux() []GPU {
	out, err := run(10*time.Second, "lspci", "-vmm")
	if err != nil {
		return sysfsGPUs()
	}
	gpus := parseLspci(string(out))
	for i := range gpus {
		gpus[i].DedicatedMB = sysfsVRAM(gpus[i].DeviceID)
	}
	return gpus
}

// sysfsVRAM reads the dedicated memory amdgpu exposes; other drivers do not.
func sysfsVRAM(pciAddr string) int64 {
	if pciAddr == "" {
		return 0
	}
	b, err := os.ReadFile(filepath.Join("/sys/bus/pci/devices", pciAddr, "mem_info_vram_total"))
	if err != nil {
		return 0
	}
	v, _ := parseUint(string(b))
	return int64(v / mib)
}

// sysfsGPUs is the fallback when lspci is not installed: read the PCI tree.
func sysfsGPUs() []GPU {
	dirs, _ := filepath.Glob("/sys/bus/pci/devices/*")
	var gpus []GPU
	for _, d := range dirs {
		class, err := os.ReadFile(filepath.Join(d, "class"))
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(class)), "0x03") { // display controllers
			continue
		}
		vendorID, _ := os.ReadFile(filepath.Join(d, "vendor"))
		vendor := map[string]string{"0x10de": "NVIDIA", "0x1002": "AMD", "0x8086": "Intel"}[strings.TrimSpace(string(vendorID))]
		if vendor == "" {
			vendor = "GPU"
		}
		addr := filepath.Base(d)
		gpus = append(gpus, GPU{Name: vendor + " GPU " + addr, Vendor: vendor, DeviceID: addr, DedicatedMB: sysfsVRAM(addr)})
	}
	return gpus
}

// ------------------------------------------------------------------ macOS

// parseSystemProfiler reads `system_profiler SPDisplaysDataType`. Apple
// Silicon shares system memory and reports no VRAM line.
func parseSystemProfiler(text string) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Chipset Model:"):
			name := strings.TrimSpace(strings.TrimPrefix(line, "Chipset Model:"))
			vendor := "Unknown"
			lower := strings.ToLower(name)
			switch {
			case strings.Contains(lower, "nvidia") || strings.Contains(lower, "geforce") || strings.Contains(lower, "rtx") || strings.Contains(lower, "gtx"):
				vendor = "NVIDIA"
			case strings.Contains(lower, "amd") || strings.Contains(lower, "radeon"):
				vendor = "AMD"
			case strings.Contains(lower, "apple") || strings.Contains(lower, "m1") || strings.Contains(lower, "m2") || strings.Contains(lower, "m3") || strings.Contains(lower, "m4"):
				vendor = "Apple"
			case strings.Contains(lower, "intel"):
				vendor = "Intel"
			}
			gpus = append(gpus, GPU{Name: name, Vendor: vendor})
		case strings.HasPrefix(line, "VRAM") && len(gpus) > 0:
			// "VRAM (Total): 8 GB" or "VRAM (Dynamic, Max): 1536 MB"
			if _, v, ok := strings.Cut(line, ":"); ok {
				gpus[len(gpus)-1].DedicatedMB = parseSizeMB(v)
			}
		}
	}
	return gpus
}

// parseSizeMB reads "8 GB", "1536 MB" or "16303 MiB".
func parseSizeMB(s string) int64 {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) == 0 {
		return 0
	}
	n, _ := parseUint(f[0])
	if len(f) > 1 && strings.HasPrefix(strings.ToUpper(f[1]), "G") {
		n *= 1024
	}
	return int64(n)
}

func detectGPUsDarwin() []GPU {
	out, err := run(20*time.Second, "system_profiler", "SPDisplaysDataType")
	if err != nil {
		return nil
	}
	return parseSystemProfiler(string(out))
}

// ------------------------------------------------------------ NVIDIA (all OSes)

// nvidiaSMI returns name and total memory (MiB) of every NVIDIA GPU, in the
// driver's order. It is the authoritative figure for NVIDIA cards.
func nvidiaSMI() (names []string, memMiB []int64) {
	out, err := run(10*time.Second, "nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	if err != nil {
		return nil, nil
	}
	return parseNvidiaSMI(string(out))
}

func parseNvidiaSMI(text string) (names []string, memMiB []int64) {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		name, mem, ok := strings.Cut(strings.TrimSpace(line), ",")
		if !ok {
			continue
		}
		v, _ := parseUint(mem)
		names = append(names, strings.TrimSpace(name))
		memMiB = append(memMiB, int64(v))
	}
	return names, memMiB
}

// applyNvidiaSMI overrides the memory of NVIDIA GPUs with the driver's figure,
// matching by name and falling back to order.
func applyNvidiaSMI(gpus []GPU) []GPU {
	names, mem := nvidiaSMI()
	if len(names) == 0 {
		return gpus
	}
	return mergeNvidia(gpus, names, mem)
}

func mergeNvidia(gpus []GPU, names []string, mem []int64) []GPU {
	used := make([]bool, len(names))
	nv := 0
	for i := range gpus {
		if gpus[i].Vendor != "NVIDIA" {
			continue
		}
		nv++
		match := -1
		for j, n := range names {
			if !used[j] && (strings.Contains(strings.ToLower(gpus[i].Name), strings.ToLower(n)) || strings.Contains(strings.ToLower(n), strings.ToLower(gpus[i].Name))) {
				match = j
				break
			}
		}
		if match < 0 { // fall back to the n-th NVIDIA device
			for j := range names {
				if !used[j] {
					match = j
					break
				}
			}
		}
		if match >= 0 {
			used[match] = true
			gpus[i].DedicatedMB = mem[match]
			if gpus[i].Name == "" || gpus[i].Vendor == "" {
				gpus[i].Name = names[match]
			}
		}
	}
	// nvidia-smi sees GPUs the OS query missed (no lspci, headless server)
	if nv == 0 {
		for j, n := range names {
			gpus = append(gpus, GPU{Name: n, Vendor: "NVIDIA", DedicatedMB: mem[j]})
		}
	}
	return gpus
}

func parseUint(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	var n uint64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + uint64(c-'0')
		}
	}
	return n, nil
}
