// Package transcriptfs opens a harness's transcript file. The path comes from a pane option anything in the pane can write, so only a regular file under <config dir>/projects/ with no symlink on the way is read.
package transcriptfs

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// ErrRefused is a path Admiral will not read; one that is merely missing is a not-exist error.
var ErrRefused = errors.New("not a regular transcript file under the Claude config directory's projects/")

// Resolve returns the real projects/ and the path below it. The config dir and projects/ may be links (a dotfiles ~/.claude); nothing below them may.
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

// Open goes through os.Root so a link swapped in after the checks is not followed; O_NONBLOCK keeps a swapped-in FIFO from blocking.
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

func SessionID(p string) (string, bool) {
	name := filepath.Base(p)
	id := name[:max(0, len(name)-len(".jsonl"))]
	return id, filepath.Ext(name) == ".jsonl" && sessionFile.MatchString(id)
}

// Find searches projects/*/ for a session stored under a directory name other than the computed one. A symlinked entry is ErrRefused.
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
