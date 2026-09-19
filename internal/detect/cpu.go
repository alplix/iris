package detect

import (
	"os/exec"
	"runtime"
	"strings"
)

func getCPUInfo() (string, string) {
	switch runtime.GOOS {
	case "windows":
		return getCPUInfoWin()
	case "linux":
		return getCPUInfoLinux()
	case "darwin":
		return getCPUInfoDarwin()
	}
	return "", ""
}

func getCPUInfoWin() (string, string) {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Processor | Select-Object Name,Manufacturer | ConvertTo-Json").Output()
	if err != nil {
		return "", ""
	}
	s := string(out)
	vendor := ""
	model := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "\"Manufacturer\"") {
			vendor = extractJSON(line)
		}
		if strings.Contains(line, "\"Name\"") {
			model = extractJSON(line)
		}
	}
	return strings.TrimSpace(vendor), strings.TrimSpace(model)
}

func extractJSON(line string) string {
	idx := strings.LastIndex(line, ":")
	if idx < 0 {
		return ""
	}
	val := strings.TrimSpace(line[idx+1:])
	val = strings.Trim(val, "\",")
	return val
}

func getCPUInfoLinux() (string, string) {
	out, err := exec.Command("cat", "/proc/cpuinfo").Output()
	if err != nil {
		return "", ""
	}
	s := string(out)
	vendor := ""
	model := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "vendor_id:") {
			vendor = strings.TrimSpace(strings.TrimPrefix(line, "vendor_id:"))
		}
		if strings.HasPrefix(line, "model name:") {
			model = strings.TrimSpace(strings.TrimPrefix(line, "model name:"))
		}
	}
	return vendor, model
}

func getCPUInfoDarwin() (string, string) {
	out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	if err != nil {
		return "", ""
	}
	return "Apple", strings.TrimSpace(string(out))
}
