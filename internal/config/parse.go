// Package config loads, validates, watches and writes $ADMIRAL_HOME/config.json
// (ADR 0010): the fleets, their harnesses and the few settings the UI writes.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Version is the config format this Admiral reads.
const Version = 1

// MaxFavoriteSkills is how many favorite skills the launch form can hold.
const MaxFavoriteSkills = 5

// Config is a validated config.json with everything derived from it.
type Config struct {
	Fleets         []Fleet // file order is marina order
	FavoriteSkills []string
	Sources        json.RawMessage // as in the file; nil when there is no sources key
	SourceList     []Source        // Sources, in panel order
}

// Fleet is one marina.
type Fleet struct {
	Key         string
	Name        string
	Color       string // resolved: explicit, else the next in the default sequence
	Ink         string // text color on Color
	SearchRoots []string
	Harnesses   []Harness // dock order: claude, copilot, codex, as present
}

func (c *Config) clone() *Config {
	out := *c
	out.FavoriteSkills = slices.Clone(c.FavoriteSkills)
	out.Sources = slices.Clone(c.Sources)
	out.SourceList = slices.Clone(c.SourceList)
	out.Fleets = make([]Fleet, len(c.Fleets))
	for i, f := range c.Fleets {
		f.SearchRoots = slices.Clone(f.SearchRoots)
		f.Harnesses = slices.Clone(f.Harnesses)
		for j, h := range f.Harnesses {
			h.Args = slices.Clone(h.Args)
			h.Env = maps.Clone(h.Env)
			h.Warnings = slices.Clone(h.Warnings)
			f.Harnesses[j] = h
		}
		out.Fleets[i] = f
	}
	return &out
}

// CanLaunch is false for a fleet with no search roots: its docks show, but
// nothing can be launched from it.
func (f Fleet) CanLaunch() bool { return len(f.SearchRoots) > 0 }

// Harness is one dock of a fleet.
type Harness struct {
	Name       string // claude, copilot or codex
	Executable string // the resolved path when Found, else as configured
	Found      bool
	Args       []string
	Env        map[string]string // the fleet's own entries, ~ expanded
	Warnings   []string
}

// Environ is Admiral's environment overlaid with the fleet's env.
func (h Harness) Environ() []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for k, v := range h.Env {
		env[k] = v
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// dockOrder is the prototype's order, whatever the file's.
var dockOrder = []string{"claude", "copilot", "codex"}

var (
	fleetKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	colorRe    = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// Parse validates data and derives the per-dock state. The error names the
// path of the bad value, without the file name.
func Parse(data []byte) (*Config, error) {
	top, err := members("", data)
	if err != nil {
		return nil, err
	}
	cfg := &Config{FavoriteSkills: []string{}}
	var fleetsRaw json.RawMessage
	haveVersion := false
	for _, m := range top {
		switch m.key {
		case "version":
			haveVersion = true
			if err := checkVersion(m.raw); err != nil {
				return nil, err
			}
		case "fleets":
			fleetsRaw = m.raw
		case "favorite_skills":
			if cfg.FavoriteSkills, err = stringList("favorite_skills", m.raw); err != nil {
				return nil, err
			}
			if len(cfg.FavoriteSkills) > MaxFavoriteSkills {
				return nil, fmt.Errorf("favorite_skills: at most %d allowed, got %d", MaxFavoriteSkills, len(cfg.FavoriteSkills))
			}
		case "sources":
			cfg.Sources = append(json.RawMessage(nil), m.raw...)
			if cfg.SourceList, err = parseSources(m.raw); err != nil {
				return nil, err
			}
		}
		// Anything else is kept in the file and ignored here (story 33).
	}
	if !haveVersion {
		return nil, fmt.Errorf("version: required, must be %d", Version)
	}
	if fleetsRaw != nil {
		if cfg.Fleets, err = parseFleets(fleetsRaw); err != nil {
			return nil, err
		}
	}
	assignColors(cfg.Fleets)
	markSharedIdentities(cfg.Fleets)
	return cfg, nil
}

func checkVersion(raw json.RawMessage) error {
	n, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
	if err != nil {
		return fmt.Errorf("version: must be an integer, got %s", raw)
	}
	switch {
	case n > Version:
		return fmt.Errorf("version: file is version %d, but this Admiral reads version %d at most", n, Version)
	case n < 1:
		return fmt.Errorf("version: must be %d or higher, got %d", Version, n)
	}
	return nil
}

func parseFleets(raw json.RawMessage) ([]Fleet, error) {
	ms, err := members("fleets", raw)
	if err != nil {
		return nil, err
	}
	fleets := []Fleet{}
	for _, m := range ms {
		path := "fleets." + m.key
		if !fleetKeyRe.MatchString(m.key) {
			return nil, fmt.Errorf("%s: fleet keys may hold only letters, digits, - and _", path)
		}
		f, err := parseFleet(path, m.key, m.raw)
		if err != nil {
			return nil, err
		}
		fleets = append(fleets, f)
	}
	return fleets, nil
}

func parseFleet(path, key string, raw json.RawMessage) (Fleet, error) {
	ms, err := members(path, raw)
	if err != nil {
		return Fleet{}, err
	}
	f := Fleet{Key: key, SearchRoots: []string{}}
	harnesses := map[string]Harness{}
	haveName := false
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "name":
			haveName = true
			if f.Name, err = str(p, m.raw); err != nil {
				return Fleet{}, err
			}
			if strings.TrimSpace(f.Name) == "" {
				return Fleet{}, fmt.Errorf("%s: must not be empty", p)
			}
		case "color":
			if f.Color, err = str(p, m.raw); err != nil {
				return Fleet{}, err
			}
			if !colorRe.MatchString(f.Color) {
				return Fleet{}, fmt.Errorf("%s: must look like #RRGGBB, got %q", p, f.Color)
			}
		case "search_roots":
			if f.SearchRoots, err = searchRoots(p, m.raw); err != nil {
				return Fleet{}, err
			}
		case "claude", "codex", "copilot":
			if harnesses[m.key], err = parseHarness(p, m.key, m.raw); err != nil {
				return Fleet{}, err
			}
		default:
			return Fleet{}, fmt.Errorf("%s: unknown key", p)
		}
	}
	if !haveName {
		return Fleet{}, fmt.Errorf("%s.name: required", path)
	}
	for _, name := range dockOrder {
		if h, ok := harnesses[name]; ok {
			f.Harnesses = append(f.Harnesses, h)
		}
	}
	return f, nil
}

func parseHarness(path, name string, raw json.RawMessage) (Harness, error) {
	ms, err := members(path, raw)
	if err != nil {
		return Harness{}, err
	}
	h := Harness{Name: name, Args: []string{}, Env: map[string]string{}}
	exe := name
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "executable":
			if exe, err = str(p, m.raw); err != nil {
				return Harness{}, err
			}
			if exe == "" {
				return Harness{}, fmt.Errorf("%s: must not be empty", p)
			}
			exe = expandHome(exe)
		case "args":
			if h.Args, err = stringList(p, m.raw); err != nil {
				return Harness{}, err
			}
		case "env":
			if h.Env, err = envMap(p, m.raw); err != nil {
				return Harness{}, err
			}
		default:
			return Harness{}, fmt.Errorf("%s: unknown key", p)
		}
	}
	h.Executable, h.Found = resolveExecutable(exe, h.Env)
	return h, nil
}

func searchRoots(path string, raw json.RawMessage) ([]string, error) {
	roots, err := stringList(path, raw)
	if err != nil {
		return nil, err
	}
	for i, r := range roots {
		if !strings.HasPrefix(r, "~/") && !filepath.IsAbs(r) {
			return nil, fmt.Errorf("%s[%d]: must be an absolute path or start with ~/, got %q", path, i, r)
		}
		roots[i] = filepath.Clean(expandHome(r))
	}
	return roots, nil
}

func envMap(path string, raw json.RawMessage) (map[string]string, error) {
	ms, err := members(path, raw)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, m := range ms {
		v, err := str(path+"."+m.key, m.raw)
		if err != nil {
			return nil, err
		}
		env[m.key] = expandEnvValue(m.key, v)
	}
	return env, nil
}

type member struct {
	key string
	raw json.RawMessage
}

// members decodes a JSON object keeping key order and refusing duplicates.
func members(path string, raw []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, pathErr(path, "must be an object")
	}
	var out []member
	seen := map[string]bool{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		key := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		if seen[key] {
			return nil, pathErr(join(path, key), "duplicate key")
		}
		seen[key] = true
		out = append(out, member{key, v})
	}
	if _, err := dec.Token(); err != nil { // closing }
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("invalid JSON: unexpected data after the top-level object")
	}
	return out, nil
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func pathErr(path, msg string) error {
	if path == "" {
		return fmt.Errorf("the top level %s", msg)
	}
	return fmt.Errorf("%s: %s", path, msg)
}

func str(path string, raw json.RawMessage) (string, error) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", fmt.Errorf("%s: must be a string", path)
	}
	return s, nil
}

func stringList(path string, raw json.RawMessage) ([]string, error) {
	var items []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
		return nil, fmt.Errorf("%s: must be an array of strings", path)
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		s, err := str(fmt.Sprintf("%s[%d]", path, i), it)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
