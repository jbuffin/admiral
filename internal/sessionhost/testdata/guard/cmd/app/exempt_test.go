package main

// Test files may run tmux as an outside observer.

func observe() {
	exec.Command("tmux", "kill-server")
}
