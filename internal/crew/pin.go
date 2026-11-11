package crew

import (
	"errors"
	"os"
	"strings"
)

// ValidName reports whether s is a member name: a lowercase slug of at most 64 characters.
func ValidName(s string) bool { return slugPattern.MatchString(s) }

// ReadFile returns the bytes of a resolved member's file, under the resolver's size and inside-the-layer checks.
func ReadFile(layerDir, file string) ([]byte, error) {
	if file == "" || file != strings.TrimSuffix(file, "/") || strings.ContainsRune(file, os.PathSeparator) {
		return nil, errors.New("not a member file name")
	}
	path := layerDir + string(os.PathSeparator) + file
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxBytes {
		return nil, errors.New("file is over 65536 bytes")
	}
	if !within(realPath(path), realPath(layerDir)) {
		return nil, errors.New("file resolves outside its layer directory")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, errors.New("file is over 65536 bytes")
	}
	return raw, nil
}
