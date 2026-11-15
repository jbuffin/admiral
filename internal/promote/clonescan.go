package promote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/neutralgit"
)

// ErrGitCarrier is a clone holding something git treats as a repository or submodule below the root.
var ErrGitCarrier = errors.New("the clone holds a git carrier")

// maxCarrierPath caps the path a refusal names, so a ship cannot flood the message.
const maxCarrierPath = 200

// Carrier is the refusal for reason at path, which is quoted and cut so it cannot forge or flood the message.
func Carrier(reason, path string) error {
	cut := ""
	if len(path) > maxCarrierPath {
		n := maxCarrierPath
		for n > 0 && !utf8.RuneStart(path[n]) {
			n--
		}
		path, cut = path[:n], " (path cut)"
	}
	return fmt.Errorf("%w: %s at %q%s", ErrGitCarrier, reason, path, cut)
}

// ScanTree walks clone's work tree without following links and refuses a .git or .gitmodules below the root, which git
// would treat as a nested repository or submodule, and a root .git that is not a real folder. Names match case-insensitively
// because a checkout can sit on a case-folding disk; only the exact root .git folder is skipped.
func ScanTree(clone string) error {
	return filepath.WalkDir(clone, func(path string, d fs.DirEntry, err error) error {
		rel, relErr := filepath.Rel(clone, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if err != nil {
			if rel == "." {
				return errors.New("the clone's work tree cannot be read")
			}
			return Carrier("a path the scan could not read", rel)
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		switch {
		case rel == ".git" && d.IsDir():
			return filepath.SkipDir
		case strings.EqualFold(name, ".git") && rel == name:
			return Carrier("a root .git that is not a folder", rel)
		case strings.EqualFold(name, ".git"):
			return Carrier("a nested git repository", rel)
		case strings.EqualFold(name, ".gitmodules") && rel != name:
			return Carrier("a nested .gitmodules", rel)
		}
		return nil
	})
}

// CheckModes refuses a listing from ls-files -s -z or ls-tree -r -z with an entry whose mode is not a plain file or a symlink.
func CheckModes(listing []byte) error {
	for _, entry := range bytes.Split(listing, []byte{0}) {
		meta, path, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			continue
		}
		if mode, _, _ := bytes.Cut(meta, []byte{' '}); string(mode) != "100644" && string(mode) != "100755" && string(mode) != "120000" {
			return Carrier("a submodule entry of mode "+string(mode), string(path))
		}
	}
	return nil
}

// checkModes refuses a gitlink in the clone's index or in the tip's tree, which the work tree walk cannot see.
func checkModes(ctx context.Context, clone string, g *neutralgit.Git, tip string) error {
	for _, args := range [][]string{{"ls-files", "-s", "-z"}, {"ls-tree", "-r", "-z", tip}} {
		out, err := g.List(ctx, args...)
		if err != nil {
			return fmt.Errorf("git %s in %s: %w", args[0], clone, err)
		}
		if err := CheckModes(out); err != nil {
			return err
		}
	}
	return nil
}
