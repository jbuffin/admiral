package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// Each refusal names what is wrong, and comes before anything is created.
func TestLaunchRefusals(t *testing.T) {
	work := t.TempDir()
	file := filepath.Join(work, "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "claude") // found when the config loaded, removed since
	claude := func(exe string) []config.Harness {
		return []config.Harness{{Name: "claude", Executable: exe, Found: true}}
	}
	cfg := &config.Config{Fleets: []config.Fleet{
		{Key: "work", Name: "Work", SearchRoots: []string{work}, Harnesses: claude("/bin/sh")},
		{Key: "nosearch", Name: "No Search", Harnesses: claude("/bin/sh")},
		{Key: "vanish", Name: "Vanish", SearchRoots: []string{work}, Harnesses: claude(gone)},
	}}
	l := &Launcher{Config: func() *config.Config { return cfg }, Home: t.TempDir(), Tmux: "tmux"}
	for _, c := range []struct {
		name, fleet, harness, dir string
		status                    int
		msg                       string
	}{
		{"a missing directory", "work", "claude", filepath.Join(work, "gone"), http.StatusBadRequest, "gone"},
		{"a file, not a directory", "work", "claude", file, http.StatusBadRequest, file},
		{"an unknown fleet", "nope", "claude", work, http.StatusNotFound, "nope"},
		{"a harness the fleet has no dock for", "work", "codex", work, http.StatusBadRequest, "codex"},
		{"a fleet with no search roots", "nosearch", "claude", work, http.StatusBadRequest, "search roots"},
		{"an executable gone since the config loaded", "vanish", "claude", work, http.StatusBadRequest, "not found"},
	} {
		_, err := l.Launch(Request{Fleet: c.fleet, Harness: c.harness, Dir: c.dir, Skill: Skill{Kind: "none"}})
		if le, ok := err.(*Error); !ok || le.Status != c.status || !strings.Contains(le.Msg, c.msg) {
			t.Errorf("%s: Launch = %v, want %d naming %q", c.name, err, c.status, c.msg)
		}
	}
}
