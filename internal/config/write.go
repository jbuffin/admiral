package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

var writeMu sync.Mutex

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

// SetRemote writes the remote section; allowedLogins is always written, so a cleared list stays visible.
func SetRemote(dir string, r Remote) error {
	raw, err := remoteJSON(r)
	if err != nil {
		return err
	}
	return setKey(dir, "remote", raw)
}

func remoteJSON(r Remote) (json.RawMessage, error) {
	return json.Marshal(struct {
		Tailscale     bool     `json:"tailscale"`
		AllowedLogins []string `json:"allowedLogins"`
	}{r.Tailscale, nonNil(r.AllowedLogins)})
}

// SetFleetsAndRemote writes both in one replace, so setup's diff and its write are the same change.
func SetFleetsAndRemote(dir string, fleets json.RawMessage, r Remote) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	_, out, err := renderKeys(path, keyValue{"fleets", fleets}, remoteKV(r))
	if err != nil {
		return err
	}
	if _, err := Parse(out); err != nil {
		return err
	}
	return replaceFile(path, out)
}

// PreviewFleetsAndRemote is PreviewFleets for setup's combined write.
func PreviewFleetsAndRemote(dir string, fleets json.RawMessage, r Remote) (before, after []byte, err error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	before, after, err = renderKeys(filepath.Join(dir, FileName), keyValue{"fleets", fleets}, remoteKV(r))
	if err != nil {
		return nil, nil, err
	}
	if _, err := Parse(after); err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

func remoteKV(r Remote) keyValue {
	raw, _ := remoteJSON(r) // a struct of a bool and strings always marshals
	return keyValue{"remote", raw}
}

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
	// The whole file, not the sources alone: a source names a fleet.
	if _, err := Parse(out); err != nil {
		return err
	}
	return replaceFile(path, out)
}

// setKey leaves a file that doesn't validate alone.
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

// SetFleets checks the result loads; setKey validates only what it read.
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

// FleetFields are the fleet settings the HTTP API may change. A nil field is left as stored.
// Docks and env stay with the setup terminal, so a browser on the LAN cannot change what runs.
type FleetFields struct {
	SearchRoots   *[]string
	Owners        *[]string
	WakeReviewers *[]string
	CloneRoot     *string
	Color         *string
	Notify        *bool
	// Chat holds the chat settings to change, by key; nil leaves them as stored.
	Chat map[string]json.RawMessage
}

var (
	ErrNoFleet = errors.New("no such fleet")
	// ErrConflict: the file cannot be edited now, or changed under the write.
	ErrConflict = errors.New("config.json cannot be edited now")
)

// EditFleet rewrites only the named fleet's editable fields; any other error is a value Parse refused.
// An empty color or clone_root removes the key, so the fleet falls back to its default.
func EditFleet(dir, key string, f FleetFields) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	data, err := ReadConfig(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("%w: %s is invalid, fix it first: %w", ErrConflict, path, err)
	}
	top, err := ParseObject(data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	fleets := NewObject()
	if raw, ok := top.Get("fleets"); ok {
		if fleets, err = ParseObject(raw); err != nil {
			return fmt.Errorf("%w: %w", ErrConflict, err)
		}
	}
	raw, ok := fleets.Get(key)
	if !ok {
		return fmt.Errorf("%w %q", ErrNoFleet, key)
	}
	fleet, err := ParseObject(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	if f.SearchRoots != nil {
		fleet.SetValue("search_roots", nonNil(*f.SearchRoots))
	}
	if f.Owners != nil {
		fleet.SetValue("owners", nonNil(*f.Owners))
	}
	if f.WakeReviewers != nil {
		fleet.SetValue("wake_reviewers", nonNil(*f.WakeReviewers))
	}
	setOrDrop := func(k string, v *string) {
		switch {
		case v == nil:
		case *v == "":
			fleet.Delete(k)
		default:
			fleet.SetValue(k, *v)
		}
	}
	if f.Chat != nil {
		if err := mergeChat(fleet, key, f.Chat); err != nil {
			return err
		}
	}
	if f.Notify != nil {
		fleet.SetValue("notify", *f.Notify)
	}
	setOrDrop("clone_root", f.CloneRoot)
	setOrDrop("color", f.Color)
	fleets.Set(key, fleet.JSON())
	_, out, err := renderKey(path, "fleets", fleets.JSON())
	if err != nil {
		// renderKey reads the file again: an error from it means the file changed under us.
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	if _, err := Parse(out); err != nil {
		return err
	}
	// A CLI write is another process and takes no lock here.
	if now, err := readConfig(path); err != nil || !bytes.Equal(now, data) {
		return fmt.Errorf("%w: config.json changed while saving; try again", ErrConflict)
	}
	return replaceFile(path, out)
}

// mergeChat refuses what parseFleetChat would replace with a default, then sets only the given
// keys. An empty effort removes its key, so the fleet falls back to the harness default.
// Switching harness also checks the model and effort the fleet keeps against the new one.
func mergeChat(fleet *Object, key string, set map[string]json.RawMessage) error {
	given := NewObject()
	for _, k := range slices.Sorted(maps.Keys(set)) {
		given.Set(k, set[k])
	}
	path := "fleets." + key + ".chat"
	stored := json.RawMessage("{}")
	if raw, ok := fleet.Get("chat"); ok {
		stored = raw
	}
	cur, err := parseFleetChat(path, stored, false, DefaultChatHarness)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	next, err := parseFleetChat(path, given.JSON(), true, cur.Harness)
	if err != nil {
		return err
	}
	if _, ok := given.Get("harness"); ok {
		if _, ok := given.Get("model"); !ok && !slices.Contains(ChatModelsFor(next.Harness), cur.Model) {
			return fmt.Errorf("%s.model: %q is not one of %v for %s: send a model with the harness", path, cur.Model, ChatModelsFor(next.Harness), next.Harness)
		}
		if _, ok := given.Get("effort"); !ok && !slices.Contains(ChatEffortsFor(next.Harness), cur.Effort) {
			return fmt.Errorf("%s.effort: %q is not one of %q for %s: send an effort with the harness", path, cur.Effort, ChatEffortsFor(next.Harness), next.Harness)
		}
	}
	chat := NewObject()
	if _, ok := fleet.Get("chat"); ok {
		if chat, err = ParseObject(stored); err != nil {
			return fmt.Errorf("%w: %w", ErrConflict, err)
		}
	}
	for _, k := range given.Keys() {
		raw, _ := given.Get(k)
		if k == "effort" && string(bytes.TrimSpace(raw)) == `""` {
			chat.Delete(k)
			continue
		}
		chat.Set(k, raw)
	}
	fleet.Set("chat", chat.JSON())
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// PreviewFleets indents both the way Admiral writes, so a diff shows only what changed.
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

func Fleets(dir string) (*Object, error) {
	data, err := ReadConfig(dir)
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

// WriteFile saves the text as it is, so a hand edit keeps its layout.
func WriteFile(dir string, data []byte) error {
	if _, err := Parse(data); err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	return replaceFile(filepath.Join(dir, FileName), data)
}

// ReadConfig refuses a file Admiral would migrate at startup: commands never migrate.
func ReadConfig(dir string) ([]byte, error) {
	data, err := readConfig(filepath.Join(dir, FileName))
	if err == nil && NeedsMigration(data) {
		return nil, ErrNeedsMigration
	}
	return data, err
}

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

type keyValue struct {
	key   string
	value json.RawMessage
}

func renderKey(path, key string, value json.RawMessage) (before, after []byte, err error) {
	return renderKeys(path, keyValue{key, value})
}

// renderKeys sets each key in place, and appends the ones the file lacks in the order given.
func renderKeys(path string, set ...keyValue) (before, after []byte, err error) {
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
	done := map[string]bool{}
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
		replaced := false
		for _, kv := range set {
			if m.key == kv.key {
				write(kv.key, kv.value)
				done[kv.key] = true
				replaced = true
			}
		}
		if !replaced {
			write(m.key, m.raw)
		}
	}
	for _, kv := range set {
		if !done[kv.key] {
			write(kv.key, kv.value)
		}
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

// replaceFile renames over path, so a reader sees the old file or the new one.
// A symlinked path keeps its link: the target is replaced.
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
