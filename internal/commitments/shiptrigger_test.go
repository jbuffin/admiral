package commitments_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/ledger"
)

const (
	fires = "fires"
	waits = "waits"
	drops = "drops"
)

// step is outcome-log lines of saltpaw and then the time the runner looks at them.
type step struct {
	events func(g *rig)
	now    time.Duration // from t0
	want   string
	why    string // text the drop card's reason holds
}

func clean(at time.Duration) func(*rig) { return func(g *rig) { g.endedClean("saltpaw", t0.Add(at)) } }
func errored(at time.Duration) func(*rig) {
	return func(g *rig) { g.endedError("saltpaw", t0.Add(at)) }
}
func stoppedAt(at time.Duration) func(*rig) { return func(g *rig) { g.stopped("saltpaw", t0.Add(at)) } }
func dismissedAt(at time.Duration) func(*rig) {
	return func(g *rig) { g.event("saltpaw", "dismissed", t0.Add(at), nil) }
}
func unmooredAt(reason string, at time.Duration) func(*rig) {
	return func(g *rig) { g.unmoored("saltpaw", reason, t0.Add(at)) }
}
func mooredAt(at time.Duration) func(*rig) {
	return func(g *rig) { g.event("saltpaw", "moored", t0.Add(at), nil) }
}
func all(fs ...func(*rig)) func(*rig) {
	return func(g *rig) {
		for _, f := range fs {
			f(g)
		}
	}
}

const later = time.Hour

// Every row of the table the runner keeps for ship triggers. A row that waits carries a second step that
// resolves it, so a runner that never looks at the log cannot pass it.
var shipRows = []struct {
	name  string
	kind  string
	first step
	then  *step
}{
	// ship_sailed
	{"sailed: a clean end with no moor fires", ledger.TriggerShipSailed, step{clean(time.Minute), later, fires, ""}, nil},
	{"sailed: unmoored as merged fires", ledger.TriggerShipSailed, step{unmooredAt("merged", time.Minute), later, fires, ""}, nil},
	{"sailed: unmoored as closed fires", ledger.TriggerShipSailed, step{unmooredAt("closed", time.Minute), later, fires, ""}, nil},
	{"sailed: unmoored as prs_done fires", ledger.TriggerShipSailed, step{unmooredAt("prs_done", time.Minute), later, fires, ""}, nil},
	{"sailed: nothing happened waits", ledger.TriggerShipSailed, step{func(*rig) {}, later, waits, ""}, &step{clean(2 * time.Hour), 3 * time.Hour, fires, ""}},
	{"sailed: an anchored ship waits", ledger.TriggerShipSailed, step{errored(time.Minute), later, waits, ""}, &step{dismissedAt(2 * time.Hour), 3 * time.Hour, drops, "dismissed"}},
	{"sailed: a moored ship waits", ledger.TriggerShipSailed, step{all(clean(time.Minute), mooredAt(2*time.Minute)), later, waits, ""}, &step{unmooredAt("merged", 2*time.Hour), 3 * time.Hour, fires, ""}},
	{"sailed: a stopped ship drops", ledger.TriggerShipSailed, step{stoppedAt(time.Minute), later, drops, "stopped"}, nil},
	{"sailed: a dismissed ship drops", ledger.TriggerShipSailed, step{dismissedAt(time.Minute), later, drops, "dismissed"}, nil},
	{"sailed: unmoored as dismissed drops", ledger.TriggerShipSailed, step{unmooredAt("dismissed", time.Minute), later, drops, "dismissed"}, nil},
	{"sailed: unmoored as unfollowed drops", ledger.TriggerShipSailed, step{unmooredAt("unfollowed", time.Minute), later, drops, "unfollowed"}, nil},
	// ship_anchored
	{"anchored: an end with an error fires", ledger.TriggerShipAnchored, step{errored(time.Minute), later, fires, ""}, nil},
	{"anchored: nothing happened waits", ledger.TriggerShipAnchored, step{func(*rig) {}, later, waits, ""}, &step{errored(2 * time.Hour), 3 * time.Hour, fires, ""}},
	{"anchored: a moored ship waits", ledger.TriggerShipAnchored, step{all(clean(time.Minute), mooredAt(2*time.Minute)), later, waits, ""}, &step{all(
		func(g *rig) { g.event("saltpaw", "woken", t0.Add(2*time.Hour), nil) }, errored(3*time.Hour)), 4 * time.Hour, fires, ""}},
	{"anchored: a clean end drops, the ship sailed off", ledger.TriggerShipAnchored, step{clean(time.Minute), later, drops, "sailed"}, nil},
	{"anchored: unmoored as merged drops, the ship sailed off", ledger.TriggerShipAnchored, step{unmooredAt("merged", time.Minute), later, drops, "sailed"}, nil},
	{"anchored: a stopped ship drops, a stop is not anchoring", ledger.TriggerShipAnchored, step{stoppedAt(time.Minute), later, drops, "stopped"}, nil},
	{"anchored: a dismissed ship drops", ledger.TriggerShipAnchored, step{dismissedAt(time.Minute), later, drops, "dismissed"}, nil},
	{"anchored: unmoored as dismissed drops", ledger.TriggerShipAnchored, step{unmooredAt("dismissed", time.Minute), later, drops, "dismissed"}, nil},
	{"anchored: unmoored as unfollowed drops", ledger.TriggerShipAnchored, step{unmooredAt("unfollowed", time.Minute), later, drops, "unfollowed"}, nil},
}

func TestEachRowOfTheShipTriggerTable(t *testing.T) {
	for _, row := range shipRows {
		t.Run(row.name, func(t *testing.T) {
			g := newRig(t)
			g.launched("saltpaw", t0.Add(-time.Hour))
			c := g.add(shipTrigger(row.kind))
			run := func(s step) {
				t.Helper()
				s.events(g)
				g.clk.set(t0.Add(s.now))
				g.tick()
				switch s.want {
				case fires:
					g.wantCard(c.ID, commitments.OutcomeReminded)
				case drops:
					card := g.wantCard(c.ID, commitments.OutcomeDropped)
					if !containsFold(card.Why, s.why) {
						t.Errorf("the dropped card says %q, want its reason to mention %q", card.Why, s.why)
					}
				case waits:
					g.wantWaiting(c.ID)
				}
			}
			run(row.first)
			if row.then != nil {
				run(*row.then)
			}
		})
	}
}

func TestACleanEndFiresOnlyAfterTheSettleGraceWithNoMoor(t *testing.T) {
	g := newRig(t)
	g.launched("saltpaw", t0.Add(-time.Hour))
	c := g.add(shipTrigger(ledger.TriggerShipSailed))
	end := t0.Add(time.Minute)
	g.endedClean("saltpaw", end)
	g.clk.set(end.Add(commitments.SettleGrace - time.Second))
	g.tick()
	g.wantWaiting(c.ID)
	g.clk.set(end.Add(commitments.SettleGrace + time.Second))
	g.tick()
	g.wantCard(c.ID, commitments.OutcomeReminded)
}

func TestACleanEndFollowedByAMoorInsideTheGraceNeverFires(t *testing.T) {
	g := newRig(t)
	g.launched("saltpaw", t0.Add(-time.Hour))
	c := g.add(shipTrigger(ledger.TriggerShipSailed))
	end := t0.Add(time.Minute)
	g.endedClean("saltpaw", end)
	g.event("saltpaw", "moored", end.Add(3*time.Second), nil) // the sail-off delay, then settle moors it
	g.clk.set(end.Add(commitments.SettleGrace + time.Second))
	g.tick()
	g.wantWaiting(c.ID)
	g.unmoored("saltpaw", "merged", end.Add(time.Hour))
	g.clk.set(end.Add(2 * time.Hour))
	g.tick()
	g.wantCard(c.ID, commitments.OutcomeReminded)
}

func TestAnEventBeforeTheCommitmentWasRecordedStillCounts(t *testing.T) {
	g := newRig(t)
	g.launched("saltpaw", t0.Add(-time.Hour))
	g.endedError("saltpaw", t0.Add(-time.Minute)) // happened while Admiral was down, before the runner looked
	c := g.add(shipTrigger(ledger.TriggerShipAnchored))
	g.clk.set(t0.Add(time.Hour))
	g.tick()
	g.wantCard(c.ID, commitments.OutcomeReminded)
}

func TestAReusedSessionNeitherFiresNorCounts(t *testing.T) {
	g := newRig(t)
	old := shipTrigger(ledger.TriggerShipAnchored) // saltpaw as launched at t0-1h
	g.launched("saltpaw", t0.Add(-time.Hour))
	oldC := g.add(old)
	fresh := shipTrigger(ledger.TriggerShipAnchored)
	fresh.Trigger.Launched = stamp(t0.Add(10 * time.Minute))
	freshC := g.add(fresh)

	// The old ship is gone and a new one took the name, then that one anchored.
	g.launched("saltpaw", t0.Add(10*time.Minute))
	g.endedError("saltpaw", t0.Add(20*time.Minute))
	g.clk.set(t0.Add(time.Hour))
	g.tick()

	g.wantCard(freshC.ID, commitments.OutcomeReminded)
	if _, ok := g.find(oldC.ID); !ok {
		t.Errorf("the commitment on the first saltpaw is closed by the second ship's end, want it still waiting")
	}
	if _, ok := g.cardFor(oldC.ID, commitments.OutcomeReminded); ok {
		t.Errorf("the second ship's end fired the commitment on the first")
	}
	// Dismissing the new ship is not the old ship's dismissal either.
	g.event("saltpaw", "dismissed", t0.Add(2*time.Hour), nil)
	g.clk.set(t0.Add(3 * time.Hour))
	g.tick()
	if _, ok := g.find(oldC.ID); !ok {
		t.Errorf("the second ship's dismissal dropped the commitment on the first, want it still waiting")
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func TestALaunchRecordWithNoStampNeverMatchesATrigger(t *testing.T) {
	g := newRig(t)
	g.event("saltpaw", "launched", t0.Add(-time.Hour), map[string]any{"fleet": fleet}) // written before records carried the stamp
	c := g.add(shipTrigger(ledger.TriggerShipAnchored))
	g.endedError("saltpaw", t0.Add(time.Minute))
	g.clk.set(t0.Add(time.Hour))
	g.tick()
	g.wantWaiting(c.ID)
}
