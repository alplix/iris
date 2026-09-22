//go:build !windows

package worker

import (
	"os/exec"
	"syscall"
)

// pauseProcess stops a running task's process at the OS level, without any
// cooperation from the app itself — the same SIGSTOP a shell's own "kill
// -STOP" or Ctrl-Z sends. The process' memory and open files are left
// exactly as they are; resumeProcess continues it from that same point.
func pauseProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGSTOP)
}

// resumeProcess continues a process pauseProcess previously stopped.
func resumeProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGCONT)
}
