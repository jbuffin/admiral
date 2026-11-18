package ships

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
)

// Which engineer turn a launch records on a ship.

func TestAScoutAdmiralBotLaunchesRecordsTheEngineerTurnItLaunchedInAndNoOtherShipDoes(t *testing.T) {
	r := newWakeRig(t, 0)
	if err := capabilities.Open(r.home).Record("work", "claude", "2.1.300", capabilities.ScoutGuard, capabilities.Result{Passed: true, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r.s.PromptKey = func(fleet string) string { return "nonce-" + fleet + "-7" }
	scout := r.launchBody(t, true, `,"skill":{"kind":"none"},"autopilot":true,"scout":true,"reason":"to read the logs"`)
	quick := r.launchBody(t, true, `,"skill":{"kind":"none"},"autopilot":true,"reason":"to read the logs"`)
	byHand := r.launchBody(t, false, `,"skill":{"kind":"none"},"autopilot":true,"scout":true`)
	got := map[string]string{}
	for _, s := range r.s.List() {
		got[s.Session] = s.PromptKey
	}
	if got[scout] != "nonce-work-7" {
		t.Errorf("the chat scout's turn = %q, want nonce-work-7", got[scout])
	}
	if got[quick] != "" || got[byHand] != "" {
		t.Errorf("a quick ship's turn = %q and a hand-launched scout's = %q, want none for both", got[quick], got[byHand])
	}
}
