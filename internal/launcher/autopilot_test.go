package launcher

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// An autopilot launch sets ADMIRAL_LOW_STAKES, so a caller can't leave it empty
// or make it something else. The refusal comes before anything is created.
func TestLaunchRefusesAnAutopilotRequestWithoutALowStakesSetting(t *testing.T) {
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{t.TempDir()},
		Harnesses: []config.Harness{{Name: "claude", Executable: "claude"}}}}}
	l := &Launcher{Config: func() *config.Config { return cfg }, Home: t.TempDir(), Tmux: "tmux"}
	for _, low := range []string{"", "always", "Advisor"} {
		_, err := l.Launch(Request{Fleet: "work", Harness: "claude", Dir: cfg.Fleets[0].SearchRoots[0],
			Skill: Skill{Kind: "none"}, Text: "x", Autopilot: true, LowStakes: low})
		le, ok := err.(*Error)
		if !ok || le.Status != http.StatusBadRequest || !strings.Contains(le.Msg, "low_stakes") {
			t.Errorf("Launch with LowStakes %q = %v, want a 400 that names low_stakes", low, err)
		}
	}
}
