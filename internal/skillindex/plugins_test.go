package skillindex_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/skillindex"
)

const (
	jbKey    = "jasonbuffin@jbuffinskills"
	jbCommod = "jasonbuffin:commodore"
)

// writePlugin lays out a plugin repo with the manifest at .claude-plugin/plugin.json and
// the skills under skills/engineering/, which the manifest's `skills` names.
func writePlugin(t *testing.T, root, version string, extra map[string]any) {
	t.Helper()
	m := map[string]any{"name": "jasonbuffin", "version": version, "skills": []string{"./skills/engineering/"}}
	for k, v := range extra {
		m[k] = v
	}
	writeJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"), m)
	dir := filepath.Join(root, "skills", "engineering", "commodore")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: commodore\ndescription: Runs the crew.\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeText(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// codexConfig writes config.toml the way `codex plugin add` leaves it, then the given plugin tables.
func (d *ids) codexConfig(tables ...string) {
	d.t.Helper()
	s := "[marketplaces.jbuffinskills]\nsource_type = \"git\"\nsource = \"https://github.com/jbuffin/skills.git\"\n"
	for _, tb := range tables {
		s += "\n" + tb + "\n"
	}
	writeText(d.t, filepath.Join(d.codex, "config.toml"), s)
}

func codexTable(key string, enabled bool) string {
	return fmt.Sprintf("[plugins.%q]\nenabled = %t", key, enabled)
}

func (d *ids) codexCache(marketplace, plugin, version string) string {
	return filepath.Join(d.codex, "plugins", "cache", marketplace, plugin, version)
}

// copilotConfig writes config.json with the two leading // lines the CLI writes.
func (d *ids) copilotConfig(entries ...map[string]any) {
	d.t.Helper()
	b, _ := json.Marshal(map[string]any{"installedPlugins": entries})
	writeText(d.t, filepath.Join(d.copilot, "config.json"),
		"// User settings belong in settings.json.\n// This file is managed automatically.\n"+string(b))
}

func (d *ids) copilotPath() string {
	return filepath.Join(d.copilot, "installed-plugins", "jbuffinskills", "jasonbuffin")
}

func (d *ids) copilotEntry(path string, enabled bool) map[string]any {
	return map[string]any{"name": "jasonbuffin", "marketplace": "jbuffinskills", "version": "3.5.0", "cache_path": path, "enabled": enabled}
}

func (d *ids) copilotSettings(enabledPlugins map[string]bool) {
	d.t.Helper()
	writeJSON(d.t, filepath.Join(d.copilot, "settings.json"), map[string]any{
		"extraKnownMarketplaces": map[string]any{"jbuffinskills": map[string]any{"source": map[string]any{"source": "github", "repo": "jbuffin/skills"}}},
		"enabledPlugins":         enabledPlugins,
	})
}

func lookup(t *testing.T, harness string, d *ids, dir string) skillindex.Commodore {
	t.Helper()
	c, ok := skillindex.LookupCommodore(harness, d.environ(), dir)
	if !ok {
		t.Fatalf("LookupCommodore(%s) found nothing", harness)
	}
	return c
}

func TestCodexPluginsAreUserLevelSkills(t *testing.T) {
	d := newIDs(t)
	writePlugin(t, d.codexCache("jbuffinskills", "jasonbuffin", "3.5.0"), "3.5.0", nil)
	d.codexConfig(codexTable(jbKey, true))
	proj := d.project("proj")

	sameSet(t, "installed with a project", skillindex.Installed("codex", d.environ(), proj), []string{jbCommod})
	sameSet(t, "installed with no checkout", skillindex.Installed("codex", d.environ(), ""), []string{jbCommod})
	got := lookup(t, "codex", d, "")
	want := skillindex.Commodore{Name: jbCommod, Dir: filepath.Join(d.codexCache("jbuffinskills", "jasonbuffin", "3.5.0"), "skills", "engineering", "commodore"),
		Plugin: jbKey, Version: "3.5.0"}
	if got != want {
		t.Errorf("LookupCommodore = %+v, want %+v", got, want)
	}
}

func TestCodexTakesTheHighestVersionDirectory(t *testing.T) {
	d := newIDs(t)
	for _, v := range []string{"3.9.1", "3.10.0", "3.5.0"} {
		writePlugin(t, d.codexCache("jbuffinskills", "jasonbuffin", v), v, nil)
	}
	d.codexConfig(codexTable(jbKey, true))
	got := lookup(t, "codex", d, "")
	if got.Version != "3.10.0" || filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(got.Dir)))) != "3.10.0" {
		t.Errorf("LookupCommodore = %+v, want version 3.10.0 from the 3.10.0 directory", got)
	}
}

func TestCodexSkipsAVersionDirectoryThatHoldsNoPlugin(t *testing.T) {
	d := newIDs(t)
	writePlugin(t, d.codexCache("jbuffinskills", "jasonbuffin", "3.5.0"), "3.5.0", nil)
	if err := os.MkdirAll(d.codexCache("jbuffinskills", "jasonbuffin", "3.6.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.codexConfig(codexTable(jbKey, true))
	if got := lookup(t, "codex", d, ""); got.Version != "3.5.0" {
		t.Errorf("LookupCommodore = %+v, want version 3.5.0", got)
	}

	d.codexConfig(codexTable(jbKey, false))
	proj := d.project("proj")
	writeSkill(t, filepath.Join(proj, ".agents", "skills"), "commodore")
	if c, ok := skillindex.LookupCommodore("codex", d.environ(), proj); ok {
		t.Errorf("LookupCommodore = %+v, want the disabled plugin to block the project's Commodore", c)
	}
}

func TestCodexPluginTurnedOffOrNotListedIsNotRead(t *testing.T) {
	for name, tables := range map[string][]string{
		"enabled = false by a hand edit": {codexTable(jbKey, false)},
		"removed from config.toml":       nil,
	} {
		t.Run(name, func(t *testing.T) {
			d := newIDs(t)
			writePlugin(t, d.codexCache("jbuffinskills", "jasonbuffin", "3.5.0"), "3.5.0", nil)
			d.codexConfig(tables...)
			sameSet(t, "installed", skillindex.Installed("codex", d.environ(), ""), nil)
			if c, ok := skillindex.LookupCommodore("codex", d.environ(), ""); ok {
				t.Errorf("LookupCommodore = %+v, want not found", c)
			}
		})
	}
}

func TestCodexConfigThatIsNotTOMLYieldsNoPlugins(t *testing.T) {
	d := newIDs(t)
	writePlugin(t, d.codexCache("jbuffinskills", "jasonbuffin", "3.5.0"), "3.5.0", nil)
	writeText(t, filepath.Join(d.codex, "config.toml"), "[plugins.\"jasonbuffin@jbuffinskills\"\nenabled = = true\n\x00")
	sameSet(t, "installed", skillindex.Installed("codex", d.environ(), ""), nil)
}

func TestCodexNamesThatEscapeTheCacheAreNotRead(t *testing.T) {
	d := newIDs(t)
	// Where each key would land if its parts were joined unchecked.
	writePlugin(t, filepath.Join(d.codex, "plugins", "stolen", "jasonbuffin", "3.5.0"), "3.5.0", nil)
	writePlugin(t, filepath.Join(d.codex, "plugins", "cache", "up", "3.5.0"), "3.5.0", nil)
	writePlugin(t, filepath.Join(d.codex, "plugins", "cache", "x", "y", "jasonbuffin", "3.5.0"), "3.5.0", nil)
	// A sound plugin beside them proves the file is read at all.
	writePlugin(t, d.codexCache("jbuffinskills", "okplug", "1.0.0"), "1.0.0", map[string]any{"name": "okplug"})
	d.codexConfig(
		codexTable("jasonbuffin@../stolen", true),
		codexTable("../up@jbuffinskills", true),
		codexTable("jasonbuffin@x/y", true),
		codexTable("okplug@jbuffinskills", true),
	)
	sameSet(t, "installed", skillindex.Installed("codex", d.environ(), ""), []string{"okplug:commodore"})
}

func TestCopilotPluginsAreUserLevelSkills(t *testing.T) {
	d := newIDs(t)
	writePlugin(t, d.copilotPath(), "3.5.0", nil)
	d.copilotConfig(d.copilotEntry(d.copilotPath(), true))
	d.copilotSettings(map[string]bool{jbKey: true})
	proj := d.project("proj")

	sameSet(t, "installed with a project", skillindex.Installed("copilot", d.environ(), proj), []string{jbCommod})
	sameSet(t, "installed with no checkout", skillindex.Installed("copilot", d.environ(), ""), []string{jbCommod})
	got := lookup(t, "copilot", d, "")
	want := skillindex.Commodore{Name: jbCommod, Dir: filepath.Join(d.copilotPath(), "skills", "engineering", "commodore"),
		Plugin: jbKey, Version: "3.5.0"}
	if got != want {
		t.Errorf("LookupCommodore = %+v, want %+v", got, want)
	}
}

func TestCopilotPluginWithNoSettingsEntryCountsAsEnabled(t *testing.T) {
	d := newIDs(t)
	writePlugin(t, d.copilotPath(), "3.5.0", nil)
	d.copilotConfig(d.copilotEntry(d.copilotPath(), true))
	sameSet(t, "installed", skillindex.Installed("copilot", d.environ(), ""), []string{jbCommod})
}

func TestCopilotPluginDisabledByEitherFileIsNotRead(t *testing.T) {
	for _, c := range []struct {
		name         string
		entryEnabled bool
		settings     map[string]bool
	}{
		{"the entry's enabled is false", false, map[string]bool{jbKey: true}},
		{"settings enabledPlugins is false", true, map[string]bool{jbKey: false}},
		{"both, as the CLI's disable writes them", false, map[string]bool{jbKey: false}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := newIDs(t)
			writePlugin(t, d.copilotPath(), "3.5.0", nil)
			d.copilotConfig(d.copilotEntry(d.copilotPath(), c.entryEnabled))
			d.copilotSettings(c.settings)
			sameSet(t, "installed", skillindex.Installed("copilot", d.environ(), ""), nil)
		})
	}
}

func TestCopilotConfigThatIsNotJSONYieldsNoPlugins(t *testing.T) {
	for name, body := range map[string]string{
		"comments only":      "// User settings belong in settings.json.\n// This file is managed automatically.\n",
		"truncated JSON":     "// c1\n// c2\n{\"installedPlugins\":[{\"name\":",
		"entries not a list": "// c1\n// c2\n{\"installedPlugins\":{\"name\":\"jasonbuffin\"}}",
		"empty":              "",
	} {
		t.Run(name, func(t *testing.T) {
			d := newIDs(t)
			writePlugin(t, d.copilotPath(), "3.5.0", nil)
			writeText(t, filepath.Join(d.copilot, "config.json"), body)
			sameSet(t, "installed", skillindex.Installed("copilot", d.environ(), ""), nil)
		})
	}
}

func TestCopilotCachePathOutsideInstalledPluginsIsNotRead(t *testing.T) {
	d := newIDs(t)
	for _, p := range []string{
		filepath.Join(d.root, "elsewhere"),
		filepath.Join(d.copilot, "outside"),
		filepath.Join(d.copilot, "installed-plugins-evil", "jasonbuffin"),
	} {
		writePlugin(t, p, "3.5.0", nil)
	}
	// A sound plugin beside them proves the file is read at all.
	okPath := filepath.Join(d.copilot, "installed-plugins", "jbuffinskills", "okplug")
	writePlugin(t, okPath, "1.0.0", map[string]any{"name": "okplug"})
	ok := map[string]any{"name": "okplug", "marketplace": "jbuffinskills", "version": "1.0.0", "cache_path": okPath, "enabled": true}
	d.copilotConfig(
		d.copilotEntry(filepath.Join(d.root, "elsewhere"), true),
		d.copilotEntry(filepath.Join(d.copilot, "installed-plugins", "..", "outside"), true),
		d.copilotEntry(filepath.Join(d.copilot, "installed-plugins-evil", "jasonbuffin"), true),
		ok,
	)
	sameSet(t, "installed", skillindex.Installed("copilot", d.environ(), ""), []string{"okplug:commodore"})
}

func TestPluginManifestSkillsAreHonoredOnCodexAndCopilot(t *testing.T) {
	custom := map[string]any{"skills": []string{"./extra"}}
	setup := map[string]func(d *ids){
		"codex": func(d *ids) {
			root := d.codexCache("jbuffinskills", "jasonbuffin", "3.5.0")
			writePlugin(t, root, "3.5.0", custom)
			writeSkill(t, filepath.Join(root, "extra"), "xs")
			d.codexConfig(codexTable(jbKey, true))
		},
		"copilot": func(d *ids) {
			writePlugin(t, d.copilotPath(), "3.5.0", custom)
			writeSkill(t, filepath.Join(d.copilotPath(), "extra"), "xs")
			d.copilotConfig(d.copilotEntry(d.copilotPath(), true))
		},
	}
	for harness, fn := range setup {
		t.Run(harness, func(t *testing.T) {
			d := newIDs(t)
			fn(d)
			sameSet(t, "installed", skillindex.Installed(harness, d.environ(), ""), []string{"jasonbuffin:xs", jbCommod})
		})
	}
}

func TestListShowsPluginSkillsOnCodexAndCopilot(t *testing.T) {
	setup := map[string]func(d *ids){
		"codex": func(d *ids) {
			writePlugin(t, d.codexCache("jbuffinskills", "jasonbuffin", "3.5.0"), "3.5.0", nil)
			d.codexConfig(codexTable(jbKey, true))
		},
		"copilot": func(d *ids) {
			writePlugin(t, d.copilotPath(), "3.5.0", nil)
			d.copilotConfig(d.copilotEntry(d.copilotPath(), true))
		},
	}
	for harness, fn := range setup {
		t.Run(harness, func(t *testing.T) {
			d := newIDs(t)
			fn(d)
			want := []skillindex.Skill{{Name: jbCommod, Description: "Runs the crew."}}
			if got := skillindex.List(harness, d.environ(), ""); len(got) != 1 || got[0] != want[0] {
				t.Errorf("List = %+v, want %+v", got, want)
			}
		})
	}
}

// Every harness: a user's Commodore plugin that is installed but disabled keeps a project's own Commodore out.
func TestADisabledUserCommodorePluginBlocksAProjectCommodore(t *testing.T) {
	type c struct {
		name, harness, projectSkills string
		setup                        func(d *ids)
	}
	claude := func(d *ids) {
		d.plugin(jbKey, "jasonbuffin", filepath.Join(d.claude, "plugins", "cache", "jbuffinskills", "jasonbuffin", "3.5.0"), nil, "commodore")
		d.enable(jbKey, false)
	}
	for _, tc := range []c{
		{"claude user settings", "claude", ".claude", claude},
		{"codex enabled = false", "codex", ".agents", func(d *ids) {
			writePlugin(t, d.codexCache("jbuffinskills", "jasonbuffin", "3.5.0"), "3.5.0", nil)
			d.codexConfig(codexTable(jbKey, false))
		}},
		{"copilot entry enabled false", "copilot", ".github", func(d *ids) {
			writePlugin(t, d.copilotPath(), "3.5.0", nil)
			d.copilotConfig(d.copilotEntry(d.copilotPath(), false))
			d.copilotSettings(map[string]bool{jbKey: true})
		}},
		{"copilot user enabledPlugins false", "copilot", ".github", func(d *ids) {
			writePlugin(t, d.copilotPath(), "3.5.0", nil)
			d.copilotConfig(d.copilotEntry(d.copilotPath(), true))
			d.copilotSettings(map[string]bool{jbKey: false})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newIDs(t)
			proj := d.project("proj")
			writeSkill(t, filepath.Join(proj, tc.projectSkills, "skills"), "commodore")
			tc.setup(d)
			if name, ok := skillindex.FindCommodore(tc.harness, d.environ(), proj); ok {
				t.Errorf("FindCommodore = %q, true; want not found", name)
			}
			if dir, ok := skillindex.CommodoreDir(tc.harness, d.environ(), proj); ok {
				t.Errorf("CommodoreDir = %q, true; want not found", dir)
			}
			if got, ok := skillindex.LookupCommodore(tc.harness, d.environ(), proj); ok {
				t.Errorf("LookupCommodore = %+v, true; want not found", got)
			}
		})
	}
}

func TestAProjectCommodoreStillLaunchesWhenTheUserHasNoCommodorePlugin(t *testing.T) {
	for harness, parent := range map[string]string{"claude": ".claude", "codex": ".agents", "copilot": ".github"} {
		t.Run(harness, func(t *testing.T) {
			d := newIDs(t)
			proj := d.project("proj")
			writeSkill(t, filepath.Join(proj, parent, "skills"), "commodore")
			got := lookup(t, harness, d, proj)
			if got.Name != "commodore" || got.Plugin != "" || got.Version != "" {
				t.Errorf("LookupCommodore = %+v, want the loose project skill with no plugin or version", got)
			}
		})
	}
}

func TestLookupCommodoreReportsThePluginAndItsVersion(t *testing.T) {
	t.Run("a loose skill has neither", func(t *testing.T) {
		d := newIDs(t)
		writeSkill(t, filepath.Join(d.claude, "skills"), "commodore")
		got := lookup(t, "claude", d, "")
		if want := (skillindex.Commodore{Name: "commodore", Dir: filepath.Join(d.claude, "skills", "commodore")}); got != want {
			t.Errorf("LookupCommodore = %+v, want %+v", got, want)
		}
	})
	t.Run("a claude plugin", func(t *testing.T) {
		d := newIDs(t)
		root := filepath.Join(d.claude, "plugins", "cache", "jbuffinskills", "jasonbuffin", "3.5.0")
		writePlugin(t, root, "3.5.0", nil)
		d.installed[jbKey] = append(d.installed[jbKey], map[string]any{"scope": "user", "installPath": root, "version": "3.5.0"})
		d.enable(jbKey, true)
		got := lookup(t, "claude", d, "")
		if got.Name != jbCommod || got.Plugin != jbKey || got.Version != "3.5.0" {
			t.Errorf("LookupCommodore = %+v, want %s from %s at 3.5.0", got, jbCommod, jbKey)
		}
	})
	t.Run("a plugin whose manifest has no version", func(t *testing.T) {
		d := newIDs(t)
		root := filepath.Join(d.claude, "plugins", "cache", "jbuffinskills", "jasonbuffin", "3.5.0")
		writePlugin(t, root, "", nil)
		d.installed[jbKey] = append(d.installed[jbKey], map[string]any{"scope": "user", "installPath": root, "version": "3.5.0"})
		d.enable(jbKey, true)
		got := lookup(t, "claude", d, "")
		if got.Plugin != jbKey || got.Version != "" {
			t.Errorf("LookupCommodore = %+v, want the plugin with an unknown version", got)
		}
	})
	t.Run("a plugin inside a skills folder has a version and no plugin", func(t *testing.T) {
		d := newIDs(t)
		writePlugin(t, filepath.Join(d.claude, "skills", "jasonbuffin"), "3.1.0", nil)
		got := lookup(t, "claude", d, "")
		if got.Name != jbCommod || got.Plugin != "" || got.Version != "3.1.0" {
			t.Errorf("LookupCommodore = %+v, want %s with no plugin at 3.1.0", got, jbCommod)
		}
	})
}

func TestLookupCommodoreWithNoCheckoutIgnoresProjects(t *testing.T) {
	d := newIDs(t)
	writeSkill(t, filepath.Join(d.root, "cwd", ".agents", "skills"), "commodore")
	t.Chdir(filepath.Join(d.root, "cwd"))
	if c, ok := skillindex.LookupCommodore("codex", d.environ(), ""); ok {
		t.Errorf("LookupCommodore = %+v, want not found: no checkout means user level only", c)
	}
}
