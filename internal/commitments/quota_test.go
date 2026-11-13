package commitments_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/launcher"
)

const quotaWait = "Held for quota: Claude's 5-hour window is at 93% (line 90%), resets 14:00"

func TestACommitmentLaunchIsMarkedUnattended(t *testing.T) {
	g := newRig(t)
	dueLaunch(g)
	g.tick()
	calls := g.ships.requests()
	if len(calls) != 1 || !calls[0].req.Unattended {
		t.Fatalf("launches = %+v, want one marked unattended: a commitment counts as unattended though it has Via admiralbot", calls)
	}
}

func TestALaunchHeldForQuotaStaysWaitingWithItsReasonAndLaunchesOnceTheWindowResets(t *testing.T) {
	g := newRig(t)
	g.ships.launch = func(n int, _ launcher.Request) (commitments.ShipRef, *launcher.Error) {
		if n <= 1 {
			return commitments.ShipRef{}, refusal(http.StatusConflict, quotaWait, quotaWait)
		}
		return commitments.ShipRef{Session: "ship-late", Name: "Late"}, nil
	}
	c := dueLaunch(g)
	g.startRun()
	waitFor(t, "the held launch never left the commitment waiting", func() bool {
		got, ok := g.find(c.ID)
		return ok && got.Waiting
	})
	got, _ := g.find(c.ID)
	if got.WaitReason != quotaWait {
		t.Errorf("WaitReason = %q, want the quota hold's wording stored with the commitment", got.WaitReason)
	}
	if cards := g.cards.cards(); len(cards) != 0 {
		t.Errorf("a held launch posted %+v, want no card and no Inbox item", cards)
	}
	g.clk.set(t0.Add(3 * time.Minute))
	g.r.Changed()
	waitFor(t, "the commitment never launched after the window reset", func() bool {
		_, ok := g.cardFor(c.ID, commitments.OutcomeLaunched)
		return ok
	})
}
