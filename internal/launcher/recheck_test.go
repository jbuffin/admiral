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
