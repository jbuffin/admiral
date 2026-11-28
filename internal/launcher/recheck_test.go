package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func TestLaunchRefusesADirectoryNotInTheFleetsList(t *testing.T) {
	t.Parallel()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	work := filepath.Join(root, "work")
	mk := func(p string, checkout bool) string {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if checkout {
			if err := os.Mkdir(filepath.Join(p, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	a := mk(filepath.Join(work, "a"), true)
	mk(filepath.Join(a, "sub"), false)
	gone := mk(filepath.Join(work, "gone"), true)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{work},
		Harnesses: []config.Harness{{Name: "claude", Executable: "/bin/sh", Env: map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(root, "id")}}}}}}
	l := &Launcher{Config: func() *config.Config { return cfg }, Home: t.TempDir(), Tmux: "tmux"}

	for name, dir := range map[string]string{
		"a checkout outside the search roots": mk(filepath.Join(root, "elsewhere"), true),
		"a plain folder in a search root":     mk(filepath.Join(work, "plain"), false),
		"a folder inside a checkout":          filepath.Join(a, "sub"),
		"a path that cleans to outside":       filepath.Join(a, "..", "..", "elsewhere"),
		"a listed checkout deleted since":     gone,
	} {
		_, err := l.Launch(Request{Fleet: "work", Harness: "claude", Dir: dir, Skill: Skill{Kind: "none"}})
		if le, ok := err.(*Error); !ok || le.Status != http.StatusBadRequest {
			t.Errorf("%s: Launch = %v, want a 400 refusal", name, err)
		}
	}

	_, err := l.Launch(Request{Fleet: "work", Harness: "claude", Dir: a, Skill: Skill{Kind: "commodore"}, Source: "orders", OrderID: "o1", Text: "go"})
	if le, ok := err.(*Error); !ok || !strings.Contains(le.Msg, "Commodore not found") {
		t.Errorf("a Commodore launch with none installed = %v, want \"Commodore not found\"", err)
	}
}

func TestAFleetWithCommodoreAsAHarnessPluginLaunchesItByTheHarnessMark(t *testing.T) {
	t.Parallel()
	put := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plugin := func(root string) {
		put(filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"jasonbuffin","version":"3.5.0","skills":["./skills/engineering/"]}`)
		put(filepath.Join(root, "skills", "engineering", "commodore", "SKILL.md"), "---\nname: commodore\n---\n")
	}
	for _, c := range []struct {
		harness, idVar, mark string
		install              func(id string)
	}{
		{"codex", "CODEX_HOME", "$", func(id string) {
			plugin(filepath.Join(id, "plugins", "cache", "jbuffinskills", "jasonbuffin", "3.5.0"))
			put(filepath.Join(id, "config.toml"), "[plugins.\"jasonbuffin@jbuffinskills\"]\nenabled = true\n")
		}},
		{"copilot", "COPILOT_HOME", "/", func(id string) {
			cache := filepath.Join(id, "installed-plugins", "jbuffinskills", "jasonbuffin")
			plugin(cache)
			put(filepath.Join(id, "config.json"), "// User settings belong in settings.json.\n// This file is managed automatically.\n"+
				`{"installedPlugins":[{"name":"jasonbuffin","marketplace":"jbuffinskills","version":"3.5.0","cache_path":"`+cache+`","enabled":true}]}`)
		}},
	} {
		t.Run(c.harness, func(t *testing.T) {
			root, _ := filepath.EvalSymlinks(t.TempDir())
			work, id := filepath.Join(root, "work"), filepath.Join(root, "id")
			repo := filepath.Join(work, "a")
			if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			c.install(id)
			cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{work},
				Harnesses: []config.Harness{{Name: c.harness, Executable: "/bin/sh", Env: map[string]string{c.idVar: id, "HOME": filepath.Join(root, "home")}}}}}}

			got, err := CheckSpec(cfg, t.TempDir(), Request{Fleet: "work", Harness: c.harness, Dir: repo, Skill: Skill{Kind: "commodore"}})
			if err != nil {
				t.Fatalf("CheckSpec = %v, want Commodore found in the plugin", err)
			}
			if got.Skill != "jasonbuffin:commodore" {
				t.Fatalf("Checked.Skill = %q, want jasonbuffin:commodore", got.Skill)
			}
			if p, want := promptFor(c.harness, got.Skill, "commodore", "", "go"), c.mark+"jasonbuffin:commodore go"; p != want {
				t.Errorf("prompt = %q, want %q", p, want)
			}
		})
	}
}
