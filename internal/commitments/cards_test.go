package commitments_test

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commitments"
)

func TestACardIsIDedByItsCommitmentAndOutcomeAndPostedBeforeTheCommitmentCloses(t *testing.T) {
	g := newRig(t)
	c := g.add(remindAt(t0.Add(time.Minute)))
	var stillOpen bool
	g.cards.onPost = func(commitments.Card) error { _, stillOpen = g.find(c.ID); return nil }
	g.clk.set(t0.Add(2 * time.Minute))
	g.tick()
	card := g.wantCard(c.ID, commitments.OutcomeReminded)
	if card.ID != "commitment-"+c.ID+"-reminded" {
		t.Errorf("card ID = %q, want commitment-%s-reminded", card.ID, c.ID)
	}
	if !stillOpen {
		t.Errorf("the commitment was already closed when its card was posted, want the card first so a crash between the two re-posts the same ID")
	}
}

func TestACrashAfterTheCardAndBeforeDoneRepostsTheSameCardAndCloses(t *testing.T) {
	g := newRig(t)
	c := g.add(remindAt(t0.Add(time.Minute)))
	g.clk.set(t0.Add(2 * time.Minute))
	first := true
	g.cards.onPost = func(commitments.Card) error {
		if first {
			first = false
			return errCrash // the card is kept, then the process dies before Done
		}
		return nil
	}
	g.tick()
	if _, ok := g.find(c.ID); !ok {
		t.Fatalf("the commitment closed although its card post failed, want it open for the retry")
	}
	g.restart()
	g.tick()
	g.wantCard(c.ID, commitments.OutcomeReminded)
	if n := len(g.cards.cards()); n != 1 {
		t.Errorf("%d distinct cards after the retry, want 1: the same ID twice", n)
	}
	if g.cards.posts < 2 {
		t.Errorf("%d posts, want the retry to post again", g.cards.posts)
	}
}
