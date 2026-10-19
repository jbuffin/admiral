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

	"github.com/jbuffin/admiral/plugins"
)

// StampFile sits in a plugin folder Admiral wrote. It holds the hex SHA-256 of
// the folder's files as written, so a later start can tell an untouched copy
// from one the engineer edited.
const StampFile = ".admiral-stamp"

// Stamp is the hash a folder's files carry in StampFile: SHA-256 over every
// file but the stamp and those go:embed skips (names starting with . or _),
// sorted by slash-separated path relative to the folder,
// each as path, a zero byte, content, a zero byte.
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

func embedded(name string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(plugins.Shipped, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(plugins.Shipped, p)
		files[p[len(name)+1:]] = data
		return err
	})
	return files, err
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

// InstallPlugins installs every plugin Admiral ships. A plugin that fails does
// not stop the others; the errors come back joined.
func InstallPlugins(home string) error {
	var errs []error
	for _, name := range plugins.Names {
		errs = append(errs, installPlugin(home, name))
	}
	return errors.Join(errs...)
}

// installPlugin writes the embedded plugin to plugins/<name>/ when the folder
// is missing, and replaces it with a newer embedded copy when the folder still
// matches its stamp. An edited folder, or one without a stamp, is left alone.
func installPlugin(home, name string) error {
	files, err := embedded(name)
	if err != nil {
		return err
	}
	want := Stamp(files)
	dir := filepath.Join(pluginsDir(home), name)

	if _, err := os.Lstat(dir); err == nil {
		have, err := os.ReadFile(filepath.Join(dir, StampFile))
		if err != nil || string(have) == want {
			return nil
		}
		onDisk, err := diskFiles(dir)
		if err != nil || Stamp(onDisk) != string(have) {
			return nil // edited by hand, or not readable: not ours to replace
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(pluginsDir(home), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(pluginsDir(home), "."+name+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for name, data := range files {
		p := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, StampFile), []byte(want), 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("install %s: %w", name, err)
	}
	return nil
}
