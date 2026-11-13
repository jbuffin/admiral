package commitments_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commitments"
)

func TestATimeCommitmentFiresItsReminderOnceAtItsTime(t *testing.T) {
	g := newRig(t)
	c := g.add(remindAt(t0.Add(10 * time.Minute)))
	g.clk.set(t0.Add(5 * time.Minute))
	g.tick()
	g.wantWaiting(c.ID)

	g.clk.set(t0.Add(10 * time.Minute))
	g.tick()
	card := g.wantCard(c.ID, commitments.OutcomeReminded)
	if card.Text != c.Text {
		t.Errorf("the reminder card holds text %q, want the commitment's %q", card.Text, c.Text)
	}
	g.tick()
	if n := len(g.cards.cards()); n != 1 {
		t.Errorf("%d cards after a second tick, want the one", n)
	}
}

func TestAMissedTimeFiresOnceWhenTheRunnerStarts(t *testing.T) {
	g := newRig(t)
	c := g.add(remindAt(t0.Add(10 * time.Minute)))
	g.clk.set(t0.Add(3 * time.Hour)) // Admiral was down past the time
	g.startRun()
	waitFor(t, "the missed time never fired a card on start", func() bool { _, ok := g.cardFor(c.ID, commitments.OutcomeReminded); return ok })
	g.restart()
	g.tick()
	if n := len(g.cards.cards()); n != 1 {
		t.Errorf("%d cards after a restart, want the one", n)
	}
}

func TestRunFiresACommitmentWhenTheClockReachesItsTime(t *testing.T) {
	g := newRig(t)
	due := t0.Add(10 * time.Minute)
	c := g.add(remindAt(due))
	g.startRun()
	waitFor(t, "Run never armed a timer for the commitment's time", func() bool { return g.clk.hasDeadline(due) })
	g.clk.set(due)
	waitFor(t, "the timer firing never posted the reminder", func() bool { _, ok := g.cardFor(c.ID, commitments.OutcomeReminded); return ok })
}

func TestACommitmentAddedWhileRunningIsSeenAfterChanged(t *testing.T) {
	g := newRig(t)
	g.startRun()
	g.clk.set(t0.Add(time.Minute))
	c := g.add(remindAt(t0.Add(30 * time.Second))) // already due when it is recorded
	g.r.Changed()
	waitFor(t, "a commitment recorded after start never fired after Changed", func() bool { _, ok := g.cardFor(c.ID, commitments.OutcomeReminded); return ok })
}

func TestNothingFiresTwiceAcrossARestart(t *testing.T) {
	g := newRig(t)
	c := g.add(launchAt(t0.Add(time.Minute)))
	g.clk.set(t0.Add(2 * time.Minute))
	g.tick()
	g.wantCard(c.ID, commitments.OutcomeLaunched)
	if n := len(g.ships.requests()); n != 1 {
		t.Fatalf("%d launches before the restart, want 1", n)
	}
	g.restart()
	g.tick()
	g.restart()
	g.startRun()
	g.r.Changed()
	time.Sleep(200 * time.Millisecond) // a relaunch by Run is asynchronous
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launches after two restarts, want still 1", n)
	}
	if n := len(g.cards.cards()); n != 1 {
		t.Errorf("%d cards after two restarts, want 1", n)
	}
}

func TestANoneTriggerNeverFires(t *testing.T) {
	g := newRig(t)
	none := g.add(remindNone())
	due := g.add(remindAt(t0.Add(time.Minute))) // proves the runner runs at all
	g.clk.set(t0.Add(30 * 24 * time.Hour))
	g.tick()
	g.wantCard(due.ID, commitments.OutcomeReminded)
	if _, ok := g.find(none.ID); !ok {
		t.Errorf("the none commitment is closed after 30 days, want it open")
	}
	if _, ok := g.cardFor(none.ID, commitments.OutcomeReminded); ok {
		t.Errorf("the none commitment posted a reminder")
	}
	for _, c := range g.cards.cards() {
		if strings.Contains(c.ID, none.ID) && c.Outcome != commitments.OutcomeAged {
			t.Errorf("the none commitment posted card %+v, want only its aged card", c)
		}
	}
}
