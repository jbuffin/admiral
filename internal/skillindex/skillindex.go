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

func Installed(harness string, environ []string, dir string) []string {
	user, project := locations(harness, environ, dir)
	set := map[string]bool{}
	for _, n := range append(user, project...) {
		set[n] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// The user's own locations come before the project's, so a cloned repository
// cannot shadow the Commodore skill (ADR 0010).
func FindCommodore(harness string, environ []string, dir string) (string, bool) {
	user, project := locations(harness, environ, dir)
	if n, ok := pickCommodore(user); ok {
		return n, true
	}
	if harness == "claude" {
		// A project that turns off the user's Commodore plugin does not get to
		// put its own in its place.
		env := envMap(environ)
		if _, ok := pickCommodore(claudePlugins(env, config.IdentityDir(harness, env), dir, false)); ok {
			return "", false
		}
	}
	return pickCommodore(project)
}

// CommodoreDir is the directory of the Commodore skill FindCommodore picks, the one holding its SKILL.md.
func CommodoreDir(harness string, environ []string, dir string) (string, bool) {
	return "", false
}

func pickCommodore(names []string) (string, bool) {
	best := ""
	for _, n := range names {
		plugin, skill, namespaced := strings.Cut(n, ":")
		switch {
		case n == "commodore":
			return n, true
		case namespaced && skill == "commodore" && (best == "" || plugin < strings.TrimSuffix(best, ":commodore")):
			best = n
		}
	}
	return best, best != ""
}

func locations(harness string, environ []string, dir string) (user, project []string) {
	env := envMap(environ)
	home := env["HOME"]
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	identity := config.IdentityDir(harness, env)
	switch harness {
	case "claude":
		user = append(scanDir(filepath.Join(identity, "skills"), true), claudePlugins(env, identity, dir, true)...)
		project = scanDir(filepath.Join(dir, ".claude", "skills"), true)
	case "codex":
		user = append(scanDir(filepath.Join(home, ".agents", "skills"), false), scanDir(filepath.Join(identity, "skills"), false)...)
		project = scanDir(filepath.Join(dir, ".agents", "skills"), false)
	case "copilot":
		user = append(scanDir(filepath.Join(identity, "skills"), false), scanDir(filepath.Join(home, ".agents", "skills"), false)...)
		for _, d := range []string{".github", ".agents", ".claude"} {
			project = append(project, scanDir(filepath.Join(dir, d, "skills"), false)...)
		}
	}
	return user, project
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
			if validName(e.Name()) {
				out = append(out, e.Name())
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
	if !validName(name) {
		return nil
	}
	var out []string
	found := func(skill string) {
		if validName(skill) {
			out = append(out, name+":"+skill)
		}
	}
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

const maxDepth = 4

// A skill's own folder is not searched further.
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

// claudePlugins lists the skills of the installed, enabled plugins, as
// https://code.claude.com/docs/en/plugins-reference lays them out. Plugins
// synced from claude.ai are left out: they are not on disk.
func claudePlugins(env map[string]string, identity, dir string, projectSettings bool) []string {
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
	if projectSettings {
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
