package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The default fleet colors, in order: designs/tokens.json fleet.sequence.
var palette = []struct{ color, ink string }{
	{"#3366E0", "#FFFFFF"}, // blue
	{"#F28C28", "#14304A"}, // orange
	{"#7A5AF0", "#FFFFFF"}, // violet
	{"#1F9E8C", "#FFFFFF"}, // teal
	{"#E2447A", "#FFFFFF"}, // rose
}

const (
	inkLight = "#FFFFFF"
	inkDark  = "#14304A"
)

// assignColors gives a fleet without a color the next one in the sequence,
// counting only such fleets, and picks the ink for every fleet.
func assignColors(fleets []Fleet) {
	next := 0
	for i := range fleets {
		f := &fleets[i]
		if f.Color == "" {
			p := palette[next%len(palette)]
			next++
			f.Color, f.Ink = p.color, p.ink
			continue
		}
		f.Ink = inkFor(f.Color)
	}
}

func inkFor(color string) string {
	for _, p := range palette {
		if strings.EqualFold(p.color, color) {
			return p.ink
		}
	}
	l := luminance(color)
	if contrast(l, luminance(inkLight)) >= contrast(l, luminance(inkDark)) {
		return inkLight
	}
	return inkDark
}

// luminance is the WCAG relative luminance of a #RRGGBB color.
func luminance(hex string) float64 {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	lin := func(c uint64) float64 {
		s := float64(c) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(v>>16&0xff) + 0.7152*lin(v>>8&0xff) + 0.0722*lin(v&0xff)
}

func contrast(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

// expandEnvValue expands ~ in an env value; PATH is a list, so each entry.
func expandEnvValue(key, v string) string {
	if key != "PATH" {
		return expandHome(v)
	}
	parts := strings.Split(v, string(os.PathListSeparator))
	for i, p := range parts {
		parts[i] = expandHome(p)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// resolveExecutable finds exe without running it. An absolute path must be an
// executable file; a bare name is looked up on the fleet's PATH if it sets one,
// else on Admiral's.
func resolveExecutable(exe string, env map[string]string) (string, bool) {
	if filepath.IsAbs(exe) {
		return exe, isExecutable(exe)
	}
	if strings.ContainsRune(exe, filepath.Separator) {
		return exe, false
	}
	path, ok := env["PATH"]
	if !ok {
		path = os.Getenv("PATH")
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) { // a relative entry would resolve against whatever the cwd is
			continue
		}
		if p := filepath.Join(dir, exe); isExecutable(p) {
			return p, true
		}
	}
	return exe, false
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

var identityVars = map[string]struct{ env, dir string }{
	"claude":  {"CLAUDE_CONFIG_DIR", "~/.claude"},
	"codex":   {"CODEX_HOME", "~/.codex"},
	"copilot": {"COPILOT_HOME", "~/.copilot"},
}

// IdentityDir is the directory a harness keeps its sign-in in when launched
// with env (a fleet's env): the harness's variable there, else in Admiral's
// own environment, else the harness default; ~ expanded (from env's HOME when
// it has one) and cleaned. The
// setup and fleet commands reuse it to refuse a shared identity.
func IdentityDir(harness string, env map[string]string) string {
	v := identityVars[harness]
	dir, ok := env[v.env]
	if !ok || dir == "" {
		dir = os.Getenv(v.env)
	}
	if dir == "" {
		dir = v.dir
	}
	if h := env["HOME"]; h != "" && (dir == "~" || strings.HasPrefix(dir, "~/")) {
		// A fleet that sets HOME keeps its default directories there.
		return filepath.Clean(filepath.Join(h, dir[1:]))
	}
	return filepath.Clean(expandHome(dir))
}

// markSharedIdentities warns on both docks when two fleets run the same
// harness against the same identity directory. It loads anyway (story 26).
func markSharedIdentities(fleets []Fleet) {
	for i := range fleets {
		for j := range fleets {
			if i == j {
				continue
			}
			for a := range fleets[i].Harnesses {
				ha := &fleets[i].Harnesses[a]
				for _, hb := range fleets[j].Harnesses {
					if ha.Name != hb.Name {
						continue
					}
					if IdentityDir(ha.Name, ha.Env) == IdentityDir(hb.Name, hb.Env) {
						ha.Warnings = append(ha.Warnings,
							fmt.Sprintf("Shares its configuration directory with fleet %s (%s)", fleets[j].Name, fleets[j].Key))
					}
				}
			}
		}
	}
}

// Resolve looks the harness's executable up again. Found is computed when the
// config loads, and the file can vanish after that.
func (h Harness) Resolve() (string, bool) {
	return resolveExecutable(h.Executable, h.Env)
}

// Fleet returns the fleet with this key.
func (c *Config) Fleet(key string) (Fleet, bool) {
	for _, f := range c.Fleets {
		if f.Key == key {
			return f, true
		}
	}
	return Fleet{}, false
}

// Harness returns the fleet's dock for this harness.
func (f Fleet) Harness(name string) (Harness, bool) {
	for _, h := range f.Harnesses {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}
