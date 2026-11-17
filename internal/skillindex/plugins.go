package skillindex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// codexPlugins lists the skills of the plugins in config.toml, read from the
// highest version directory of Codex's plugin cache. blocking lists the
// turned-off ones too.
func codexPlugins(identity string, blocking bool) []skill {
	data, err := os.ReadFile(filepath.Join(identity, "config.toml"))
	if err != nil {
		return nil
	}
	var cfg struct {
		Plugins map[string]struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	if toml.Unmarshal(data, &cfg) != nil {
		return nil
	}
	keys := make([]string, 0, len(cfg.Plugins))
	for k := range cfg.Plugins {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var out []skill
	for _, key := range keys {
		if e := cfg.Plugins[key].Enabled; e != nil && !*e && !blocking {
			continue
		}
		name, marketplace, ok := strings.Cut(key, "@")
		if !ok || !pathPart(name) || !pathPart(marketplace) {
			continue
		}
		parent := filepath.Join(identity, "plugins", "cache", marketplace, name)
		root, ok := highestVersionDir(parent)
		if !ok || !within(filepath.Join(identity, "plugins", "cache"), root) {
			continue
		}
		out = append(out, pluginAt(root, name, key)...)
	}
	return out
}

// pathPart says s is one plain folder name, so a registry key cannot walk out of the cache.
func pathPart(s string) bool {
	return s != "" && s != "." && !strings.Contains(s, "..") && !strings.ContainsAny(s, `/\`) && !strings.ContainsRune(s, 0)
}

func highestVersionDir(parent string) (string, bool) {
	ents, err := os.ReadDir(parent)
	if err != nil {
		return "", false
	}
	best := ""
	for _, e := range ents {
		if e.IsDir() && isFile(filepath.Join(parent, e.Name(), ".claude-plugin", "plugin.json")) && (best == "" || VersionLess(best, e.Name())) {
			best = e.Name()
		}
	}
	if best == "" {
		return "", false
	}
	return filepath.Join(parent, best), true
}

// VersionLess compares dotted versions numerically, so 3.10.0 is above 3.9.1.
func VersionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, errX := strconv.Atoi(pa[i])
		y, errY := strconv.Atoi(pb[i])
		switch {
		case errX == nil && errY == nil && x != y:
			return x < y
		case (errX != nil || errY != nil) && pa[i] != pb[i]:
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// copilotPlugins lists the skills of the plugins in config.json whose
// cache_path is inside installed-plugins. A plugin is off when its entry or the
// user's settings.json says so; blocking lists those too.
func copilotPlugins(identity string, blocking bool) []skill {
	data, err := os.ReadFile(filepath.Join(identity, "config.json"))
	if err != nil {
		return nil
	}
	var cfg struct {
		InstalledPlugins []struct {
			Name        string `json:"name"`
			Marketplace string `json:"marketplace"`
			CachePath   string `json:"cache_path"`
			Enabled     *bool  `json:"enabled"`
		} `json:"installedPlugins"`
	}
	if json.Unmarshal(dropLeadingComments(data), &cfg) != nil {
		return nil
	}
	enabled := map[string]bool{}
	if s, err := os.ReadFile(filepath.Join(identity, "settings.json")); err == nil {
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(dropLeadingComments(s), &settings) == nil {
			enabled = settings.EnabledPlugins
		}
	}
	base := filepath.Join(identity, "installed-plugins")
	var out []skill
	for _, p := range cfg.InstalledPlugins {
		if p.Name == "" || p.Marketplace == "" || !within(base, p.CachePath) {
			continue
		}
		key := p.Name + "@" + p.Marketplace
		off := (p.Enabled != nil && !*p.Enabled) || (!enabled[key] && hasKey(enabled, key))
		if off && !blocking {
			continue
		}
		out = append(out, pluginAt(p.CachePath, p.Name, key)...)
	}
	return out
}

func hasKey(m map[string]bool, k string) bool {
	_, ok := m[k]
	return ok
}

// Copilot starts config.json with // lines, which are not JSON.
func dropLeadingComments(data []byte) []byte {
	for {
		trimmed := bytes.TrimLeft(data, " \t\r\n")
		if !bytes.HasPrefix(trimmed, []byte("//")) {
			return trimmed
		}
		_, rest, _ := bytes.Cut(trimmed, []byte("\n"))
		data = rest
	}
}

// within says path is below base once symlinks are followed.
func within(base, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realBase, realPath)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// pluginAt reads the plugin whose manifest is under root and tags its skills with the registry key.
func pluginAt(root, fallbackName, key string) []skill {
	skills := pluginSkills(root, fallbackName)
	for i := range skills {
		skills[i].plugin = key
	}
	return skills
}
