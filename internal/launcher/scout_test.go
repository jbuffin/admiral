package launcher

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func (p *pinRig) scoutRequest(dir string) Request {
	return Request{Fleet: "work", Harness: "claude", Dir: dir, Skill: Skill{Kind: "none"}, Text: "find the flaky test", Autopilot: true, Scout: true}
}

func TestAScoutsEnvironmentNamesItsResolvedCheckoutAndRunsInAutopilot(t *testing.T) {
	p := newPinRig(t, map[string]string{"ADMIRAL_SCOUT": "/fleet/says/otherwise"})
	real := filepath.Join(p.work, "real")
	mustMkdir(t, real)
	link := filepath.Join(p.work, "link")
	mustSymlink(t, real, link)
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	// The checkout check lists only roots and git checkouts, so the link is the fleet's search root.
	p.l.Config().Fleets[0].SearchRoots = []string{link}
	if _, err := p.l.Launch(p.scoutRequest(link)); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	env := p.recorded(t, "claude")
	if env["ADMIRAL_SCOUT"] != want {
		t.Errorf("ADMIRAL_SCOUT = %q, want the symlink-resolved checkout %q", env["ADMIRAL_SCOUT"], want)
	}
	if env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("ADMIRAL_AUTOPILOT = %q, want 1", env["ADMIRAL_AUTOPILOT"])
	}
}

func TestASessionOptionMarksAScoutAndOnlyAScout(t *testing.T) {
	p := newPinRig(t, nil)
	scout, err := p.l.Launch(p.scoutRequest(p.work))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	p.recorded(t, "claude")
	plain := p.ship(t)
	for _, c := range []struct {
		session string
		want    string
	}{{scout.Session, "1"}, {plain.Session, ""}} {
		if got, err := p.l.server().ShipOption(c.session, "scout"); err != nil || got != c.want {
			t.Errorf("@admiral-scout of %s = %q (%v), want %q", c.session, got, err, c.want)
		}
	}
}

func TestAScoutsPromptEndsWithItsContractAndAnotherShipsDoesNot(t *testing.T) {
	p := newPinRig(t, nil)
	if _, err := p.l.Launch(p.scoutRequest(p.work)); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	scoutPrompt := p.lastArg(t)
	if !strings.HasPrefix(scoutPrompt, "find the flaky test") {
		t.Errorf("a scout's prompt = %q, want the engineer's text first", scoutPrompt)
	}
	for _, want := range []string{"## Scout", "changes nothing", "findings", "open question"} {
		if !strings.Contains(scoutPrompt, want) {
			t.Errorf("a scout's prompt = %q, want it to contain %q", scoutPrompt, want)
		}
	}
	if _, err := p.l.Launch(p.justRequest()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := p.lastArg(t); strings.Contains(got, "Scout") {
		t.Errorf("a ship that is not a scout got a scout's contract: %q", got)
	}
}

func TestAScoutsCrewBoatInheritsTheSessionsScoutVariable(t *testing.T) {
	p := newPinRig(t, nil)
	scout, err := p.l.Launch(p.scoutRequest(p.work))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	session := p.recorded(t, "claude")["ADMIRAL_SCOUT"]
	if session == "" {
		t.Fatal("the scout has no ADMIRAL_SCOUT")
	}
	p.allowGates()
	p.l.Config().Fleets[0].Harnesses[1].Env["ADMIRAL_SCOUT"] = "/dock/says/so"
	if err := p.l.LaunchCrew(CrewRequest{Session: scout.Session, Name: "boat", Harness: "codex", Dir: p.work, Prompt: "go"}); err != nil {
		t.Fatalf("LaunchCrew: %v", err)
	}
	if got := p.recorded(t, "codex")["ADMIRAL_SCOUT"]; got != session {
		t.Errorf("a scout's boat has ADMIRAL_SCOUT = %q, want the session's %q", got, session)
	}
}

func TestACrewLaunchFromAScoutNeedsTheScoutGateOfTheBoatsHarness(t *testing.T) {
	p := newPinRig(t, nil)
	ship := p.ship(t)
	var asked []string
	p.l.ScoutGate = func(fleet config.Fleet, harness string) error {
		asked = append(asked, fleet.Key+"/"+harness)
		if harness != "claude" {
			return errors.New("a scout cannot run on " + harness + ": no scout guard for " + harness)
		}
		return nil
	}
	boat := func(name, harness string, scout bool) error {
		return p.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: name, Harness: harness, Dir: p.work, Prompt: "go", Scout: scout})
	}

	err := boat("codex-boat", "codex", true)
	if err == nil || !strings.Contains(err.Error(), "codex") {
		t.Errorf("a scout's codex boat: LaunchCrew = %v, want a refusal naming codex", err)
	}
	if names, _ := p.l.server().CrewNames(ship.Session); strings.Contains(strings.Join(names, ","), "codex-boat") {
		t.Errorf("the refused boat left a window: %v", names)
	}
	if err := boat("claude-boat", "claude", true); err != nil {
		t.Errorf("a scout's claude boat: LaunchCrew = %v, want it launched", err)
	}
	asked = nil
	if err := boat("plain-boat", "codex", false); err != nil {
		t.Errorf("a boat of a ship that is not a scout: LaunchCrew = %v, want it launched", err)
	}
	if len(asked) != 0 {
		t.Errorf("the scout gate was asked %v for a caller that is not a scout", asked)
	}
}

func TestACrewLaunchFromAScoutWithNoScoutGateIsRefused(t *testing.T) {
	p := newPinRig(t, nil)
	ship := p.ship(t)
	if err := p.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: "boat", Harness: "claude", Dir: p.work, Prompt: "go", Scout: true}); err == nil {
		t.Error("LaunchCrew from a scout with no scout gate configured launched: it must fail closed")
	}
}
