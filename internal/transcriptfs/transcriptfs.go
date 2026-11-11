// Package transcriptfs is how Admiral gets at a harness's transcript file (ADR
// 0019): the path checks and the open, shared by the chat and by a ship's
// conversation. A transcript path comes from a pane option that anything in the
// pane can write, so it is read only if it is a regular file under the harness's
// own <config dir>/projects/ with no symlink on the way.
package transcriptfs

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// ErrRefused is a path Admiral will not read. A path that is only not there is
// a plain not-exist error instead.
var ErrRefused = errors.New("not a regular transcript file under the Claude config directory's projects/")

// Resolve is the transcript at path p, if it may be read: absolute, ending in
// .jsonl, under <configDir>/projects/, and neither the file nor a directory
// between projects/ and it is a symlink. The config directory itself (a
// dotfiles ~/.claude, say) and projects/ may be links: the path is matched
// against the real projects/, found by walking up from the file. It returns the
// real projects/ and the path below it.
func Resolve(configDir, p string) (root, rel string, err error) {
	if !sessionhost.ValidTranscriptPath(p) {
		return "", "", ErrRefused
	}
	p = filepath.Clean(p)
	root, err = filepath.EvalSymlinks(filepath.Join(configDir, "projects"))
	if err != nil {
		return "", "", err
	}
	parts := []string{filepath.Base(p)}
	for cur := filepath.Dir(p); ; cur = filepath.Dir(cur) {
		if real, err := filepath.EvalSymlinks(cur); err == nil && real == root {
			break
		}
		if filepath.Dir(cur) == cur {
			return "", "", ErrRefused
		}
		parts = append([]string{filepath.Base(cur)}, parts...)
	}
	at := root
	for i, part := range parts {
		at = filepath.Join(at, part)
		fi, err := os.Lstat(at)
		switch {
		case err != nil:
			return "", "", err
		case fi.Mode()&os.ModeSymlink != 0:
			return "", "", ErrRefused
		case i < len(parts)-1 && !fi.IsDir():
			return "", "", ErrRefused
		case i == len(parts)-1 && !fi.Mode().IsRegular():
			return "", "", ErrRefused
		}
	}
	return root, filepath.Join(parts...), nil
}

// Open resolves p (see Resolve) and opens it for reading through os.Root, so a
// link swapped in after the checks is not followed. O_NONBLOCK keeps a FIFO
// swapped in after them from blocking the open; a regular file reads the same
// either way. The caller closes the file.
func Open(configDir, p string) (*os.File, os.FileInfo, error) {
	root, rel, err := Resolve(configDir, p)
	if err != nil {
		return nil, nil, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	f, err := r.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, ErrRefused
	}
	return f, info, nil
}

var sessionFile = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// SessionID is the session id in a transcript path (its file name without
// .jsonl), if it is a UUID.
func SessionID(p string) (string, bool) {
	name := filepath.Base(p)
	id := name[:max(0, len(name)-len(".jsonl"))]
	return id, filepath.Ext(name) == ".jsonl" && sessionFile.MatchString(id)
}

// Find looks for session id's transcript under <configDir>/projects/*/, for
// when Claude Code stored it under a directory name other than the computed one
// (it may shorten a long one). It lists the real projects/ and tries each entry
// through Resolve, so one that is a symlink, or a file reached through one, is
// ErrRefused. It returns the first path that opens or is refused; a missing id
// is a not-exist error.
func Find(configDir, id string) (string, error) {
	if !sessionFile.MatchString(id) {
		return "", ErrRefused
	}
	root, err := filepath.EvalSymlinks(filepath.Join(configDir, "projects"))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	var refused bool
	for _, e := range entries {
		p := filepath.Join(root, e.Name(), id+".jsonl")
		if _, _, err := Resolve(configDir, p); err == nil {
			return p, nil
		} else if errors.Is(err, ErrRefused) {
			refused = true
		}
	}
	if refused {
		return "", ErrRefused
	}
	return "", os.ErrNotExist
}
