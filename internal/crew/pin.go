package crew

import (
	"errors"
	"io"
	"os"
	"strings"
)

// ValidName reports whether s is a member name: a lowercase slug of at most 64 characters.
func ValidName(s string) bool { return slugPattern.MatchString(s) }

// ReadFile returns the bytes of a resolved member's file. It opens the file once, checks that the open
// file is inside the layer, reads at most the size limit, and refuses bytes that no longer parse to the
// member that was resolved.
func ReadFile(layerDir string, m Member) ([]byte, error) {
	file := m.File
	if file == "" || file != strings.TrimSuffix(file, "/") || strings.ContainsRune(file, os.PathSeparator) {
		return nil, errors.New("not a member file name")
	}
	real := realPath(layerDir + string(os.PathSeparator) + file)
	if !within(real, realPath(layerDir)) {
		return nil, errors.New("file resolves outside its layer directory")
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// The path had no link when it was resolved; a different file now means it was swapped.
	if now, err := os.Lstat(real); err != nil || !os.SameFile(opened, now) || !opened.Mode().IsRegular() {
		return nil, errors.New("file changed while it was read")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, errors.New("file is over 65536 bytes")
	}
	fm, _, perr := parseMember(raw)
	if perr == nil {
		perr = checkFields(fm, strings.TrimSuffix(file, ".md"))
	}
	if perr != nil {
		return nil, errors.New("file changed after it was resolved: " + perr.message)
	}
	if fm.get("name") != m.Name || fm.get("role") != m.Role || !sameHarness(stringOrNil(fm.get("harness")), m.Harness) {
		return nil, errors.New("file changed after it was resolved")
	}
	return raw, nil
}

func sameHarness(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
