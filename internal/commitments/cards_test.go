package commitments_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/digest"
	"github.com/jbuffin/admiral/internal/launcher"
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

func TestEachCardAddsOneDigestLine(t *testing.T) {
	g := newRig(t)
	g.launched("saltpaw", t0.Add(-time.Hour))
	remind := g.add(remindAt(t0.Add(time.Minute)))
	launch := g.add(launchAt(t0.Add(time.Minute)))
	bad := launchAt(t0.Add(time.Minute))
	bad.Text = "start the long run"
	bad.Launch = []byte(`{"fleet":"work","harness":"claude","dir":"/work/repo","skill":{"kind":"none"},"text":"start the long run"}`)
	badC := g.add(bad)
	drop := shipTrigger("ship_anchored")
	drop.Text = "tell me if saltpaw anchors"
	dropC := g.add(drop)
	g.event("saltpaw", "dismissed", t0.Add(30*time.Second), nil)
	g.ships.launch = func(_ int, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
		if strings.Contains(req.Text, "long run") {
			return commitments.ShipRef{}, refusal(http.StatusBadRequest, "no such checkout", "")
		}
		return commitments.ShipRef{Session: "ship-1", Name: "One"}, nil
	}
	g.clk.set(t0.Add(time.Hour))
	g.tick()
	if n := len(g.cards.cards()); n != 4 {
		t.Fatalf("%d cards %+v, want 4 (reminded, launched, can't run, dropped)", n, g.cards.cards())
	}
	f, err := digest.Read(g.home, fleet)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range f.Lines {
		lines = append(lines, l.Text)
	}
	text := strings.Join(lines, "\n")
	for _, c := range []ledgerText{{remind.ID, remind.Text}, {launch.ID, launch.Text}, {badC.ID, bad.Text}, {dropC.ID, drop.Text}} {
		if !strings.Contains(text, c.text) {
			t.Errorf("digest = %q, want a line for commitment %s (%q)", text, c.id, c.text)
		}
	}
	if len(lines) != 4 {
		t.Errorf("%d digest lines, want one per card (4):\n%s", len(lines), text)
	}
}

type ledgerText struct{ id, text string }
