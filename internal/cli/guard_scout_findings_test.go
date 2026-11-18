package cli

import (
	"errors"
	"path/filepath"
	"testing"
)

// A scout writes its findings with its file tool at the path ADMIRAL_FINDINGS names.

func TestAScoutMayWriteItsFindingsFileWithTheFileToolButNotThroughTheShell(t *testing.T) {
	r := newScoutRig(t)
	home := r.out
	findings := filepath.Join(home, "reports", "dory.md")
	t.Setenv("ADMIRAL_HOME", home)
	t.Setenv("ADMIRAL_FINDINGS", findings)
	for _, auto := range []string{"", "1"} {
		if err, out, _ := r.run(t, auto, "Write", map[string]any{"file_path": findings, "content": "# Found\n"}, ""); err != nil || out != "" {
			t.Errorf("autopilot %q: a Write to the findings path = %v, %q, want it allowed", auto, err, out)
		}
		err, _, _ := r.run(t, auto, "Bash", bashInput(`echo found > $ADMIRAL_FINDINGS`), "")
		var denied *GuardDenied
		if !errors.As(err, &denied) || denied.Rule != scoutWrite {
			t.Errorf("autopilot %q: a shell write to $ADMIRAL_FINDINGS = %v, want rule %s", auto, err, scoutWrite)
		}
	}
}
