package commitments_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ledger"
)

func dueLaunch(g *rig) ledger.Commitment {
	c := g.add(launchAt(t0.Add(time.Minute)))
	g.clk.set(t0.Add(2 * time.Minute))
	return c
}

func TestALaunchGoesThroughLaunchWithAutopilotItsKeyAndItsViaAndNoActor(t *testing.T) {
	g := newRig(t)
	c := dueLaunch(g)
	g.tick()
	calls := g.ships.requests()
	if len(calls) != 1 {
		t.Fatalf("%d launches, want 1", len(calls))
	}
	req := calls[0].req
	if !req.Autopilot || !req.QuietGate {
		t.Errorf("Autopilot=%v QuietGate=%v, want both: an unattended launch runs in autopilot and posts no Inbox notice", req.Autopilot, req.QuietGate)
	}
	if req.CommitmentKey != "commitment:"+c.ID {
		t.Errorf("CommitmentKey = %q, want commitment:%s", req.CommitmentKey, c.ID)
	}
	if req.Via != c.Via || req.Via != actor.Via {
		t.Errorf("Via = %q, want the commitment's %q", req.Via, c.Via)
	}
	if req.Fleet != fleet || req.Harness != "claude" || req.Dir != "/work/repo" || req.Text != "fix the build" {
		t.Errorf("request = %+v, want the stored spec", req)
	}
	if _, has := actor.FleetOf(calls[0].ctx); has {
		t.Errorf("Launch ctx carries an actor, which rekeys the launch on the chat prompt")
	}
	card := g.wantCard(c.ID, commitments.OutcomeLaunched)
	if card.Ship != "ship-1" {
		t.Errorf("the launched card names ship %q, want ship-1", card.Ship)
	}
}

func TestAWaitingRefusalKeepsTheCommitmentOpenAndWaitingUntilChangedFreesRoom(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *launcher.Error
	}{
		{"no cap room", refusal(http.StatusConflict, "the fleet is at its cap", "Waiting for room under the cap")},
		{"a resource is busy", refusal(http.StatusConflict, "Waiting for gpu (held by saltpaw)", "gpu")},
		{"the host is unknown", refusal(http.StatusServiceUnavailable, "no host yet", "Waiting for the terminal host")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRig(t)
			g.ships.launch = func(n int, _ launcher.Request) (commitments.ShipRef, *launcher.Error) {
				if n <= 2 {
					return commitments.ShipRef{}, tc.err
				}
				return commitments.ShipRef{Session: "ship-late", Name: "Late"}, nil
			}
			c := dueLaunch(g)
			g.startRun()
			waitFor(t, "the refused launch never left the commitment waiting", func() bool {
				got, ok := g.find(c.ID)
				return ok && got.Waiting
			})
			if n := len(g.cards.cards()); n != 0 {
				t.Fatalf("a waiting launch posted %+v, want no card yet", g.cards.cards())
			}
			g.r.Changed() // a ship or a resource changed
			waitFor(t, "a second refusal never came after Changed", func() bool { return len(g.ships.requests()) >= 2 })
			if got, ok := g.find(c.ID); !ok || !got.Waiting {
				t.Errorf("after a second refusal the commitment is %+v (open %v), want it open and waiting", got, ok)
			}
			g.r.Changed()
			waitFor(t, "the launch never succeeded once room was free", func() bool { _, ok := g.cardFor(c.ID, commitments.OutcomeLaunched); return ok })
			g.wantCard(c.ID, commitments.OutcomeLaunched)
			if n := len(g.ships.requests()); n != 3 {
				t.Errorf("%d launch tries, want 3 (two refused, one launched)", n)
			}
		})
	}
}

func TestAWaitingLaunchSurvivesARestartAndLaunchesWhenRoomFrees(t *testing.T) {
	g := newRig(t)
	full := true
	g.ships.launch = func(n int, _ launcher.Request) (commitments.ShipRef, *launcher.Error) {
		if full {
			return commitments.ShipRef{}, refusal(http.StatusConflict, "the fleet is at its cap", "Waiting for room under the cap")
		}
		return commitments.ShipRef{Session: "ship-after", Name: "After"}, nil
	}
	c := dueLaunch(g)
	g.tick()
	if got, ok := g.find(c.ID); !ok || !got.Waiting {
		t.Fatalf("commitment = %+v (open %v), want waiting", got, ok)
	}
	g.restart()
	full = false
	g.tick()
	g.wantCard(c.ID, commitments.OutcomeLaunched)
}

func TestARefusalWithNoWaitIsACantRunCardAndIsNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *launcher.Error
	}{
		{"a held key", refusal(http.StatusConflict, "ship saltpaw already holds this issue", "")},
		{"a bad request", refusal(http.StatusBadRequest, "autopilot is not proven on claude in work", "")},
		{"not found", refusal(http.StatusNotFound, "no such checkout /work/repo", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRig(t)
			g.ships.launch = func(int, launcher.Request) (commitments.ShipRef, *launcher.Error) {
				return commitments.ShipRef{}, tc.err
			}
			c := dueLaunch(g)
			g.tick()
			card := g.wantCard(c.ID, commitments.OutcomeCantRun)
			if !containsFold(card.Why, tc.err.Msg) {
				t.Errorf("the can't-run card says %q, want the refusal's %q", card.Why, tc.err.Msg)
			}
			g.r.Changed()
			g.tick()
			g.restart()
			g.tick()
			if n := len(g.ships.requests()); n != 1 {
				t.Errorf("%d launch tries, want 1: a refusal that cannot clear is never retried", n)
			}
			if n := len(g.cards.cards()); n != 1 {
				t.Errorf("%d cards, want the one", n)
			}
		})
	}
}

func TestTheFenceRefusesASpecOrSourceOfAnotherFleetWithoutLaunching(t *testing.T) {
	for _, tc := range []struct {
		name, spec string
		runs       bool
	}{
		{"a spec for another fleet", `{"fleet":"home","harness":"claude","dir":"/work/repo","skill":{"kind":"none"},"text":"x"}`, false},
		{"a source of another fleet", `{"fleet":"work","harness":"claude","source":"theirs","order_id":"T-1","skill":{"kind":"none"}}`, false},
		{"a source of its own fleet", `{"fleet":"work","harness":"claude","source":"tracker","order_id":"T-1","skill":{"kind":"none"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRig(t)
			c := launchAt(t0.Add(time.Minute))
			c.Launch = json.RawMessage(tc.spec)
			got := g.add(c)
			g.clk.set(t0.Add(2 * time.Minute))
			g.tick()
			if tc.runs {
				g.wantCard(got.ID, commitments.OutcomeLaunched)
				return
			}
			g.wantCard(got.ID, commitments.OutcomeCantRun)
			if n := len(g.ships.requests()); n != 0 {
				t.Errorf("%d launches, want none past the fence", n)
			}
		})
	}
}

func TestAShipAlreadyHoldingTheKeyMeansTheLaunchHappenedBeforeACrash(t *testing.T) {
	g := newRig(t)
	c := dueLaunch(g)
	g.ships.hold("commitment:"+c.ID, commitments.ShipRef{Session: "saltpaw-2", Name: "Saltpaw"})
	g.tick()
	card := g.wantCard(c.ID, commitments.OutcomeLaunched)
	if card.Ship != "saltpaw-2" {
		t.Errorf("the card names ship %q, want the holder saltpaw-2", card.Ship)
	}
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches, want none: the ship is already there", n)
	}
}

func TestACancelDuringTheLaunchCountsAsSuccess(t *testing.T) {
	g := newRig(t)
	var id string
	g.ships.launch = func(n int, _ launcher.Request) (commitments.ShipRef, *launcher.Error) {
		if err := g.led.CancelCommitment(fleet, id); err != nil {
			t.Errorf("CancelCommitment = %v", err)
		}
		return commitments.ShipRef{Session: "ship-1", Name: "One"}, nil
	}
	c := dueLaunch(g)
	id = c.ID
	g.tick()
	if _, ok := g.cardFor(c.ID, commitments.OutcomeLaunched); !ok {
		t.Errorf("cards = %+v, want the launched card: the ship launched", g.cards.cards())
	}
	if _, ok := g.cardFor(c.ID, commitments.OutcomeCantRun); ok {
		t.Errorf("a cancel in flight made a can't-run card for a ship that launched")
	}
	g.restart()
	g.tick()
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launches, want 1", n)
	}
}

func TestACancelBeforeTheLaunchLockLaunchesNothingAndPostsNoCard(t *testing.T) {
	g := newRig(t)
	var id string
	launched := 0
	g.ships.launch = func(n int, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
		if err := g.led.CancelCommitment(fleet, id); err != nil {
			t.Errorf("CancelCommitment = %v", err)
		}
		if req.Before != nil { // the launcher runs this under its lock, before it makes the session
			if err := req.Before(""); err != nil {
				return commitments.ShipRef{}, refusal(http.StatusConflict, err.Error(), "")
			}
		}
		launched++
		return commitments.ShipRef{Session: "ship-1", Name: "One"}, nil
	}
	c := dueLaunch(g)
	id = c.ID
	g.tick()
	if launched != 0 {
		t.Errorf("%d ships launched for a commitment cancelled before the launch lock", launched)
	}
	if cards := g.cards.cards(); len(cards) != 0 {
		t.Errorf("cards = %+v, want none: the engineer cancelled it", cards)
	}
	if open := g.open(); len(open) != 0 {
		t.Errorf("open = %+v, want none", open)
	}
}

func TestALaunchNamesNoOtherFleetsShip(t *testing.T) {
	g := newRig(t)
	dueLaunch(g)
	g.tick()
	reqs := g.ships.requests()
	if len(reqs) != 1 || reqs[0].req.ByFleet != fleet {
		t.Errorf("requests = %+v, want ByFleet %q so a holder of another fleet is named only by its fleet", reqs, fleet)
	}
}
