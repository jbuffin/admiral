// Package skillindex is the one place that reads inside a harness's identity
// directory, and only its skill and plugin locations.
package skillindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/jbuffin/admiral/internal/config"
)

// skill is an invocation name and the folder holding its SKILL.md.
type skill struct{ name, dir string }

func Installed(harness string, environ []string, dir string) []string {
	user, project := locations(harness, environ, dir)
	set := map[string]bool{}
	for _, sk := range append(user, project...) {
		set[sk.name] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Skill is an installed skill as a person picks it: the name typed after the
// harness's Mark, and the description from its SKILL.md.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// List is Installed with each skill's description, read from the copy the
// harness would load: the user's own before the project's.
func List(harness string, environ []string, dir string) []Skill {
	user, project := locations(harness, environ, dir)
	seen := map[string]bool{}
	var out []Skill
	for _, sk := range append(user, project...) {
		if seen[sk.name] {
			continue
		}
		seen[sk.name] = true
		out = append(out, Skill{sk.name, describe(filepath.Join(sk.dir, "SKILL.md"))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Mark is what a harness types before a skill's name: $ for Codex, / for Claude Code and Copilot.
func Mark(harness string) string {
	if harness == "codex" {
		return "$"
	}
	return "/"
}

// The user's own locations come before the project's, so a cloned repository
// cannot shadow the Commodore skill (ADR 0010).
func FindCommodore(harness string, environ []string, dir string) (string, bool) {
	sk, ok := findCommodore(harness, environ, dir)
	return sk.name, ok
}

// CommodoreDir is the directory of the Commodore skill FindCommodore picks, the one holding its SKILL.md.
func CommodoreDir(harness string, environ []string, dir string) (string, bool) {
	sk, ok := findCommodore(harness, environ, dir)
	return sk.dir, ok
}

func findCommodore(harness string, environ []string, dir string) (skill, bool) {
	user, project := locations(harness, environ, dir)
	if sk, ok := pickCommodore(user); ok {
		return sk, true
	}
	if harness == "claude" {
		// A project that turns off the user's Commodore plugin does not get to
		// put its own in its place.
		env := envMap(environ)
		if _, ok := pickCommodore(claudePlugins(env, config.IdentityDir(harness, env), dir, false)); ok {
			return skill{}, false
		}
	}
	return pickCommodore(project)
}

func pickCommodore(skills []skill) (skill, bool) {
	var best skill
	for _, sk := range skills {
		plugin, name, namespaced := strings.Cut(sk.name, ":")
		switch {
		case sk.name == "commodore":
			return sk, true
		case namespaced && name == "commodore" && (best.name == "" || plugin < strings.TrimSuffix(best.name, ":commodore")):
			best = sk
		}
	}
	return best, best.name != ""
}

func locations(harness string, environ []string, dir string) (user, project []skill) {
	env := envMap(environ)
	home := env["HOME"]
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	identity := config.IdentityDir(harness, env)
	switch harness {
	case "claude":
		user = append(scanDir(filepath.Join(identity, "skills"), true), claudePlugins(env, identity, dir, true)...)
		project = projectSkills(dir, ".claude", true)
	case "codex":
		user = append(scanDir(filepath.Join(home, ".agents", "skills"), false), scanDir(filepath.Join(identity, "skills"), false)...)
		project = projectSkills(dir, ".agents", false)
	case "copilot":
		user = append(scanDir(filepath.Join(identity, "skills"), false), scanDir(filepath.Join(home, ".agents", "skills"), false)...)
		for _, d := range []string{".github", ".agents", ".claude"} {
			project = append(project, projectSkills(dir, d, false)...)
		}
	}
	return user, project
}

// projectSkills reads nothing for an empty dir, which would otherwise mean the working directory.
func projectSkills(dir, parent string, plugins bool) []skill {
	if dir == "" {
		return nil
	}
	return scanDir(filepath.Join(dir, parent, "skills"), plugins)
}

func envMap(environ []string) map[string]string {
	env := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

// SafeInvocation says name is safe to type after / or $ and to keep in a tmux option.
func SafeInvocation(name string) bool {
	plugin, skill, namespaced := strings.Cut(name, ":")
	if !namespaced {
		return validName(name)
	}
	return validName(plugin) && validName(skill)
}

// No colon: plugin names are kebab-case.
func validName(n string) bool {
	return n != "" && strings.IndexFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f || unicode.IsSpace(r) || r == ':' }) < 0
}

// A folder holding .claude-plugin/plugin.json is a skills-directory plugin.
func scanDir(dir string, plugins bool) []skill {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []skill
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		if !isDir(p) {
			continue
		}
		switch {
		case isFile(filepath.Join(p, "SKILL.md")):
			if validName(e.Name()) {
				out = append(out, skill{e.Name(), p})
			}
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

// "." in a manifest's paths is the plugin root holding one SKILL.md.
func pluginSkills(root, fallbackName string) []skill {
	m, ok := readManifest(root)
	if !ok {
		return nil
	}
	return namespaced(root, m, fallbackName)
}

func namespaced(root string, m manifest, fallbackName string) []skill {
	name := m.Name
	if name == "" {
		name = fallbackName
	}
	if !validName(name) {
		return nil
	}
	var out []skill
	found := func(skillName, dir string) {
		if validName(skillName) {
			out = append(out, skill{name + ":" + skillName, dir})
		}
	}
	for _, s := range findSkills(filepath.Join(root, "skills")) {
		found(s.name, s.dir)
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
				found(name, root)
			}
			continue
		}
		for _, s := range findSkills(p) {
			found(s.name, s.dir)
		}
	}
	return out
}

const maxDepth = 4

// A skill's own folder is not searched further.
func findSkills(dir string) []skill {
	var out []skill
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
				out = append(out, skill{e.Name(), p})
			} else if depth < maxDepth {
				walk(p, depth+1)
			}
		}
	}
	walk(dir, 1)
	return out
}

// claudePlugins lists the skills of the installed, enabled plugins, as
// https://code.claude.com/docs/en/plugins-reference lays them out. Plugins
// synced from claude.ai are left out: they are not on disk.
func claudePlugins(env map[string]string, identity, dir string, projectSettings bool) []skill {
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
	enabled := enabledPlugins(identity, dir, projectSettings)
	keys := make([]string, 0, len(installed.Plugins))
	for k := range installed.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []skill
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

// Nothing else in those files is decoded: they can hold env and apiKeyHelper.
func enabledPlugins(identity, dir string, projectSettings bool) map[string]bool {
	out := map[string]bool{}
	files := []string{filepath.Join(identity, "settings.json")}
	if projectSettings && dir != "" {
		files = append(files, filepath.Join(dir, ".claude", "settings.json"), filepath.Join(dir, ".claude", "settings.local.json"))
	}
	for _, f := range files {
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

// HarnessSkills is one dock's installed skills.
type HarnessSkills struct {
	Harness string
	Skills  []Skill
}

// Entry is one skill across docks, under the name a crew member saves.
type Entry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Harnesses   []string `json:"harnesses"`
}

// Merge is not written yet.
func Merge(lists []HarnessSkills) []Entry { return nil }
