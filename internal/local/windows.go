//go:build windows

package local

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func statusByPID(pid int) DaemonStatus {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return DaemonStopped
	}
	defer syscall.CloseHandle(h)
	return DaemonRunning
}

func StartDetached(exe string) (*exec.Cmd, error) {
	cmd := exec.Command(exe, "--daemon")
	// Tells the client it already is the background copy.
	cmd.Env = append(os.Environ(), "IRIS_DETACHED=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start failed: %w", err)
	}
	return cmd, nil
}

func StartDaemon(d *Daemon, version string) error {
	if !d.Info.Found {
		return fmt.Errorf("Iris client not found")
	}
	if d.Status() == DaemonRunning {
		return nil
	}
	if err := d.WriteConfig(version); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	cmd, err := StartDetached(d.Info.Exe)
	if err != nil {
		return err
	}
	if cmd.Process != nil {
		writePIDFile(d.PIDFile, cmd.Process.Pid)
	}
	time.Sleep(800 * time.Millisecond)
	return nil
}

func StopDaemon(d *Daemon) error {
	pid := d.PID()
	if pid <= 0 {
		return nil
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		removePIDFile(d.PIDFile)
		return nil
	}
	defer syscall.CloseHandle(h)
	err = syscall.TerminateProcess(h, 0)
	removePIDFile(d.PIDFile)
	if err != nil {
		return fmt.Errorf("terminate failed: %w", err)
	}
	return nil
}

func writePIDFile(path string, pid int) {
	if path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644)
}

func removePIDFile(path string) {
	if path != "" {
		os.Remove(path)
	}
}
