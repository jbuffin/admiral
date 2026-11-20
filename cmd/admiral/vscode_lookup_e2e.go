//go:build e2e

package main

import (
	"os"

	"github.com/jbuffin/admiral/internal/editors"
)

// vscodeInstalled lets a test build answer for the whole lookup: ADMIRAL_VSCODE_TEST is `installed` or `missing`,
// so a run gives the same result whether or not the machine has VS Code. A release is built without this file.
func vscodeInstalled() bool {
	switch os.Getenv("ADMIRAL_VSCODE_TEST") {
	case "installed":
		return true
	case "missing":
		return false
	}
	return editors.VSCodeInstalled()
}
