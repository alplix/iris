package detect

import (
	"os/exec"
	"runtime"
	"strings"
)

func detectGPUs() []GPU {
	switch runtime.GOOS {
	case "windows":
		return detectGPUsWin()
	case "linux":
		return detectGPUsLinux()
	case "darwin":
		return detectGPUsDarwin()
	}
	return nil
}

func detectGPUsWin() []GPU {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_VideoController | Select-Object Name,PNPDeviceID,AdapterRAM | ConvertTo-Json").Output()
	if err != nil {
		return nil
	}
	s := string(out)
	var gpus []GPU
	objects := strings.Split(s, "},{")
	for _, obj := range objects {
		var gpu GPU
		for _, field := range strings.Split(obj, ",") {
			field = strings.TrimSpace(field)
			if strings.Contains(field, "\"Name\"") {
				gpu.Name = extractGPUValue(field)
			}
			if strings.Contains(field, "\"PNPDeviceID\"") {
				gpu.DeviceID = extractGPUValue(field)
				if strings.Contains(gpu.DeviceID, "VEN_10DE") {
					gpu.Vendor = "NVIDIA"
				} else if strings.Contains(gpu.DeviceID, "VEN_1002") {
					gpu.Vendor = "AMD"
				} else if strings.Contains(gpu.DeviceID, "VEN_8086") {
					gpu.Vendor = "Intel"
				}
			}
			if strings.Contains(field, "\"AdapterRAM\"") {
				v, _ := parseUint(extractGPUValue(field))
				gpu.DedicatedMB = int64(v / (1024 * 1024))
			}
		}
		if gpu.Name != "" {
			gpus = append(gpus, gpu)
		}
	}
	return gpus
}

func extractGPUValue(field string) string {
	idx := strings.Index(field, ":")
	if idx < 0 {
		return ""
	}
	val := strings.TrimSpace(field[idx+1:])
	val = strings.Trim(val, "\"")
	return val
}

func detectGPUsLinux() []GPU {
	out, err := exec.Command("lspci", "-vmm").Output()
	if err != nil {
		return nil
	}
	s := string(out)
	var gpus []GPU
	var cur GPU
	inVGA := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Class:") && strings.Contains(strings.ToLower(line), "vga") {
			inVGA = true
			cur = GPU{}
			continue
		}
		if strings.HasPrefix(line, "Class:") && inVGA {
			gpus = append(gpus, cur)
			cur = GPU{}
			inVGA = false
			continue
		}
		if inVGA {
			if strings.HasPrefix(line, "Vendor:") {
				vendor := strings.TrimSpace(strings.TrimPrefix(line, "Vendor:"))
				cur.Vendor = vendor
				cur.Name = vendor
			}
			if strings.HasPrefix(line, "Device:") {
				cur.Name = cur.Vendor + " " + strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			}
		}
	}
	if inVGA && cur.Name != "" {
		gpus = append(gpus, cur)
	}
	return gpus
}

func detectGPUsDarwin() []GPU {
	out, err := exec.Command("system_profiler", "SPDisplaysDataType").Output()
	if err != nil {
		return nil
	}
	s := string(out)
	var gpus []GPU
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Chipset Model:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "Chipset Model:"))
			vendor := "Unknown"
			lower := strings.ToLower(name)
			if strings.Contains(lower, "nvidia") || strings.Contains(lower, "geforce") || strings.Contains(lower, "rtx") || strings.Contains(lower, "gtx") {
				vendor = "NVIDIA"
			} else if strings.Contains(lower, "amd") || strings.Contains(lower, "radeon") {
				vendor = "AMD"
			} else if strings.Contains(lower, "apple") || strings.Contains(lower, "m1") || strings.Contains(lower, "m2") || strings.Contains(lower, "m3") || strings.Contains(lower, "m4") {
				vendor = "Apple"
			} else if strings.Contains(lower, "intel") {
				vendor = "Intel"
			}
			gpus = append(gpus, GPU{Name: name, Vendor: vendor})
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
