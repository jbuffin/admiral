package launcher

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

// A moored ship's record reserves its name, so no launch picks it.
func TestPickNameSkipsAMooredShip(t *testing.T) {
	if testing.Short() {
		t.Skip("pickName asks tmux for its sessions")
	}
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux on PATH")
	}
	home := t.TempDir()
	for i, n := range names {
		if i == 7 {
			continue
		}
		if err := moor.Write(home, moor.Record{Name: n, Session: Slug(n), Fleet: "work", Harness: "claude"}); err != nil {
			t.Fatal(err)
		}
	}
	l := &Launcher{Tmux: bin, Socket: filepath.Join(t.TempDir(), "tmux.sock"), Home: home}
	for range 20 {
		got, err := l.pickName()
		if err != nil {
			t.Fatal(err)
		}
		if got != names[7] {
			t.Fatalf("picked %q, want %q, the only name with no moored record", got, names[7])
		}
	}
}
