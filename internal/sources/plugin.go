// Package sources runs Admiral's order sources (ADR 0006): subprocess plugins
// that speak JSON-RPC over stdin and stdout, the supervisor that keeps them
// running, and the HTTP and WebSocket surface that shows their orders.
package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
)

// Manifest is a plugin's plugin.json.
type Manifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Icon        string   `json:"icon"`
}

// errNotInstalled is returned for a plugin with no folder.
var errNotInstalled = errors.New("plugin is not installed")

// pluginsDir is $ADMIRAL_HOME/plugins.
func pluginsDir(home string) string { return filepath.Join(home, "plugins") }

// LoadManifest reads and checks plugins/<name>/plugin.json. It returns the
// plugin's folder.
func LoadManifest(home, name string) (Manifest, string, error) {
	var m Manifest
	if !config.NameRe.MatchString(name) {
		return m, "", fmt.Errorf("plugin name %q may hold only letters, digits, - and _", name)
	}
	dir := filepath.Join(pluginsDir(home), name)
	data, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if errors.Is(err, fs.ErrNotExist) {
		if _, serr := os.Stat(dir); serr != nil {
			return m, dir, fmt.Errorf("plugin %s: %w", name, errNotInstalled)
		}
		return m, dir, fmt.Errorf("plugin %s has no plugin.json", name)
	}
	if err != nil {
		return m, dir, err
	}
	if len(data) > 64<<10 {
		return m, dir, fmt.Errorf("plugin.json is over 64 KiB")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, dir, fmt.Errorf("plugin.json is not a valid manifest: %v", err)
	}
	switch {
	case m.Name != name:
		return Manifest{}, dir, fmt.Errorf("plugin.json names the plugin %q, but its folder is %q", m.Name, name)
	case strings.TrimSpace(m.Command) == "":
		return Manifest{}, dir, fmt.Errorf("plugin.json has no command")
	case m.Icon != "" && !validIconName(m.Icon):
		return Manifest{}, dir, fmt.Errorf("plugin.json icon %q must be a .svg or .png file in the plugin folder", m.Icon)
	}
	if m.Args == nil {
		m.Args = []string{}
	}
	return m, dir, nil
}

func validIconName(icon string) bool {
	ext := strings.ToLower(filepath.Ext(icon))
	return icon == filepath.Base(icon) && !strings.HasPrefix(icon, ".") && (ext == ".svg" || ext == ".png")
}

// iconPath is the icon file, or "" when the plugin has none, it is missing, or
// it resolves outside the plugin's folder.
func iconPath(m Manifest, dir string) string {
	if m.Icon == "" || !validIconName(m.Icon) {
		return ""
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	p, err := filepath.EvalSymlinks(filepath.Join(dir, m.Icon))
	if err != nil || !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return ""
	}
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return p
}

// PluginInfo is one installed plugin as GET /api/plugins lists it.
type PluginInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error"`
}

func iconURL(name string) string { return "/api/plugins/" + name + "/icon" }

// ListPlugins reads every folder in plugins/, valid or not.
func ListPlugins(home string) []PluginInfo {
	out := []PluginInfo{}
	entries, _ := os.ReadDir(pluginsDir(home))
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if st, err := os.Stat(filepath.Join(pluginsDir(home), e.Name())); err == nil && st.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		m, dir, err := LoadManifest(home, name)
		if err != nil {
			out = append(out, PluginInfo{Name: name, Error: err.Error()})
			continue
		}
		info := PluginInfo{Name: name, Description: m.Description, Valid: true}
		if iconPath(m, dir) != "" {
			info.IconURL = iconURL(name)
		}
		out = append(out, info)
	}
	return out
}
