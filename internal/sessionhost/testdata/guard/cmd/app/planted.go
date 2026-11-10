package main

// Planted tmux violations for TestTmuxGuardCatchesPlantedCall. One per line.
// This tree is parsed, never compiled (Go skips testdata).

func planted() {
	exec.Command("tmux", "kill-server")
	exec.Command(cfg.TmuxPath, "-S", sock)
	syscall.Exec(tmuxPath, args, env)
	_ = "capture-pane -p -t "
	_ = []string{"set", "-p", "@x", "1"}
	_ = "@admiral-state"
}
