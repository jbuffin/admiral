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

// ScrubbedEnv are the variables that would tie a probe or judge call to a real ship or
// pane: the guard would record an outcome against the ship, and the bundle's hook would
// mark the pane.
var ScrubbedEnv = []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX", "ADMIRAL_LOW_STAKES",
	"ADMIRAL_AUTO_MERGE", "ADMIRAL_PORT", "TMUX", "TMUX_PANE",
	// Claude Code refuses to start inside another Claude Code session when it sees these.
	"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION"}
