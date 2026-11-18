package commitments_test

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/ledger"
)

// scoutStopped is what stopShip writes for a scout's own end: killed, then stopped with the scout mark.
func scoutStopped(g *rig, at time.Time) {
	g.event("saltpaw", "ended", at, map[string]any{"exit_code": nil, "signal": "KILL"})
	g.event("saltpaw", "stopped", at.Add(time.Second), map[string]any{"scout": true})
}

func TestAScoutsStopReadsAsSailedSoShipSailedFires(t *testing.T) {
	g := newRig(t)
	g.launched("saltpaw", t0.Add(-time.Hour))
	c := g.add(shipTrigger(ledger.TriggerShipSailed))
	scoutStopped(g, t0.Add(time.Minute))
	g.clk.set(t0.Add(later))
	g.tick()
	g.wantCard(c.ID, commitments.OutcomeReminded)
}

func TestAScoutsStopReadsAsSailedSoShipAnchoredDrops(t *testing.T) {
	g := newRig(t)
	g.launched("saltpaw", t0.Add(-time.Hour))
	c := g.add(shipTrigger(ledger.TriggerShipAnchored))
	scoutStopped(g, t0.Add(time.Minute))
	g.clk.set(t0.Add(later))
	g.tick()
	card := g.wantCard(c.ID, commitments.OutcomeDropped)
	if !containsFold(card.Why, "sailed") {
		t.Errorf("the dropped card says %q, want its reason to say the ship sailed", card.Why)
	}
}

func TestAStopWithoutTheScoutMarkStillReadsAsStopped(t *testing.T) {
	g := newRig(t)
	g.launched("saltpaw", t0.Add(-time.Hour))
	c := g.add(shipTrigger(ledger.TriggerShipSailed))
	g.stopped("saltpaw", t0.Add(time.Minute))
	g.clk.set(t0.Add(later))
	g.tick()
	card := g.wantCard(c.ID, commitments.OutcomeDropped)
	if !containsFold(card.Why, "stopped") {
		t.Errorf("the dropped card says %q, want its reason to say the ship was stopped", card.Why)
	}
}
