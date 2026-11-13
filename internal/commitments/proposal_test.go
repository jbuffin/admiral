package commitments_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ledger"
)

func withYou(n int) *campaigns.Node { return child(issue(n), campaigns.WithYou, campaigns.Ready) }

// readFor is readOf for another campaign.
func readFor(id string, nodes ...*campaigns.Node) campaigns.Read {
	r := readOf(nodes...)
	r.ID = id
	return r
}

// launchesAs makes every launch answer a ship holding its child's key with the status.
func (g *rig) launchesAs(status string) {
	g.ships.launch = func(n int, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
		ref := commitments.ShipRef{Session: fmt.Sprintf("ship-%d", n), Name: fmt.Sprintf("Ship %d", n), Status: status}
		g.ships.hold(req.CommitmentKey, ref)
		return ref, nil
	}
}

func (g *rig) asked(t *testing.T, n int) []fakeAsk {
	t.Helper()
	got := g.props.questions()
	if len(got) != n {
		t.Fatalf("%d questions asked %+v; want %d", len(got), got, n)
	}
	return got
}

func TestAReadyWithYouChildIsProposedOnceAndNotAgainAcrossARestart(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21)))
	g.tick()
	q := g.asked(t, 1)[0]
	if q.Fleet != fleet || q.Child != issue(21) || !strings.HasPrefix(q.ID, "campaign-"+c.ID+"-") {
		t.Errorf("question = %+v; want a campaign-%s-… question for %s in %s", q, c.ID, issue(21), fleet)
	}
	if !strings.Contains(q.Title, "Child 21") || !strings.Contains(q.Title, "Ship the harbour") {
		t.Errorf("title %q; want it to name the child and the campaign", q.Title)
	}
	g.tick()
	g.restart()
	g.tick()
	g.asked(t, 1)
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches before an answer, want none", n)
	}
	got, _ := g.find(c.ID)
	if got.Proposal == nil || got.Proposal.URL != issue(21) || got.Proposal.QuestionID != q.ID {
		t.Errorf("Proposal = %+v; want %s open as %s", got.Proposal, issue(21), q.ID)
	}
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildProposed {
		t.Errorf("child = %+v; want proposed", ch)
	}
}

func TestTwoReadyWithYouChildrenAreProposedOneAtATimeAndANoSkipsTheFirst(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21), withYou(22)))
	g.tick()
	g.tick()
	first := g.asked(t, 1)[0]
	if first.Child != issue(21) {
		t.Fatalf("first question is about %s, want %s", first.Child, issue(21))
	}
	g.props.reply(first.ID, commitments.ProposalNo)
	g.tick()
	g.tick()
	qs := g.asked(t, 2)
	if qs[1].Child != issue(22) || qs[1].ID == first.ID {
		t.Errorf("second question = %+v; want a new question about %s", qs[1], issue(22))
	}
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildDeclined {
		t.Errorf("child 21 = %+v; want declined", ch)
	}
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches after a No, want none", n)
	}
}

func TestYesLaunchesTheChildWithYouAndPostsALaunchedCardWithItsShip(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21)))
	g.launchesAs("underway")
	g.tick()
	q := g.asked(t, 1)[0]
	g.props.reply(q.ID, commitments.ProposalYes)
	g.tick()
	calls := g.ships.requests()
	if len(calls) != 1 {
		t.Fatalf("%d launches after a Yes, want 1", len(calls))
	}
	req := calls[0].req
	if req.Autopilot || req.QuietGate {
		t.Errorf("request %+v; a With-you launch is neither autopilot nor quiet-gated", req)
	}
	if req.Skill.Kind != "commodore" || req.Source != config.ReservedSourceKey || req.Harness != "claude" || !req.Clone || req.CloneRepo != "github.com/acme/work" {
		t.Errorf("request %+v; want the node's skill, the campaign source, the commitment's harness and the order's clone", req)
	}
	if req.CommitmentKey != "commitment:"+c.ID+":"+issue(21) || req.OrderID != campID+"/"+issue(21) || req.Fleet != fleet || req.ByFleet != fleet || req.Via != actor.Via || req.Text != "order for "+issue(21) || req.Before == nil {
		t.Errorf("request %+v; want the per-child key, the order, the fleet, who started it and a cancel check", req)
	}
	ch := g.children(c.ID)[issue(21)]
	if ch.State != ledger.ChildLaunched || ch.Mode != ledger.ModeWithYou || ch.Ship != "ship-1" {
		t.Errorf("child = %+v; want launched with you on ship-1", ch)
	}
	cards := g.cardsOf(c.ID, commitments.OutcomeLaunched)
	if len(cards) != 1 || cards[0].Child != issue(21) || cards[0].Ship != "ship-1" {
		t.Errorf("launched cards %+v; want one naming %s and ship-1", cards, issue(21))
	}
	if _, ok := g.find(c.ID); !ok {
		t.Error("the commitment closed on a launch, want it open")
	}
	if w := g.props.withdrawals(); len(w) != 1 || w[0] != q.ID {
		t.Errorf("withdrawn %v; want the answered question %s removed once the child launched", w, q.ID)
	}
	g.tick()
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launches after another pass, want still 1", n)
	}
}

func TestYesOnAChildWithNoSkillLaunchesItWithNoSkill(t *testing.T) {
	g := newRig(t)
	g.work("claude")
	plain := withYou(21)
	plain.Skill = campaigns.Skill{}
	g.camp.set(readOf(plain))
	g.tick()
	g.props.reply(g.asked(t, 1)[0].ID, commitments.ProposalYes)
	g.tick()
	calls := g.ships.requests()
	if len(calls) != 1 || calls[0].req.Skill.Kind != "none" {
		t.Fatalf("launches %+v; want one with skill kind none", calls)
	}
}

func TestYesOnAChildThatIsNoLongerReadyLaunchesNothingAndWithdrawsTheQuestion(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21), withYou(22)))
	g.tick()
	q := g.asked(t, 1)[0]
	g.props.reply(q.ID, commitments.ProposalYes)
	g.camp.set(readOf(child(issue(21), campaigns.WithYou, campaigns.Blocked), withYou(22)))
	g.tick()
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches of a child that is blocked now, want none", n)
	}
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildWithdrawn || ch.Reason != "not ready" {
		t.Errorf("child 21 = %+v; want withdrawn: not ready", ch)
	}
}

func TestAProposedChildThatStopsBeingReadyHasItsQuestionWithdrawnAndIsProposedAgainWhenReadyAgain(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21)))
	g.tick()
	first := g.asked(t, 1)[0]
	g.camp.set(readOf(child(issue(21), campaigns.WithYou, campaigns.Blocked)))
	g.tick()
	if w := g.props.withdrawals(); len(w) != 1 || w[0] != first.ID {
		t.Errorf("withdrawn %v; want %s", w, first.ID)
	}
	if got, _ := g.find(c.ID); got.Proposal != nil {
		t.Errorf("Proposal = %+v; want none after the withdrawal", got.Proposal)
	}
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildWithdrawn || ch.Reason != "not ready" {
		t.Errorf("child = %+v; want withdrawn: not ready", ch)
	}
	g.camp.set(readOf(withYou(21)))
	g.tick()
	g.tick()
	qs := g.asked(t, 2)
	if qs[1].Child != issue(21) || qs[1].ID == first.ID {
		t.Errorf("second question = %+v; want a new id for %s", qs[1], issue(21))
	}
	if ch := g.children(c.ID)[issue(21)]; ch.Proposals != 2 {
		t.Errorf("child = %+v; want asked twice", ch)
	}
}

func TestACampaignThatEndsWithdrawsItsQuestionBeforeItsEndedCard(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21)))
	g.tick()
	q := g.asked(t, 1)[0]
	g.camp.set(closedRead(withYou(21)))
	g.tick()
	if w := g.props.withdrawals(); len(w) != 1 || w[0] != q.ID {
		t.Errorf("withdrawn %v; want %s", w, q.ID)
	}
	g.wantCard(c.ID, commitments.OutcomeEnded)
	text := g.ledgerText()
	w, d := strings.Index(text, `"event":"`+ledger.EventChildWithdrawn+`"`), strings.Index(text, `"event":"done"`)
	if w < 0 || d < 0 || w > d || !strings.Contains(text, `"reason":"ended"`) {
		t.Errorf("ledger %s; want child_withdrawn (ended) before the commitment's done", text)
	}
}

func TestUnfollowingAndAVanishedFollowWithdrawTheQuestionToo(t *testing.T) {
	for name, read := range map[string]campaigns.Read{
		"unfollowed": func() campaigns.Read { r := readOf(withYou(21)); r.Followed = false; return r }(),
		"not found":  {},
	} {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			c := g.work("claude")
			g.camp.set(readOf(withYou(21)))
			g.tick()
			q := g.asked(t, 1)[0]
			g.camp.set(read)
			g.tick()
			if w := g.props.withdrawals(); len(w) != 1 || w[0] != q.ID {
				t.Errorf("withdrawn %v; want %s", w, q.ID)
			}
			g.wantCard(c.ID, commitments.OutcomeEnded)
		})
	}
}

func TestAQuestionRemovedByHandIsRecordedWithdrawnAndTheChildProposedAgain(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21)))
	g.tick()
	first := g.asked(t, 1)[0]
	g.props.gone(first.ID)
	g.tick()
	g.tick()
	qs := g.asked(t, 2)
	if qs[1].Child != issue(21) || qs[1].ID == first.ID {
		t.Errorf("second question = %+v; want a new id for %s", qs[1], issue(21))
	}
	if !strings.Contains(g.ledgerText(), ledger.EventChildWithdrawn) {
		t.Error("the removed question was not recorded as withdrawn")
	}
	if ch := g.children(c.ID)[issue(21)]; ch.Proposals != 2 {
		t.Errorf("child = %+v; want asked twice", ch)
	}
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches for a question nobody answered, want none", n)
	}
}

func TestAShipThatHoldsTheFleetStopsTheNextProposalOnlyWhileUnderwayOrNeedingYou(t *testing.T) {
	for _, tc := range []struct {
		status string
		holds  bool
	}{
		{"underway", true}, {"needs_you", true},
		{"anchored", false}, {"sailing_off", false}, {"moored", false}, {"", false},
	} {
		t.Run("ship "+tc.status, func(t *testing.T) {
			g := newRig(t)
			g.work("claude")
			g.camp.set(readOf(withYou(21), withYou(22)))
			g.launchesAs(tc.status)
			g.tick()
			g.props.reply(g.asked(t, 1)[0].ID, commitments.ProposalYes)
			g.tick()
			g.tick()
			g.tick()
			want := 2
			if tc.holds {
				want = 1
			}
			qs := g.props.questions()
			if len(qs) != want {
				t.Fatalf("%d questions %+v with the ship %q; want %d", len(qs), qs, tc.status, want)
			}
			if !tc.holds && qs[1].Child != issue(22) {
				t.Errorf("second question is about %s, want %s", qs[1].Child, issue(22))
			}
		})
	}
}

func TestOneWithYouQuestionIsOpenAtATimeAcrossTheCommitmentsOfAFleet(t *testing.T) {
	g := newRig(t)
	g.workIn(fleet, "w1", "camp-1", "claude")
	g.workIn(fleet, "w2", "camp-2", "claude")
	g.camp.setFor("camp-1", readFor("camp-1", withYou(21)))
	g.camp.setFor("camp-2", readFor("camp-2", withYou(31)))
	g.launchesAs("underway")
	g.tick()
	g.tick()
	first := g.asked(t, 1)[0]
	g.props.reply(first.ID, commitments.ProposalYes)
	g.tick()
	g.tick()
	g.asked(t, 1)
	calls := g.ships.requests()
	if len(calls) != 1 {
		t.Fatalf("%d launches, want 1", len(calls))
	}
	// The first ship's lifecycle moves on: it sails off, and the other commitment's child is proposed.
	g.ships.hold(calls[0].req.CommitmentKey, commitments.ShipRef{Session: "ship-1", Name: "Ship 1", Status: "moored"})
	g.tick()
	g.tick()
	qs := g.asked(t, 2)
	if qs[1].Child == first.Child {
		t.Errorf("second question %+v is about the child already launched", qs[1])
	}
}

func TestAShipOfAnotherFleetDoesNotHoldThisFleet(t *testing.T) {
	g := newRig(t)
	g.fleets = []string{fleet, "home"}
	g.restart()
	g.workIn("home", "h1", "camp-h", "claude")
	g.appendRawIn("home", fmt.Sprintf(`{"v":1,"ts":%q,"event":"child_launched","id":"h1","url":%q,"ship":"theirs-1","mode":"with_you"}`+"\n", stamp(t0), issue(41)))
	g.ships.hold("commitment:h1:"+issue(41), commitments.ShipRef{Session: "theirs-1", Name: "Theirs", Status: "underway"})
	g.camp.setFor("camp-h", readFor("camp-h", child(issue(41), campaigns.WithYou, campaigns.Underway)))
	g.work("claude")
	g.camp.setFor(campID, readOf(withYou(21)))
	g.tick()
	qs := g.asked(t, 1)
	if qs[0].Fleet != fleet || qs[0].Child != issue(21) {
		t.Errorf("question = %+v; want %s in %s while the other fleet's ship is underway", qs[0], issue(21), fleet)
	}
}

func TestAWithoutYouShipThatIsUnderwayDoesNotHoldTheFleetAndItsLaunchRecordsItsMode(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(ready(22), withYou(21)))
	g.launchesAs("underway")
	g.tick()
	g.tick()
	if qs := g.props.questions(); len(qs) != 1 || qs[0].Child != issue(21) {
		t.Errorf("questions %+v; want the With-you child asked while a Without-you ship is underway", qs)
	}
	calls := g.ships.requests()
	if len(calls) != 1 || !calls[0].req.Autopilot || !calls[0].req.QuietGate {
		t.Errorf("launches %+v; want the Without-you child in autopilot", calls)
	}
	if ch := g.children(c.ID)[issue(22)]; ch.State != ledger.ChildLaunched || ch.Mode != ledger.ModeWithoutYou {
		t.Errorf("child 22 = %+v; want launched without you", ch)
	}
}

func TestARefusedYesPostsACantRunCardRecordsTheChildFailedAndIsNotRetried(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21)))
	g.ships.launch = func(int, launcher.Request) (commitments.ShipRef, *launcher.Error) {
		return commitments.ShipRef{}, refusal(409, "issue 21 is claimed by another ship", "")
	}
	g.tick()
	g.props.reply(g.asked(t, 1)[0].ID, commitments.ProposalYes)
	g.tick()
	g.tick()
	g.tick()
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launch attempts, want 1: a refusal is not retried", n)
	}
	cards := g.cardsOf(c.ID, commitments.OutcomeCantRun)
	if len(cards) != 1 || cards[0].Child != issue(21) || !strings.Contains(cards[0].Why, "claimed") {
		t.Errorf("can't-run cards %+v; want one for %s with the refusal's words", cards, issue(21))
	}
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildFailed {
		t.Errorf("child = %+v; want failed", ch)
	}
}

func TestAYesThatMustWaitKeepsItsAnswerAndRetriesThirtySecondsLater(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(withYou(21)))
	g.ships.launch = func(n int, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
		if n == 1 {
			return commitments.ShipRef{}, refusal(409, "the fleet is at its cap", "cap")
		}
		return commitments.ShipRef{Session: "ship-late", Name: "Late", Status: "underway"}, nil
	}
	g.tick()
	g.props.reply(g.asked(t, 1)[0].ID, commitments.ProposalYes)
	g.startRun()
	waitFor(t, "a retry pass 30s after the refused Yes", func() bool { return g.clk.hasDeadline(t0.Add(30 * time.Second)) })
	if n := len(g.ships.requests()); n != 1 {
		t.Fatalf("%d launches, want the one refused", n)
	}
	g.clk.set(t0.Add(30 * time.Second))
	waitFor(t, "the retried launch", func() bool { return g.children(c.ID)[issue(21)].State == ledger.ChildLaunched })
	if ch := g.children(c.ID)[issue(21)]; ch.Ship != "ship-late" || ch.Mode != ledger.ModeWithYou {
		t.Errorf("child = %+v; want launched with you on ship-late", ch)
	}
}

func TestAFleetWithoutAChatProposesNothingAndLeavesAnOpenQuestionAlone(t *testing.T) {
	g := newRig(t)
	g.work("claude")
	g.camp.set(readOf(withYou(21), withYou(22)))
	g.props.chatOff(fleet)
	g.tick()
	g.tick()
	g.asked(t, 0)
	g.props.chatOn(fleet)
	g.tick()
	first := g.asked(t, 1)[0]
	g.props.chatOff(fleet)
	g.tick()
	g.tick()
	g.asked(t, 1)
	if w := g.props.withdrawals(); len(w) != 0 {
		t.Errorf("withdrawn %v; a chat going off leaves the open question alone", w)
	}
	g.props.chatOn(fleet)
	g.props.reply(first.ID, commitments.ProposalNo)
	g.tick()
	g.tick()
	g.asked(t, 2)
}

func TestACommitmentAtTheChildEventBoundProposesNothingAndPostsOneTooLargeCard(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	var b strings.Builder
	for i := 0; i < ledger.MaxChildEvents; i++ {
		fmt.Fprintf(&b, `{"v":1,"ts":%q,"event":"child_launched","id":%q,"url":%q,"ship":"ship"}`+"\n", stamp(t0), c.ID, issue(1000+i))
	}
	g.appendRaw(b.String())
	g.camp.set(readOf(withYou(21)))
	g.tick()
	g.tick()
	g.asked(t, 0)
	if cards := g.cardsOf(c.ID, commitments.OutcomeTooLarge); len(cards) != 1 || cards[0].Why == "" {
		t.Errorf("too-large cards %+v; want exactly one that says why", cards)
	}
}

func TestAnAnsweredQuestionIsRemovedOnceTheRunnerHasActedOnItButNotWhileAYesWaits(t *testing.T) {
	t.Run("no", func(t *testing.T) {
		g := newRig(t)
		g.work("claude")
		g.camp.set(readOf(withYou(21)))
		g.tick()
		q := g.asked(t, 1)[0]
		g.props.reply(q.ID, commitments.ProposalNo)
		g.tick()
		if w := g.props.withdrawals(); len(w) != 1 || w[0] != q.ID {
			t.Errorf("withdrawn %v; want %s removed after the No", w, q.ID)
		}
	})
	t.Run("refused yes", func(t *testing.T) {
		g := newRig(t)
		g.work("claude")
		g.camp.set(readOf(withYou(21)))
		g.ships.launch = func(int, launcher.Request) (commitments.ShipRef, *launcher.Error) {
			return commitments.ShipRef{}, refusal(409, "issue 21 is claimed by another ship", "")
		}
		g.tick()
		q := g.asked(t, 1)[0]
		g.props.reply(q.ID, commitments.ProposalYes)
		g.tick()
		if w := g.props.withdrawals(); len(w) != 1 || w[0] != q.ID {
			t.Errorf("withdrawn %v; want %s removed after the refused Yes", w, q.ID)
		}
	})
	t.Run("waiting yes", func(t *testing.T) {
		g := newRig(t)
		g.work("claude")
		g.camp.set(readOf(withYou(21)))
		g.ships.launch = func(int, launcher.Request) (commitments.ShipRef, *launcher.Error) {
			return commitments.ShipRef{}, refusal(409, "the fleet is at its cap", "cap")
		}
		g.tick()
		q := g.asked(t, 1)[0]
		g.props.reply(q.ID, commitments.ProposalYes)
		g.tick()
		if w := g.props.withdrawals(); len(w) != 0 {
			t.Errorf("withdrawn %v; a Yes that waits for room keeps its answer", w)
		}
	})
}

func TestAnOpenProposalReservesAnEventSoLaunchesCannotCrowdOutItsAnswer(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	var b strings.Builder
	for i := 0; i < ledger.MaxChildEvents-3; i++ {
		fmt.Fprintf(&b, `{"v":1,"ts":%q,"event":"child_launched","id":%q,"url":%q,"ship":"ship"}`+"\n", stamp(t0), c.ID, issue(1000+i))
	}
	g.appendRaw(b.String())
	g.camp.set(readOf(withYou(21)))
	g.tick()
	q := g.asked(t, 1)[0]
	g.camp.set(readOf(withYou(21), ready(30)))
	g.tick()
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches with the proposal's answer needing the last event, want none", n)
	}
	g.props.reply(q.ID, commitments.ProposalYes)
	g.tick()
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildLaunched {
		t.Errorf("child 21 = %+v; want the Yes recorded as launched", ch)
	}
	if got, _ := g.find(c.ID); got.Proposal != nil {
		t.Errorf("Proposal = %+v; want it cleared", got.Proposal)
	}
}

func TestAShipStartedByHandOnAWithYouChildDoesNotHoldTheFleet(t *testing.T) {
	g := newRig(t)
	g.work("claude")
	g.camp.set(readOf(withYou(22)))
	// Underway, but not held under the commitment's key: someone launched it by hand.
	g.ships.hold("hand:"+issue(21), commitments.ShipRef{Session: "hand-1", Name: "By hand", Status: "underway"})
	g.tick()
	if qs := g.props.questions(); len(qs) != 1 || qs[0].Child != issue(22) {
		t.Errorf("questions %+v; want %s proposed while a hand-launched ship is underway", qs, issue(22))
	}
}
