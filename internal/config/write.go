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
	"strings"
	"sync"

	"github.com/jbuffin/admiral/internal/standing"
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
	// Preset is work or home; switching it clears the Limit values and keeps quiet hours.
	Preset *string
	// Limits holds the Limit keys to change; nil leaves them as stored.
	Limits map[string]json.RawMessage
}

var (
	ErrNoFleet = errors.New("no such fleet")
	// ErrConflict: the file cannot be edited now, or changed under the write.
	ErrConflict = errors.New("config.json cannot be edited now")
)

// editFleet rewrites only the named fleet's editable fields; any other error is a value Parse refused.
// An empty color or clone_root removes the key, so the fleet falls back to its default.
// A chat harness the capability store has not cleared (read through cfg) is refused, so no caller skips the gate.
func editFleet(dir, key string, f FleetFields, cfg *Config, repos RepoLookup) error {
	if raw, ok := f.Chat["harness"]; ok {
		if err := refuseUnofferedHarness(cfg, key, raw); err != nil {
			return err
		}
	}
	if err := checkLimitsMembership(cfg, key, f, repos); err != nil {
		return err
	}
	return rewriteFleet(dir, key, func(fleets, fleet *Object) error {
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
		if f.Preset != nil || f.Limits != nil {
			if err := mergeLimits(fleet, f); err != nil {
				return err
			}
		}
		setOrDrop("clone_root", f.CloneRoot)
		setOrDrop("color", f.Color)
		return nil
	})
}

// loadFleet reads config.json and the named fleet's object; any failure but an unknown fleet is a conflict.
func loadFleet(dir, key string) (data []byte, fleets, fleet *Object, err error) {
	path := filepath.Join(dir, FileName)
	if data, err = ReadConfig(dir); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	if _, err := Parse(data); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %s is invalid, fix it first: %w", ErrConflict, path, err)
	}
	top, err := ParseObject(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	fleets = NewObject()
	if raw, ok := top.Get("fleets"); ok {
		if fleets, err = ParseObject(raw); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: %w", ErrConflict, err)
		}
	}
	raw, ok := fleets.Get(key)
	if !ok {
		return nil, nil, nil, fmt.Errorf("%w %q", ErrNoFleet, key)
	}
	if fleet, err = ParseObject(raw); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return data, fleets, fleet, nil
}

// rewriteFleet reads config.json under the write lock, lets apply change the named fleet's object (and read the others),
// and writes the file only when the result parses and the file did not change under the edit.
func rewriteFleet(dir, key string, apply func(fleets, fleet *Object) error) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(dir, FileName)
	data, fleets, fleet, err := loadFleet(dir, key)
	if err != nil {
		return err
	}
	if err := apply(fleets, fleet); err != nil {
		return err
	}
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

// checkLimitsMembership refuses a repo_allowlist patch naming a repo the fleet's search roots don't hold and its stored allowlist doesn't already list.
// A save and a preview both run it, so they cannot disagree.
func checkLimitsMembership(cfg *Config, key string, f FleetFields, repos RepoLookup) error {
	raw, ok := f.Limits["repo_allowlist"]
	if !ok || repos == nil {
		return nil
	}
	cur, known := cfg.Fleet(key)
	if !known {
		return fmt.Errorf("%w %q", ErrNoFleet, key)
	}
	var stored []string
	if f.Preset == nil || standing.Preset(*f.Preset) == cur.Preset {
		stored = cur.Limits.RepoAllowlist
	}
	return checkRepoMembership(key, raw, stored, repos(key))
}

// refuseUnofferedHarness is the server holding the line the Preferences picker draws: a harness
// the capability store has not cleared for the fleet's AdmiralBot cannot be chosen. A save that
// leaves the harness alone is not checked, so a fleet whose stored harness lapsed can still be edited.
func refuseUnofferedHarness(cfg *Config, key string, raw json.RawMessage) error {
	var harness string
	if json.Unmarshal(raw, &harness) != nil {
		return nil // the strict parse names the wrong type
	}
	f, ok := cfg.Fleet(key)
	if !ok || !slices.Contains(Harnesses(), harness) {
		return nil
	}
	if o := cfg.chatHarnessOption(f, harness); !o.Offered {
		return fmt.Errorf("fleets.%s.chat.harness: %s", key, o.Reason)
	}
	return nil
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

// limitGroups are the Limit keys whose members save one by one, so sending one ceiling leaves the others as stored.
var limitGroups = []string{"ceilings", "rates", "spend", "skip"}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

// storedPreset is the preset the fleet object holds; none means work.
func storedPreset(fleet *Object) standing.Preset {
	if raw, ok := fleet.Get("preset"); ok {
		var s string
		if json.Unmarshal(raw, &s) == nil && standing.Preset(s) == standing.Home {
			return standing.Home
		}
	}
	return standing.Work
}

// mergeLimits sets the preset, then the given Limit keys over what is stored; a null deletes the stored key,
// so that Limit reads as the preset's again. Switching preset drops every stored Limit except quiet hours,
// which belong to the fleet and not to a preset. The switch is judged on the preset in the file being edited.
func mergeLimits(fleet *Object, f FleetFields) error {
	limits := NewObject()
	if raw, ok := fleet.Get("limits"); ok {
		var err error
		if limits, err = ParseObject(raw); err != nil {
			return fmt.Errorf("%w: %w", ErrConflict, err)
		}
	}
	preset := storedPreset(fleet)
	if f.Preset != nil {
		if standing.Preset(*f.Preset) != preset {
			kept := NewObject()
			if raw, ok := limits.Get("quiet_hours"); ok {
				kept.Set("quiet_hours", raw)
			}
			limits = kept
		}
		fleet.SetValue("preset", *f.Preset)
		if p := standing.Preset(*f.Preset); p == standing.Home || p == standing.Work {
			preset = p
		}
	}
	for _, k := range slices.Sorted(maps.Keys(f.Limits)) {
		raw := f.Limits[k]
		switch {
		case isNull(raw):
			limits.Delete(k)
			continue
		case k == "ceilings":
			if merged, ok := mergeCeilings(limits, raw, preset); ok {
				raw = merged
			}
		case slices.Contains(limitGroups, k):
			if merged, ok := mergeGroup(limits, k, raw); ok {
				raw = merged
			}
		}
		if g, ok := groupObject(raw); ok && slices.Contains(limitGroups, k) && g.Len() == 0 {
			limits.Delete(k)
			continue
		}
		limits.Set(k, raw)
	}
	if limits.Len() == 0 {
		fleet.Delete("limits")
	} else {
		fleet.Set("limits", limits.JSON())
	}
	return nil
}

func groupObject(raw json.RawMessage) (*Object, bool) {
	o, err := ParseObject(raw)
	return o, err == nil
}

// mergeGroup layers the given members of a nested group over the stored ones, a null deleting its member;
// a value that is not an object is left for the parser to refuse.
func mergeGroup(limits *Object, group string, given json.RawMessage) (json.RawMessage, bool) {
	in, err := ParseObject(given)
	if err != nil {
		return nil, false
	}
	out := NewObject()
	if raw, ok := limits.Get(group); ok {
		if out, err = ParseObject(raw); err != nil {
			return nil, false
		}
	}
	for _, k := range in.Keys() {
		raw, _ := in.Get(k)
		if isNull(raw) {
			out.Delete(k)
			continue
		}
		out.Set(k, raw)
	}
	return out.JSON(), true
}

// mergeCeilings layers each given ceiling's keys over the stored ceiling, so a PUT of one key leaves the rest as stored.
func mergeCeilings(limits *Object, given json.RawMessage, preset standing.Preset) (json.RawMessage, bool) {
	in, err := ParseObject(given)
	if err != nil {
		return nil, false
	}
	out := NewObject()
	if raw, ok := limits.Get("ceilings"); ok {
		if out, err = ParseObject(raw); err != nil {
			return nil, false
		}
	}
	presetCeilings := standing.PresetLimits(preset).Ceilings
	base := map[string]standing.Ceiling{"t1": presetCeilings.T1, "t2": presetCeilings.T2, "t3": presetCeilings.T3, "t4": presetCeilings.T4, "fleet": presetCeilings.Fleet}
	for _, tier := range in.Keys() {
		raw, _ := in.Get(tier)
		if isNull(raw) {
			out.Delete(tier)
			continue
		}
		next, err := ParseObject(raw)
		if err != nil {
			out.Set(tier, raw)
			continue
		}
		cur := NewObject()
		if stored, ok := out.Get(tier); ok {
			if cur, err = ParseObject(stored); err != nil {
				cur = NewObject()
			}
		}
		for _, k := range next.Keys() {
			v, _ := next.Get(k)
			if isNull(v) && k != "threshold" { // a stored null threshold keeps the preset's from layering back
				cur.Delete(k)
				continue
			}
			cur.Set(k, v)
		}
		fixRestricted(cur, next, base[tier])
		if cur.Len() == 0 {
			out.Delete(tier)
			continue
		}
		out.Set(tier, cur.JSON())
	}
	if out.Len() == 0 {
		return json.RawMessage("{}"), true
	}
	return out.JSON(), true
}

// fixRestricted keeps a ceiling valid across a level change: leaving A3 drops the stored restricted level,
// and entering A3 without one takes the preset's, or investigate.
func fixRestricted(cur, sent *Object, preset standing.Ceiling) {
	raw, ok := sent.Get("level")
	if !ok {
		return
	}
	var name string
	if json.Unmarshal(raw, &name) != nil {
		return
	}
	level, ok := standing.ParseLevel(name)
	if !ok {
		return
	}
	if _, given := sent.Get("restricted"); given {
		return
	}
	switch {
	case level != standing.A3:
		cur.Delete("restricted")
	default:
		if _, has := cur.Get("restricted"); has {
			return
		}
		r := standing.Investigate
		if preset.Restricted != "" {
			r = preset.Restricted
		}
		cur.SetValue("restricted", string(r))
	}
}

// checkRepoMembership refuses a sent entry the fleet's search roots do not find, unless it is already stored,
// so a checkout that moved never blocks a save that keeps it. An entry of the wrong shape is left for the parser, which names it.
func checkRepoMembership(key string, raw json.RawMessage, stored, found []string) error {
	var given []string
	if json.Unmarshal(raw, &given) != nil {
		return nil
	}
	has := func(list []string, r string) bool {
		return slices.ContainsFunc(list, func(f string) bool { return strings.EqualFold(f, r) })
	}
	for i, r := range given {
		if repoRe.MatchString(r) && !has(stored, r) && !has(found, r) {
			return fmt.Errorf("fleets.%s.limits.repo_allowlist[%d]: %q is not a repo under the fleet's search roots", key, i, r)
		}
	}
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
