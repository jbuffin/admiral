//go:build windows

// PROTOTYPE for #876, throwaway: never merge. A ConPTY session host and a
// stub of the bridge, to see whether Claude Code and Codex work through
// ConPTY to xterm.js. See README.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "host":
		err = runHost()
	case "bridge":
		err = runBridge(false)
	case "run":
		err = runBridge(true)
	default:
		err = fmt.Errorf("usage: proto [run|host|bridge]")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "proto:", err)
		os.Exit(1)
	}
}
