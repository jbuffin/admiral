// Package home resolves Admiral's data directory.
package home

import (
	"fmt"
	"os"
	"path/filepath"
)

// Resolve returns $ADMIRAL_HOME, or ~/.admiral when it is unset, and creates
// the directory with mode 0700 if it does not exist.
func Resolve() (string, error) {
	dir := os.Getenv("ADMIRAL_HOME")
	if dir == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		dir = filepath.Join(user, ".admiral")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}
