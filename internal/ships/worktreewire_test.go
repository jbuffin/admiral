package ships

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAShipWithAWorktreeReportsItOnTheWireAndOneWithoutOmitsIt(t *testing.T) {
	r := newLiveRig(t, false)
	if _, raw, ok := r.wire(t); !ok || raw["worktree"] != nil {
		t.Fatalf("a ship with no worktree: wire = %v (listed %v), want no worktree field", raw, ok)
	}

	made := filepath.Join(r.home, "worktrees", "saltpaw")
	if err := os.MkdirAll(made, 0o755); err != nil {
		t.Fatal(err)
	}
	var got string
	waitUntil(t, "the ship to report its worktree", func() bool {
		_, raw, ok := r.wire(t)
		if !ok || raw["worktree"] == nil {
			return false
		}
		return json.Unmarshal(raw["worktree"], &got) == nil
	})
	real := func(p string) string {
		if e, err := filepath.EvalSymlinks(p); err == nil {
			return e
		}
		return p
	}
	if real(got) != real(made) {
		t.Errorf("worktree = %q, want the ship's worktree %q", got, made)
	}
}
