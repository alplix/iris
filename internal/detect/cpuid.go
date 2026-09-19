package detect

import (
	"crypto/md5"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

func getHostCPID() string {
	switch runtime.GOOS {
	case "windows":
		return hostCPIDWin()
	case "linux":
		return hostCPIDLinux()
	case "darwin":
		return hostCPIDDarwin()
	}
	return fallbackCPID()
}

func hostCPIDWin() string {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"(Get-CimInstance Win32_ComputerSystemProduct).UUID").Output()
	if err != nil {
		return fallbackCPID()
	}
	uuid := strings.TrimSpace(string(out))
	if uuid != "" && uuid != "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF" {
		return strings.ToLower(uuid)
	}
	return fallbackCPID()
}

func hostCPIDLinux() string {
	out, err := exec.Command("cat", "/etc/machine-id").Output()
	if err == nil {
		id := strings.TrimSpace(string(out))
		if id != "" {
			return id
		}
	}
	out, err = exec.Command("cat", "/var/lib/dbus/machine-id").Output()
	if err == nil {
		id := strings.TrimSpace(string(out))
		if id != "" {
			return id
		}
	}
	return fallbackCPID()
}

func hostCPIDDarwin() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return fallbackCPID()
	}
	s := string(out)
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "IOPlatformUUID") {
			parts := strings.Split(line, "\"")
			if len(parts) >= 4 {
				return strings.ToLower(parts[3])
			}
		}
	}
	return fallbackCPID()
}

func fallbackCPID() string {
	hostname, _ := exec.Command("hostname").Output()
	data := fmt.Sprintf("%s-%s-%s-%d", runtime.GOOS, runtime.GOARCH, strings.TrimSpace(string(hostname)), 12345)
	sum := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", sum)
}
