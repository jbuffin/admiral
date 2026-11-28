package launcher

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// dropSessionEnv is what a scout's shell can do to its own tmux session: `tmux set-environment -u`.
func (p *pinRig) dropSessionEnv(t *testing.T, session string, names ...string) {
	t.Helper()
	for _, n := range names {
		if out, err := exec.Command(p.l.Tmux, "-S", p.l.Socket, "set-environment", "-t", "="+session+":", "-u", n).CombinedOutput(); err != nil {
			t.Fatalf("set-environment -u %s: %v: %s", n, err, out)
		}
	}
}

// allowGates lets every harness through, for a test about what a boat is given.
func (p *pinRig) allowGates() {
	p.l.ScoutGate = func(config.Fleet, string) error { return nil }
	p.l.AutopilotGate = func(config.Fleet, string) error { return nil }
}

func TestACrewBoatInAScoutSessionKeepsTheScoutEnvAfterTheSessionDropsIt(t *testing.T) {
	t.Parallel()
	p := newPinRig(t, nil)
	scout, err := p.l.Launch(p.scoutRequest(p.work))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	session := p.recorded(t, "claude")["ADMIRAL_SCOUT"]
	p.allowGates()
	p.dropSessionEnv(t, scout.Session, "ADMIRAL_SCOUT", "ADMIRAL_AUTOPILOT")
	// The caller's own environment says nothing: the boat's mode comes from the session's record.
	if err := p.l.LaunchCrew(CrewRequest{Session: scout.Session, Name: "boat", Harness: "claude", Dir: p.work, Prompt: "go"}); err != nil {
		t.Fatalf("LaunchCrew: %v", err)
	}
	got := p.recorded(t, "claude")
	if got["ADMIRAL_SCOUT"] != session || got["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("the boat has ADMIRAL_SCOUT=%q ADMIRAL_AUTOPILOT=%q, want %q and 1 though the session dropped both", got["ADMIRAL_SCOUT"], got["ADMIRAL_AUTOPILOT"], session)
	}
}

func TestACrewLaunchWithNoScoutInTheCallersEnvIntoAScoutSessionStillHitsTheScoutGate(t *testing.T) {
	t.Parallel()
	p := newPinRig(t, nil)
	scout, err := p.l.Launch(p.scoutRequest(p.work))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	p.l.ScoutGate = func(fleet config.Fleet, harness string) error {
		if harness != "claude" {
			return errors.New("a scout cannot run on " + harness)
		}
		return nil
	}
	p.l.AutopilotGate = func(config.Fleet, string) error { return nil }
	err = p.l.LaunchCrew(CrewRequest{Session: scout.Session, Name: "codex-boat", Harness: "codex", Dir: p.work, Prompt: "go"})
	if err == nil || !strings.Contains(err.Error(), "codex") {
		t.Errorf("LaunchCrew = %v, want the scout gate's refusal of codex", err)
	}
	if names, _ := p.l.server().CrewNames(scout.Session); strings.Contains(strings.Join(names, ","), "codex-boat") {
		t.Errorf("the refused boat left a window: %v", names)
	}
}

func TestACrewBoatInAShipThatIsNotAScoutGetsNoScoutEnv(t *testing.T) {
	t.Parallel()
	p := newPinRig(t, nil)
	ship := p.ship(t)
	if err := p.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: "boat", Harness: "claude", Dir: p.work, Prompt: "go"}); err != nil {
		t.Fatalf("LaunchCrew: %v", err)
	}
	if got, set := p.recorded(t, "claude")["ADMIRAL_SCOUT"]; set {
		t.Errorf("a boat of a ship that is not a scout has ADMIRAL_SCOUT = %q", got)
	}
}
