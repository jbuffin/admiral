//go:build !darwin && !linux

package launcher

import "os"

// renameNoReplace is os.Rename, which refuses an existing directory but not an existing file.
func renameNoReplace(oldpath, newpath string) error {
	if _, err := os.Lstat(newpath); err == nil {
		return os.ErrExist
	}
	return os.Rename(oldpath, newpath)
}
