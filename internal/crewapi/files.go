package crewapi

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unicode/utf8"
)

const maxFile = 65536

var (
	errLink       = errors.New("a link is in the path, refused")
	errNotDir     = errors.New("a directory is a file, refused")
	errUnreadable = errors.New("the file is not a text file under 65536 bytes")
)

// Every write under a crew directory holds this lock, so two requests never read the same file and
// write over each other.
var writeMu sync.Mutex

// walk follows parts down from base one component at a time with Lstat, since os.Root and MkdirAll follow a
// link that stays inside the root. A missing component ends the walk with found false, or is made when create is set.
func walk(base string, create bool, parts ...string) (dir string, found bool, err error) {
	dir = base
	for _, part := range parts {
		dir = filepath.Join(dir, part)
		fi, err := os.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
			if !create {
				return dir, false, nil
			}
			if err := os.Mkdir(dir, 0o700); err != nil {
				return "", false, err
			}
		case err != nil:
			return "", false, err
		case fi.Mode()&fs.ModeSymlink != 0:
			return "", false, errLink
		case !fi.IsDir():
			return "", false, errNotDir
		}
	}
	return dir, true, nil
}

// readRegular reads a file that is neither a link nor over the size limit; a missing file is found false.
func readRegular(path string) (raw []byte, found bool, err error) {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, false, errLink
	case !fi.Mode().IsRegular():
		return nil, false, errUnreadable
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, false, errLink
		}
		return nil, false, err
	}
	defer f.Close()
	raw, err = io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) > maxFile || !utf8.Valid(raw) {
		return nil, false, errUnreadable
	}
	return raw, true, nil
}

// writeAtomic replaces dir/name through a temp file in dir, which the resolver ignores for its leading dot.
func writeAtomic(dir, name string, data []byte) error {
	dst := filepath.Join(dir, name)
	if fi, err := os.Lstat(dst); err == nil && !fi.Mode().IsRegular() {
		return errLink
	}
	tmp, err := os.CreateTemp(dir, ".crew-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
