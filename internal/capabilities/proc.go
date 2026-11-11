package capabilities

import (
	"os/exec"
	"syscall"
)

// InOwnGroup starts cmd in its own process group and makes a cancelled context kill the
// whole group, so a harness's helpers don't outlive a timeout. Call it before Start.
func InOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
