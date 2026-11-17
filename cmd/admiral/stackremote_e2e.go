//go:build e2e

package main

import (
	"path/filepath"

	"github.com/jbuffin/admiral/internal/moor"
)

// stackRemote is the test build's way to reach a repository with no network: a bare clone under the home's e2e-remotes. A release is built without this file.
func stackRemote(home string, c moor.StackCheck) string {
	return "file://" + filepath.Join(home, "e2e-remotes", c.Host, c.Repo+".git")
}
