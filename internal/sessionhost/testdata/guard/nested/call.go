package nested

// A separate module is not part of Admiral's boundary.

func call() {
	exec.Command("tmux", "kill-server")
}
