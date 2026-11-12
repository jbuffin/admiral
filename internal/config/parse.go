// Package config loads, validates, watches and writes $ADMIRAL_HOME/config.json.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const Version = 1

const MaxFavoriteSkills = 5

type Config struct {
	Fleets         []Fleet // file order is marina order
	FavoriteSkills []string
	Sources        json.RawMessage // as in the file; nil when there is no sources key
	SourceList     []Source        // Sources, in panel order
	// MaxShips counts every ship underway or needing you, hand launches included.
	MaxShips  int
	Resources map[string]Resource
	// Chat is resolved: Fleet is never a missing fleet, and Model is never empty.
	Chat   Chat
	Remote Remote
}

// Remote is Admiral-wide: the Tailscale mode and who may use it.
type Remote struct {
	Tailscale     bool
	AllowedLogins []string
}

// Chat's Effort is empty for the harness default, which config.json leaves out.
type Chat struct {
	Fleet  string
	Model  string
	Effort string
}

const DefaultChatModel = "sonnet"

// An empty effort is the harness default.
var (
	ChatModels  = []string{"sonnet", "opus", "haiku"}
	ChatEfforts = []string{"", "low", "medium", "high"}
)

type Fleet struct {
	Key           string
	Name          string
	Color         string // resolved: explicit, else the next in the default sequence
	Ink           string // text color on Color
	ColorIndex    *int   // position in the default sequence; nil for a custom Color
	SearchRoots   []string
	Owners        []string          // GitHub orgs and users whose work belongs to the fleet
	WakeReviewers []string          // GitHub logins whose feedback wakes a moored ship
	CloneRoot     string            // where the fleet's clones live; empty when unset
	Env           map[string]string // the fleet's own env, ~ expanded; plugin sources and gh calls start with it
	Harnesses     []Harness         // dock order: claude, copilot, codex, as present
}

func (c *Config) clone() *Config {
	out := *c
	out.FavoriteSkills = slices.Clone(c.FavoriteSkills)
	out.Sources = slices.Clone(c.Sources)
	out.Resources = c.cloneResources()
	out.Remote.AllowedLogins = slices.Clone(c.Remote.AllowedLogins)
	out.SourceList = make([]Source, len(c.SourceList))
	for i, s := range c.SourceList {
		out.SourceList[i] = s.clone()
	}
	out.Fleets = make([]Fleet, len(c.Fleets))
	for i, f := range c.Fleets {
		f.SearchRoots = slices.Clone(f.SearchRoots)
		f.Owners = slices.Clone(f.Owners)
		f.WakeReviewers = slices.Clone(f.WakeReviewers)
		f.Env = maps.Clone(f.Env)
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

func (f Fleet) CanLaunch() bool { return len(f.SearchRoots) > 0 }

type Harness struct {
	Name       string // claude, copilot or codex
	Executable string // the resolved path when Found, else as configured
	Found      bool
	Args       []string
	Env        map[string]string // the fleet's own entries, ~ expanded
	Warnings   []string
}

func (h Harness) Environ() []string { return overlayEnv(h.Env) }

func (f Fleet) Environ() []string { return overlayEnv(f.Env) }

func overlayEnv(own map[string]string) []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for k, v := range own {
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
	// A GitHub login: letters, digits and single hyphens, none at either end; an app's [bot] suffix is allowed. The regexp cannot count the hyphens, so the 39-character bound is checked beside it.
	loginRe = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9]){0,38}(?:\[bot\])?$`)
	colorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// ValidLogin is whether l has the shape of a GitHub login, an app's [bot] suffix included.
func ValidLogin(l string) bool {
	return loginRe.MatchString(l) && len(strings.TrimSuffix(l, "[bot]")) <= 39
}

// Parse errors name the path of the bad value, without the file name.
func Parse(data []byte) (*Config, error) {
	top, err := members("", data)
	if err != nil {
		return nil, err
	}
	cfg := &Config{FavoriteSkills: []string{}, MaxShips: DefaultMaxShips}
	var fleetsRaw json.RawMessage
	haveVersion := false
	var chat Chat
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
		case "max_ships":
			if cfg.MaxShips, err = positiveInt("max_ships", m.raw); err != nil {
				return nil, err
			}
		case "chat":
			if chat, err = parseChat(m.raw); err != nil {
				return nil, err
			}
		case "remote":
			if cfg.Remote, err = parseRemote(m.raw); err != nil {
				return nil, err
			}
		case "resources":
			if cfg.Resources, err = parseResources(m.raw); err != nil {
				return nil, err
			}
		case "sources":
			cfg.Sources = append(json.RawMessage(nil), m.raw...)
			if cfg.SourceList, err = parseSources(m.raw); err != nil {
				return nil, err
			}
		}
		// Anything else is kept in the file and ignored here.
	}
	if !haveVersion {
		return nil, fmt.Errorf("version: required, must be %d", Version)
	}
	if fleetsRaw != nil {
		if cfg.Fleets, err = parseFleets(fleetsRaw); err != nil {
			return nil, err
		}
	}
	if err := checkSourceFleets(cfg); err != nil {
		return nil, err
	}
	cfg.Chat = resolveChat(chat, cfg.Fleets)
	assignColors(cfg.Fleets)
	markSharedIdentities(cfg.Fleets)
	return cfg, nil
}

func parseRemote(raw json.RawMessage) (Remote, error) {
	ms, err := members("remote", raw)
	if err != nil {
		return Remote{}, err
	}
	var r Remote
	for _, m := range ms {
		p := "remote." + m.key
		switch m.key {
		case "tailscale":
			if err = json.Unmarshal(m.raw, &r.Tailscale); err != nil {
				return Remote{}, fmt.Errorf("%s: must be true or false", p)
			}
		case "allowedLogins":
			if r.AllowedLogins, err = stringList(p, m.raw); err != nil {
				return Remote{}, err
			}
			// Trimmed like the request's login, so a hand-padded entry still matches.
			for i, l := range r.AllowedLogins {
				r.AllowedLogins[i] = strings.TrimSpace(l)
			}
		default:
			return Remote{}, fmt.Errorf("%s: unknown key", p)
		}
	}
	return r, nil
}

func parseChat(raw json.RawMessage) (Chat, error) {
	ms, err := members("chat", raw)
	if err != nil {
		return Chat{}, err
	}
	var c Chat
	for _, m := range ms {
		p := "chat." + m.key
		switch m.key {
		case "fleet":
			c.Fleet, err = str(p, m.raw)
		case "model":
			c.Model, err = str(p, m.raw)
		case "effort":
			c.Effort, err = str(p, m.raw)
		default:
			return Chat{}, fmt.Errorf("%s: unknown key", p)
		}
		if err != nil {
			return Chat{}, err
		}
	}
	return c, nil
}

// resolveChat never fails the load: a bad fleet, model or effort falls back to
// the default with a warning.
func resolveChat(c Chat, fleets []Fleet) Chat {
	if c.Model != "" && !slices.Contains(ChatModels, c.Model) {
		log.Printf("config: chat.model %q is not one of %v, using %s", c.Model, ChatModels, DefaultChatModel)
		c.Model = ""
	}
	if c.Model == "" {
		c.Model = DefaultChatModel
	}
	if !slices.Contains(ChatEfforts, c.Effort) {
		log.Printf("config: chat.effort %q is not one of %q, using the harness default", c.Effort, ChatEfforts)
		c.Effort = ""
	}
	if slices.ContainsFunc(fleets, func(f Fleet) bool { return f.Key == c.Fleet }) {
		return c
	}
	c.Fleet = ""
	for _, f := range fleets {
		if slices.ContainsFunc(f.Harnesses, func(h Harness) bool { return h.Name == "claude" }) {
			c.Fleet = f.Key
			return c
		}
	}
	if len(fleets) > 0 {
		c.Fleet = fleets[0].Key
	}
	return c
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
		case "owners":
			if f.Owners, err = stringList(p, m.raw); err != nil {
				return Fleet{}, err
			}
			for i, o := range f.Owners {
				if strings.TrimSpace(o) == "" {
					return Fleet{}, fmt.Errorf("%s[%d]: must not be empty", p, i)
				}
			}
		case "wake_reviewers":
			if f.WakeReviewers, err = stringList(p, m.raw); err != nil {
				return Fleet{}, err
			}
			for i, l := range f.WakeReviewers {
				if !ValidLogin(l) {
					return Fleet{}, fmt.Errorf("%s[%d]: must be a GitHub login, got %q", p, i, l)
				}
			}
		case "clone_root":
			if f.CloneRoot, err = str(p, m.raw); err != nil {
				return Fleet{}, err
			}
			if !strings.HasPrefix(f.CloneRoot, "~/") && !filepath.IsAbs(f.CloneRoot) {
				return Fleet{}, fmt.Errorf("%s: must be an absolute path or start with ~/, got %q", p, f.CloneRoot)
			}
			f.CloneRoot = filepath.Clean(expandHome(f.CloneRoot))
		case "env":
			if f.Env, err = envMap(p, m.raw); err != nil {
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
