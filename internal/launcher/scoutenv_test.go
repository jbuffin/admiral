package launcher

import "testing"

func TestAShipThatIsNotAScoutNeverHasTheScoutVariableWhoeverSetsIt(t *testing.T) {
	t.Setenv("ADMIRAL_SCOUT", "/admirals/own/env")
	r := newCrewDirRig(t, map[string]string{"ADMIRAL_SCOUT": "/fleet/says/so"})
	r.launch(t)
	if got, set := r.recorded(t, "claude")["ADMIRAL_SCOUT"]; set {
		t.Errorf("ADMIRAL_SCOUT = %q on a ship that is not a scout, want it unset though the fleet's env and Admiral's set it", got)
	}
}

func TestACrewBoatsDockCannotSetTheScoutVariable(t *testing.T) {
	t.Parallel()
	r := newCrewDirRig(t, nil)
	ship := r.launch(t)
	r.recorded(t, "claude")
	r.l.Config().Fleets[0].Harnesses[1].Env["ADMIRAL_SCOUT"] = "/dock/says/so"
	if err := r.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: "boat", Harness: "codex", Dir: r.work, Prompt: "go"}); err != nil {
		t.Fatalf("LaunchCrew: %v", err)
	}
	if got, set := r.recorded(t, "codex")["ADMIRAL_SCOUT"]; set {
		t.Errorf("a boat of a ship that is not a scout has ADMIRAL_SCOUT = %q, want it unset though its dock sets one", got)
	}
}
