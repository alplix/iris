package detect

import (
	"os"
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
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return parseMemTotal(string(data))
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
	case "linux", "darwin", "freebsd", "openbsd", "netbsd":
		return getDiskUnix("/")
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
