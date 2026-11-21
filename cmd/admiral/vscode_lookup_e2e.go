//go:build e2e

package main

import "os"

// A test build never looks at the machine it runs on: VS Code is installed only when ADMIRAL_VSCODE_TEST is
// `installed`, so a run on a Mac with VS Code and on a clean runner give the same answer. A release is built without this file.
func vscodeInstalled() bool { return os.Getenv("ADMIRAL_VSCODE_TEST") == "installed" }
