package detect

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func getRAM() float64 {
	switch runtime.GOOS {
	case "windows":
		return getRAMWin()
	case "linux":
		return getRAMLinux()
	case "darwin":
		return getRAMDarwin()
	}
	return 0
}

func getRAMWin() float64 {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory").Output()
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return v
}

func getRAMLinux() float64 {
	out, err := exec.Command("cat", "/proc/meminfo").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				v, _ := strconv.ParseFloat(fields[1], 64)
				return v * 1024
			}
		}
	}
	return 0
}

func getRAMDarwin() float64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return v
}

func getDiskUsage() (float64, float64) {
	switch runtime.GOOS {
	case "windows":
		return getDiskWin()
	case "linux":
		return getDiskLinux("/")
	case "darwin":
		return getDiskLinux("/")
	}
	return 0, 0
}

func getDiskWin() (float64, float64) {
	ps := "$d=Get-PSDrive C; \"$($d.Used+$d.Free)\"; \"$($d.Free)\""
	out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).Output()
	if err != nil {
		return 0, 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) >= 2 {
		total, _ := strconv.ParseFloat(strings.TrimSpace(lines[0]), 64)
		free, _ := strconv.ParseFloat(strings.TrimSpace(lines[1]), 64)
		return total, free
	}
	return 0, 0
}

func getDiskLinux(path string) (float64, float64) {
	out, err := exec.Command("df", "-B1", path).Output()
	if err != nil {
		return 0, 0
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return 0, 0
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return 0, 0
	}
	total, _ := strconv.ParseFloat(fields[1], 64)
	free, _ := strconv.ParseFloat(fields[3], 64)
	return total, free
}
