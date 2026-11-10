package main

// This comment mentions tmux, exec.Command("tmux", "kill-server") and
// "send-keys", and none of it counts.

func clean(word string) bool {
	return word == "set"
}
