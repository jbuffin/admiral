package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StampFile tells an untouched copy of a plugin from one the engineer edited.
const StampFile = ".admiral-stamp"

// Stamp is SHA-256 over every file but the stamp and those go:embed skips,
// sorted by slash path, each as path, a zero byte, content, a zero byte.
func Stamp(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(files[n])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func diskFiles(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// go:embed leaves out names starting with . or _, so the stamp never
		// covered them; Finder's .DS_Store must not make a folder look edited.
		if p != dir && (strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = data
		return err
	})
	return files, err
}

var retired = []string{"freeform", "github"}

// An edited copy or a symlink stays, and a symlink's target is never touched.
func RemoveRetired(home string) error {
	var errs []error
	for _, name := range retired {
		dir := filepath.Join(pluginsDir(home), name)
		info, err := os.Lstat(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		if !info.IsDir() {
			fmt.Fprintf(os.Stderr, "admiral: plugins/%s is a retired plugin, but not a plain folder; left alone\n", name)
			continue
		}
		if _, ok := untouched(dir); !ok {
			fmt.Fprintf(os.Stderr, "admiral: plugins/%s is a retired plugin, but you edited it; left alone\n", name)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "admiral: removed the retired plugin plugins/%s\n", name)
	}
	return errors.Join(errs...)
}

func untouched(dir string) (string, bool) {
	have, err := os.ReadFile(filepath.Join(dir, StampFile))
	if err != nil {
		return "", false
	}
	onDisk, err := diskFiles(dir)
	if err != nil || Stamp(onDisk) != string(have) {
		return "", false
	}
	return string(have), true
}
