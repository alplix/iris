//go:build windows

package worker

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Windows has no public API to suspend an entire process tree the way
// SIGSTOP does on Unix. NtSuspendProcess/NtResumeProcess are undocumented
// but have shipped in ntdll.dll unchanged since Windows XP and are the same
// primitive well-known tools (Process Explorer's "Suspend", Sysinternals'
// PsSuspend) use — there is no supported alternative short of a job object
// set up ahead of time, which exec.Command does not give us.
var (
	ntdll                = syscall.NewLazyDLL("ntdll.dll")
	procNtSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	procNtResumeProcess  = ntdll.NewProc("NtResumeProcess")
)

const processSuspendResume = 0x0800

func pauseProcess(cmd *exec.Cmd) error  { return callNtProcessControl(cmd, procNtSuspendProcess) }
func resumeProcess(cmd *exec.Cmd) error { return callNtProcessControl(cmd, procNtResumeProcess) }

func callNtProcessControl(cmd *exec.Cmd, proc *syscall.LazyProc) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	h, err := syscall.OpenProcess(processSuspendResume, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", cmd.Process.Pid, err)
	}
	defer syscall.CloseHandle(h)
	if ret, _, callErr := proc.Call(uintptr(h)); ret != 0 {
		return fmt.Errorf("%s failed: %v", proc.Name, callErr)
	}
	return nil
}
