package skillindex_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/jbuffin/admiral/internal/skillindex"
)

// ids is a fleet's harness environment: its HOME and identity directories,
// all under one temporary root.
type ids struct {
	t                      *testing.T
	root, home             string
	claude, codex, copilot string
	installed              map[string][]map[string]any
	enabled                map[string]bool
}

func newIDs(t *testing.T) *ids {
	t.Helper()
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLAUDE_CODE_PLUGIN_CACHE_DIR"} {
		t.Setenv(k, "")
	}
	r := t.TempDir()
	return &ids{t: t, root: r, home: filepath.Join(r, "home"), claude: filepath.Join(r, "claude"),
		codex: filepath.Join(r, "codex"), copilot: filepath.Join(r, "copilot"),
		installed: map[string][]map[string]any{}, enabled: map[string]bool{}}
}

func (d *ids) environ() []string {
	return []string{"HOME=" + d.home, "CLAUDE_CONFIG_DIR=" + d.claude, "CODEX_HOME=" + d.codex, "COPILOT_HOME=" + d.copilot}
}

func (d *ids) project(name string) string {
	p := filepath.Join(d.root, "work", name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		d.t.Fatal(err)
	}
	return p
}

func writeSkill(t *testing.T, base, name string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// plugin installs a Claude Code plugin keyed <name>@<marketplace> at path,
// with skills (nested folders allowed) under its skills/ folder.
func (d *ids) plugin(key, name, path string, extra map[string]any, skills ...string) {
	d.t.Helper()
	m := map[string]any{"name": name}
	for k, v := range extra {
		m[k] = v
	}
	writeJSON(d.t, filepath.Join(path, ".claude-plugin", "plugin.json"), m)
	for _, s := range skills {
		writeSkill(d.t, filepath.Join(path, "skills"), s)
	}
	d.installed[key] = append(d.installed[key], map[string]any{"scope": "user", "installPath": path})
	d.flush()
}

func (d *ids) enable(key string, on bool) {
	d.enabled[key] = on
	d.flush()
}

func (d *ids) flush() {
	writeJSON(d.t, filepath.Join(d.claude, "plugins", "installed_plugins.json"), map[string]any{"version": 2, "plugins": d.installed})
	writeJSON(d.t, filepath.Join(d.claude, "settings.json"), map[string]any{"enabledPlugins": d.enabled})
}

func sameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	g, w := append([]string{}, got...), append([]string{}, want...)
	sort.Strings(g)
	sort.Strings(w)
	if !reflect.DeepEqual(g, w) && !(len(g) == 0 && len(w) == 0) {
		t.Errorf("%s = %v, want %v", what, g, w)
	}
}

func TestEachHarnessReadsItsOwnLocations(t *testing.T) {
	d := newIDs(t)
	p1, p2 := d.project("p1"), d.project("p2")
	writeSkill(t, filepath.Join(d.claude, "skills"), "u-claude")
	writeSkill(t, filepath.Join(d.claude, "skills"), "dup")
	writeSkill(t, filepath.Join(p1, ".claude", "skills"), "p-claude")
	writeSkill(t, filepath.Join(p1, ".claude", "skills"), "dup")
	writeSkill(t, filepath.Join(p1, ".agents", "skills"), "p-agents")
	writeSkill(t, filepath.Join(p1, ".github", "skills"), "p-github")
	writeSkill(t, filepath.Join(d.home, ".agents", "skills"), "h-agents")
	writeSkill(t, filepath.Join(d.codex, "skills"), "u-codex")
	writeSkill(t, filepath.Join(d.copilot, "skills"), "u-copilot")
	// Defaults that the fleet's identity variables replace.
	writeSkill(t, filepath.Join(d.home, ".claude", "skills"), "h-claude")
	writeSkill(t, filepath.Join(d.home, ".copilot", "skills"), "h-copilot")
	if err := os.MkdirAll(filepath.Join(d.claude, "skills", "no-skill-file"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		harness, dir string
		want         []string
	}{
		{"claude", p1, []string{"u-claude", "p-claude", "dup"}},
		{"claude", p2, []string{"u-claude", "dup"}},
		{"codex", p1, []string{"h-agents", "u-codex", "p-agents"}},
		{"copilot", p1, []string{"u-copilot", "h-agents", "p-github", "p-agents", "p-claude", "dup"}},
	} {
		sameSet(t, c.harness+" in "+filepath.Base(c.dir), skillindex.Installed(c.harness, d.environ(), c.dir), c.want)
	}
}

func TestDefaultLocationsComeFromTheFleetHome(t *testing.T) {
	d := newIDs(t)
	t.Setenv("HOME", t.TempDir())
	writeSkill(t, filepath.Join(os.Getenv("HOME"), ".claude", "skills"), "admiral-claude")
	writeSkill(t, filepath.Join(d.home, ".claude", "skills"), "fleet-claude")
	writeSkill(t, filepath.Join(d.home, ".agents", "skills"), "fleet-agents")
	env := []string{"HOME=" + d.home}
	proj := d.project("proj")
	sameSet(t, "claude", skillindex.Installed("claude", env, proj), []string{"fleet-claude"})
	sameSet(t, "codex", skillindex.Installed("codex", env, proj), []string{"fleet-agents"})
}

func TestClaudePlugins(t *testing.T) {
	d := newIDs(t)
	p1, p2 := d.project("p1"), d.project("p2")
	cache := filepath.Join(d.claude, "plugins", "cache", "mk")

	d.plugin("jb@mk", "jb", filepath.Join(cache, "jb"), map[string]any{"skills": []string{"./extra"}}, "engineering/commodore", "alpha")
	writeSkill(t, filepath.Join(cache, "jb", "extra"), "xs")
	d.enable("jb@mk", true)
	d.plugin("off@mk", "off", filepath.Join(cache, "off"), nil, "offskill")
	d.enable("off@mk", false)
	d.plugin("dflt@mk", "dflt", filepath.Join(cache, "dflt"), nil, "ds")        // on by default
	d.plugin("loc@mk", "loc", filepath.Join(d.root, "local-plugin"), nil, "ls") // installed outside the cache
	d.enable("loc@mk", true)
	d.plugin("pd@mk", "pd", filepath.Join(cache, "pd"), nil, "pds")
	d.enable("pd@mk", true)
	writeJSON(t, filepath.Join(p1, ".claude", "settings.json"), map[string]any{"enabledPlugins": map[string]bool{"pd@mk": false}})
	dp := filepath.Join(d.claude, "skills", "dirplug") // a skills-directory plugin
	writeJSON(t, filepath.Join(dp, ".claude-plugin", "plugin.json"), map[string]any{"name": "dp"})
	writeSkill(t, filepath.Join(dp, "skills"), "dps")

	base := []string{"jb:commodore", "jb:alpha", "jb:xs", "dflt:ds", "loc:ls", "dp:dps"}
	sameSet(t, "p1, where the project turns pd off", skillindex.Installed("claude", d.environ(), p1), base)
	sameSet(t, "p2", skillindex.Installed("claude", d.environ(), p2), append(base, "pd:pds"))
	sameSet(t, "codex", skillindex.Installed("codex", d.environ(), p1), nil)
}

func TestFindCommodore(t *testing.T) {
	jb := func(d *ids, skill string) {
		d.plugin("jb@mk", "jb", filepath.Join(d.claude, "plugins", "cache", "mk", "jb"), nil, skill)
		d.enable("jb@mk", true)
	}
	for _, c := range []struct {
		name, harness string
		setup         func(d *ids, proj string)
		want          string // "" for not found
	}{
		{"absent", "claude", func(*ids, string) {}, ""},
		{"a claude plugin", "claude", func(d *ids, _ string) { jb(d, "engineering/commodore") }, "jb:commodore"},
		{"codex plain", "codex", func(d *ids, _ string) { writeSkill(t, filepath.Join(d.codex, "skills"), "commodore") }, "commodore"},
		{"plain beats a plugin", "claude", func(d *ids, _ string) {
			writeSkill(t, filepath.Join(d.claude, "skills"), "commodore")
			jb(d, "commodore")
		}, "commodore"},
		{"a user plugin beats the project's plain one", "claude", func(d *ids, proj string) {
			writeSkill(t, filepath.Join(proj, ".claude", "skills"), "commodore")
			jb(d, "commodore")
		}, "jb:commodore"},
		{"the project's own when the user has none", "claude", func(d *ids, proj string) {
			writeSkill(t, filepath.Join(proj, ".claude", "skills"), "commodore")
		}, "commodore"},
		{"a project that turns off the user's plugin gets none", "claude", func(d *ids, proj string) {
			writeSkill(t, filepath.Join(proj, ".claude", "skills"), "commodore")
			jb(d, "commodore")
			writeJSON(t, filepath.Join(proj, ".claude", "settings.json"), map[string]any{"enabledPlugins": map[string]bool{"jb@mk": false}})
		}, ""},
		{"a plugin name with a newline is never used", "claude", func(d *ids, proj string) {
			bad := filepath.Join(proj, ".claude", "skills", "x")
			writeJSON(t, filepath.Join(bad, ".claude-plugin", "plugin.json"), map[string]any{"name": "evil\nname"})
			writeSkill(t, filepath.Join(bad, "skills"), "commodore")
		}, ""},
		{"plugins by plugin name", "claude", func(d *ids, _ string) {
			for _, n := range []string{"zzz", "aaa"} {
				d.plugin(n+"@mk", n, filepath.Join(d.claude, "plugins", "cache", "mk", n), nil, "commodore")
				d.enable(n+"@mk", true)
			}
		}, "aaa:commodore"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := newIDs(t)
			proj := d.project("proj")
			c.setup(d, proj)
			got, found := skillindex.FindCommodore(c.harness, d.environ(), proj)
			if found != (c.want != "") || got != c.want {
				t.Errorf("FindCommodore = %q, %v; want %q", got, found, c.want)
			}
		})
	}
}
