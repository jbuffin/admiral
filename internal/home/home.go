// Package home resolves Admiral's data directory.
package home

import (
	"crypto/sha256"
	"encoding/hex"
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
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("restrict %s: %w", dir, err)
	}
	return dir, nil
}

// ID names a data directory without giving its path away. A server reports it
// so that a command can tell whether the server on a port serves its directory.
func ID(dir string) string {
	// Both sides name the directory their own way (relative, through a symlink).
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	sum := sha256.Sum256([]byte(filepath.Clean(dir)))
	return hex.EncodeToString(sum[:8])
}
