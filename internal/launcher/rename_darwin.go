package launcher

import "golang.org/x/sys/unix"

// renameNoReplace moves oldpath to newpath, failing with EEXIST when newpath exists.
func renameNoReplace(oldpath, newpath string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_EXCL)
}
