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

// SetChat replaces chat in dir/config.json. Fleet and model are always written;
// effort only when set, so the harness default leaves no field.
func SetChat(dir string, c Chat) error {
	obj := map[string]string{"fleet": c.Fleet, "model": c.Model}
	if c.Effort != "" {
		obj["effort"] = c.Effort
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	_, out, err := renderKey(path, "chat", raw)
	if err != nil {
		return err
	}
	if _, err := Parse(out); err != nil {
		return err
	}
	return replaceFile(path, out)
}

// SetSources replaces sources in dir/config.json.
func SetSources(dir string, sources json.RawMessage) error {
	if !json.Valid(sources) {
		return fmt.Errorf("sources: not valid JSON")
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	_, out, err := renderKey(path, "sources", sources)
	if err != nil {
		return err
	}
	// The whole file, not the sources alone: an autopilot entry names a fleet.
	if _, err := Parse(out); err != nil {
		return err
	}
	return replaceFile(path, out)
}

// setKey replaces one top-level key, keeping every other key, value and the
// fleets' order, and writes the file atomically. A file that doesn't validate
// is left alone; a missing one is created.
func setKey(dir, key string, value json.RawMessage) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	_, out, err := renderKey(path, key, value)
	if err != nil {
		return err
	}
	return replaceFile(path, out)
}

// SetFleets replaces fleets in dir/config.json. The result must load, which
// setKey alone does not check (it validates only what it read).
func SetFleets(dir string, fleets json.RawMessage) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	_, out, err := renderKey(path, "fleets", fleets)
	if err != nil {
		return err
	}
	if _, err := Parse(out); err != nil {
		return err
	}
	return replaceFile(path, out)
}

// PreviewFleets is the config.json before and after SetFleets, both indented
// the way Admiral writes it, so a diff shows only what changed. A missing file
// is the empty config.
func PreviewFleets(dir string, fleets json.RawMessage) (before, after []byte, err error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	before, after, err = renderKey(filepath.Join(dir, FileName), "fleets", fleets)
	if err != nil {
		return nil, nil, err
	}
	if _, err := Parse(after); err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// Fleets is the fleets object of dir/config.json in file order; empty when
// there is no file. A file that doesn't validate is an error.
func Fleets(dir string) (*Object, error) {
	data, err := readConfig(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	if _, err := Parse(data); err != nil {
		return nil, fmt.Errorf("%s is invalid, fix it first (admiral config edit): %w", filepath.Join(dir, FileName), err)
	}
	top, err := ParseObject(data)
	if err != nil {
		return nil, err
	}
	raw, ok := top.Get("fleets")
	if !ok {
		return NewObject(), nil
	}
	return ParseObject(raw)
}

// WriteFile replaces dir/config.json with data, which must load. The text is
// saved as it is, so a hand edit keeps its layout.
func WriteFile(dir string, data []byte) error {
	if _, err := Parse(data); err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	return replaceFile(filepath.Join(dir, FileName), data)
}

// ReadConfig is dir/config.json, or the empty config when there is no file yet.
// A dangling symlink is an error: writing would replace the link.
func ReadConfig(dir string) ([]byte, error) { return readConfig(filepath.Join(dir, FileName)) }

func readConfig(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if _, lerr := os.Lstat(path); lerr == nil {
			// A dangling symlink: writing would replace the link with a plain file.
			return nil, fmt.Errorf("%s is a symlink to a file that is missing, restore it first", path)
		}
		return []byte(`{"version":1,"fleets":{}}`), nil
	}
	return data, err
}

// renderKey returns the file as it is (indented) and with key replaced.
func renderKey(path, key string, value json.RawMessage) (before, after []byte, err error) {
	data, err := readConfig(path)
	if err != nil {
		return nil, nil, err
	}
	if _, err := Parse(data); err != nil {
		return nil, nil, fmt.Errorf("%s is invalid, fix it first: %w", path, err)
	}
	top, err := members("", data)
	if err != nil {
		return nil, nil, err
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
	if after, err = indent(out.Bytes()); err != nil {
		return nil, nil, err
	}
	before, err = indent(data)
	return before, after, err
}

func indent(raw []byte) ([]byte, error) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
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
