//go:build !windows

package affected

import (
	"os/exec"
	"syscall"
)

// killProcessGroupOnCancel runs the command in a process group of its own
// and makes a cancelled context kill the whole group.
func killProcessGroupOnCancel(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
