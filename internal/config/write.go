package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// writeMu serializes read-modify-write cycles inside this process.
var writeMu sync.Mutex

// SetFavoriteSkills replaces favorite_skills in dir/config.json.
func SetFavoriteSkills(dir string, skills []string) error {
	if len(skills) > MaxFavoriteSkills {
		return fmt.Errorf("favorite_skills: at most %d allowed, got %d", MaxFavoriteSkills, len(skills))
	}
	if skills == nil {
		skills = []string{}
	}
	raw, err := json.Marshal(skills)
	if err != nil {
		return err
	}
	return setKey(dir, "favorite_skills", raw)
}

// SetSources replaces sources in dir/config.json.
func SetSources(dir string, sources json.RawMessage) error {
	if !json.Valid(sources) {
		return fmt.Errorf("sources: not valid JSON")
	}
	return setKey(dir, "sources", sources)
}

// setKey replaces one top-level key, keeping every other key, value and the
// fleets' order, and writes the file atomically. A file that doesn't validate
// is left alone; a missing one is created.
func setKey(dir, key string, value json.RawMessage) error {
	writeMu.Lock()
	defer writeMu.Unlock()

	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if _, lerr := os.Lstat(path); lerr == nil {
			// A dangling symlink: writing would replace the link with a plain file.
			return fmt.Errorf("%s is a symlink to a file that is missing, restore it first", path)
		}
		data = []byte(`{"version":1,"fleets":{}}`)
	} else if err != nil {
		return err
	}
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("%s is invalid, fix it first: %w", path, err)
	}
	top, err := members("", data)
	if err != nil {
		return err
	}

	var out bytes.Buffer
	out.WriteByte('{')
	replaced := false
	write := func(k string, raw json.RawMessage) {
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		out.Write(kb)
		out.WriteByte(':')
		out.Write(raw)
	}
	for _, m := range top {
		if m.key == key {
			write(key, value)
			replaced = true
		} else {
			write(m.key, m.raw)
		}
	}
	if !replaced {
		write(key, value)
	}
	out.WriteByte('}')
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out.Bytes(), "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')
	return replaceFile(path, pretty.Bytes())
}

// replaceFile writes a temp file beside path and renames it over, so a reader
// sees the old file or the new one. A symlinked path keeps its link: the
// target is replaced.
func replaceFile(path string, data []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+FileName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
