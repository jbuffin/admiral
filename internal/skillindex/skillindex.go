// Package skillindex lists the skills a harness can invoke from a directory
// (stories 79 and 85), read from disk on every call. It is the one place that
// reads inside a harness's identity directory, and only the skill and plugin
// locations below and the enabledPlugins key of Claude Code's settings files.
package skillindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
)

// Installed is the invocation names of the skills the harness finds in dir,
// sorted, without duplicates. env is the fleet's environment (Harness.Environ).
//
// Locations are the spec's: Claude Code reads <CLAUDE_CONFIG_DIR>/skills, the
// project's .claude/skills and enabled plugins; Codex reads ~/.agents/skills,
// $CODEX_HOME/skills and the project's .agents/skills; Copilot reads
// $COPILOT_HOME/skills, ~/.agents/skills and the project's .github/skills,
// .agents/skills and .claude/skills.
func Installed(harness string, environ []string, dir string) []string {
	env := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	home := env["HOME"]
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	identity := config.IdentityDir(harness, env)
	set := map[string]bool{}
	add := func(names []string) {
		for _, n := range names {
			set[n] = true
		}
	}
	switch harness {
	case "claude":
		add(scanDir(filepath.Join(identity, "skills"), true))
		add(scanDir(filepath.Join(dir, ".claude", "skills"), true))
		add(claudePlugins(env, identity, dir))
	case "codex":
		add(scanDir(filepath.Join(home, ".agents", "skills"), false))
		add(scanDir(filepath.Join(identity, "skills"), false))
		add(scanDir(filepath.Join(dir, ".agents", "skills"), false))
	case "copilot":
		add(scanDir(filepath.Join(identity, "skills"), false))
		add(scanDir(filepath.Join(home, ".agents", "skills"), false))
		for _, d := range []string{".github", ".agents", ".claude"} {
			add(scanDir(filepath.Join(dir, d, "skills"), false))
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Commodore picks the Commodore skill from names: a plain "commodore", else
// <plugin>:commodore, the plugin names in order (ADR 0010).
func Commodore(names []string) (string, bool) {
	best := ""
	for _, n := range names {
		switch {
		case n == "commodore":
			return n, true
		case strings.HasSuffix(n, ":commodore") && (best == "" || n < best):
			best = n
		}
	}
	return best, best != ""
}

// scanDir lists the <name>/SKILL.md folders directly in dir. With plugins, a
// folder holding .claude-plugin/plugin.json is a skills-directory plugin whose
// skills are namespaced with its manifest name.
func scanDir(dir string, plugins bool) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		if !isDir(p) {
			continue
		}
		switch {
		case isFile(filepath.Join(p, "SKILL.md")):
			out = append(out, e.Name())
		case plugins && isFile(filepath.Join(p, ".claude-plugin", "plugin.json")):
			out = append(out, pluginSkills(p, "")...)
		}
	}
	return out
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// manifest is the part of .claude-plugin/plugin.json the index reads.
type manifest struct {
	Name           string          `json:"name"`
	Skills         json.RawMessage `json:"skills"`
	DefaultEnabled *bool           `json:"defaultEnabled"`
}

func readManifest(root string) (manifest, bool) {
	var m manifest
	data, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return m, false
	}
	return m, true
}

// pluginSkills is the namespaced skills of the plugin at root: its skills/
// folder searched all the way down, plus the paths its manifest declares
// ("." is the plugin root holding one SKILL.md). fallbackName names a plugin
// whose manifest has no name.
func pluginSkills(root, fallbackName string) []string {
	m, ok := readManifest(root)
	if !ok {
		return nil
	}
	return namespaced(root, m, fallbackName)
}

func namespaced(root string, m manifest, fallbackName string) []string {
	name := m.Name
	if name == "" {
		name = fallbackName
	}
	if name == "" {
		return nil
	}
	var out []string
	found := func(skill string) { out = append(out, name+":"+skill) }
	for _, s := range findSkills(filepath.Join(root, "skills")) {
		found(s)
	}
	var declared []string
	if json.Unmarshal(m.Skills, &declared) != nil {
		var one string
		if json.Unmarshal(m.Skills, &one) == nil {
			declared = []string{one}
		}
	}
	for _, d := range declared {
		p := filepath.Join(root, filepath.Clean("/"+d)) // never above the plugin
		if filepath.Clean("/"+d) == "/" {
			if isFile(filepath.Join(root, "SKILL.md")) {
				found(name)
			}
			continue
		}
		for _, s := range findSkills(p) {
			found(s)
		}
	}
	return out
}

// maxDepth bounds the search below a plugin's skills folder.
const maxDepth = 4

// findSkills lists the folders under dir, at any depth, that hold a SKILL.md,
// by folder name. A skill's own folder is not searched further.
func findSkills(dir string) []string {
	var out []string
	var walk func(d string, depth int)
	walk = func(d string, depth int) {
		ents, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range ents {
			p := filepath.Join(d, e.Name())
			if !isDir(p) {
				continue
			}
			if isFile(filepath.Join(p, "SKILL.md")) {
				out = append(out, e.Name())
			} else if depth < maxDepth {
				walk(p, depth+1)
			}
		}
	}
	walk(dir, 1)
	return out
}

// claudePlugins lists the skills of the installed, enabled plugins. Claude
// Code's plugin docs (https://code.claude.com/docs/en/plugins-reference): the
// plugins root holds installed_plugins.json, whose entries give each install's
// installPath (possibly outside the cache); a plugin is on when
// enabledPlugins says so, else when its manifest's defaultEnabled is not false.
// Plugins synced from claude.ai are left out: they are not on disk.
func claudePlugins(env map[string]string, identity, dir string) []string {
	root := filepath.Join(identity, "plugins")
	if v := env["CLAUDE_CODE_PLUGIN_CACHE_DIR"]; v != "" {
		root = v
	}
	var installed struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	data, err := os.ReadFile(filepath.Join(root, "installed_plugins.json"))
	if err != nil || json.Unmarshal(data, &installed) != nil {
		return nil
	}
	enabled := enabledPlugins(identity, dir)
	keys := make([]string, 0, len(installed.Plugins))
	for k := range installed.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		for _, in := range installs(installed.Plugins[key]) {
			if in.InstallPath == "" || (in.Scope != "" && in.Scope != "user" && in.ProjectPath != dir) {
				continue
			}
			path := in.InstallPath
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			m, ok := readManifest(path)
			if !ok {
				continue
			}
			on, listed := enabled[key]
			if !listed {
				on = m.DefaultEnabled == nil || *m.DefaultEnabled
			}
			if on {
				name, _, _ := strings.Cut(key, "@")
				out = append(out, namespaced(path, m, name)...)
			}
		}
	}
	return out
}

type install struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
	ProjectPath string `json:"projectPath"`
}

// installs reads an installed_plugins.json value: a list of installs, or one.
func installs(raw json.RawMessage) []install {
	var list []install
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one install
	if json.Unmarshal(raw, &one) == nil {
		return []install{one}
	}
	return nil
}

// enabledPlugins merges the enabledPlugins key of the user's settings.json and
// the project's settings.json and settings.local.json, later files winning.
// Nothing else in those files is decoded: they can hold env and apiKeyHelper.
func enabledPlugins(identity, dir string) map[string]bool {
	out := map[string]bool{}
	for _, f := range []string{
		filepath.Join(identity, "settings.json"),
		filepath.Join(dir, ".claude", "settings.json"),
		filepath.Join(dir, ".claude", "settings.local.json"),
	} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			EnabledPlugins map[string]json.RawMessage `json:"enabledPlugins"`
		}
		if json.Unmarshal(data, &s) != nil {
			continue
		}
		for k, raw := range s.EnabledPlugins {
			var b bool
			if json.Unmarshal(raw, &b) == nil {
				out[k] = b
			}
		}
	}
	return out
}
