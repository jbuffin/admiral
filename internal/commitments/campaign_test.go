package commitments_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ledger"
)

const campID = "camp-1"

// fakeCampaign is the follow store's answer: whatever a test last set, counted per read.
type fakeCampaign struct {
	mu    sync.Mutex
	read_ campaigns.Read
	reads int
}

func (f *fakeCampaign) read(id string) campaigns.Read {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.read_
}

func (f *fakeCampaign) set(r campaigns.Read) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.read_ = r
}

func (f *fakeCampaign) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func issue(n int) string { return fmt.Sprintf("https://github.com/acme/widgets/issues/%d", n) }

func child(url string, mode campaigns.Mode, status campaigns.Status) *campaigns.Node {
	return &campaigns.Node{URL: url, Title: "Child " + url[strings.LastIndex(url, "/")+1:], Repo: "acme/widgets", Mode: mode, Status: status, Skill: campaigns.Skill{Kind: "commodore"}}
}

func ready(n int) *campaigns.Node { return child(issue(n), campaigns.WithoutYou, campaigns.Ready) }

// readOf is an open, followed campaign whose ready children are the nodes given, each with an order.
func readOf(nodes ...*campaigns.Node) campaigns.Read {
	root := &campaigns.Node{URL: issue(20), Title: "Ship the harbour", Repo: "acme/widgets", Parent: true, Children: nodes}
	r := campaigns.Read{ID: campID, Found: true, Followed: true, Orders: map[string]campaigns.Order{}}
	r.Campaign.Root = root
	for _, n := range nodes {
		if n.Status == campaigns.Ready {
			r.Campaign.Ready = append(r.Campaign.Ready, n)
		}
		r.Orders[n.URL] = campaigns.Order{Text: "order for " + n.URL, CloneRepo: "github.com/acme/work"}
	}
	return r
}

func closedRead(nodes ...*campaigns.Node) campaigns.Read {
	r := readOf(nodes...)
	r.Campaign.Ready = nil
	r.Campaign.Root.Closed = true
	for _, n := range nodes {
		n.Closed, n.Status = true, campaigns.Done
	}
	return r
}

// work seeds an open campaign commitment as a hand-written ledger line, so a runner test does not lean on the ledger's writer.
func (g *rig) work(harness string) ledger.Commitment {
	g.t.Helper()
	dir := filepath.Join(g.home, "chat", fleet)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		g.t.Fatal(err)
	}
	g.appendRaw(fmt.Sprintf(`{"v":1,"ts":%q,"event":"added","id":"w1","kind":"commitment","text":"work on acme/widgets#20","via":"admiralbot","trigger":{"kind":"campaign","subject":%q},"action":"work","harness":%q}`+"\n", stamp(t0), campID, harness))
	return ledger.Commitment{ID: "w1", Text: "work on acme/widgets#20", Trigger: ledger.Trigger{Kind: ledger.TriggerCampaign, Subject: campID}, Action: ledger.ActionWork, Harness: harness, Via: actor.Via}
}

func (g *rig) appendRaw(lines string) {
	g.t.Helper()
	f, err := os.OpenFile(filepath.Join(g.home, "chat", fleet, "ledger.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		g.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(lines); err != nil {
		g.t.Fatal(err)
	}
}

func (g *rig) children(id string) map[string]ledger.Child {
	g.t.Helper()
	c, ok := g.find(id)
	if !ok {
		g.t.Fatalf("commitment %s is closed, want it open", id)
	}
	out := map[string]ledger.Child{}
	for _, ch := range c.Children {
		out[ch.URL] = ch
	}
	return out
}

// cardsOf are the cards of one outcome for a commitment, in the order posted.
func (g *rig) cardsOf(id, outcome string) []commitments.Card {
	var out []commitments.Card
	for _, c := range g.cards.cards() {
		if c.Commitment == id && c.Outcome == outcome {
			out = append(out, c)
		}
	}
	return out
}

func (g *rig) ledgerText() string {
	g.t.Helper()
	b, err := os.ReadFile(filepath.Join(g.home, "chat", fleet, "ledger.jsonl"))
	if err != nil {
		g.t.Fatal(err)
	}
	return string(b)
}

func TestAReadyWithoutYouChildLaunchesInAutopilotWithItsOrderAndTheCommitmentStaysOpen(t *testing.T) {
	g := newRig(t)
	c := g.work("codex")
	g.camp.set(readOf(ready(21)))
	g.tick()

	calls := g.ships.requests()
	if len(calls) != 1 {
		t.Fatalf("%d launches, want 1", len(calls))
	}
	req := calls[0].req
	if req.Fleet != fleet || req.Source != "campaign" || req.OrderID != campID+"/"+issue(21) {
		t.Errorf("request fleet %q source %q order %q; want work, campaign, %s/%s", req.Fleet, req.Source, req.OrderID, campID, issue(21))
	}
	if req.Skill.Kind != "commodore" || req.Text != "order for "+issue(21) {
		t.Errorf("request skill %+v text %q; want the child's skill and its order text", req.Skill, req.Text)
	}
	if req.Harness != "codex" || !req.Clone || req.CloneRepo != "github.com/acme/work" {
		t.Errorf("request harness %q clone %v clone_repo %q; want the stored harness, Clone, and the order's repo", req.Harness, req.Clone, req.CloneRepo)
	}
	if !req.Autopilot || !req.QuietGate || req.Via != actor.Via || req.ByFleet != fleet {
		t.Errorf("request autopilot %v quiet %v via %q by %q; want autopilot, quiet gate, admiralbot, work", req.Autopilot, req.QuietGate, req.Via, req.ByFleet)
	}
	if req.CommitmentKey != "commitment:"+c.ID+":"+issue(21) {
		t.Errorf("commitment key %q; want commitment:<id>:<child url>", req.CommitmentKey)
	}
	if req.Before == nil {
		t.Error("the request has no Before, so a cancel could not stop the launch")
	}
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildLaunched || ch.Ship != "ship-1" {
		t.Errorf("child = %+v; want launched on ship-1", ch)
	}
	cards := g.cardsOf(c.ID, commitments.OutcomeLaunched)
	if len(cards) != 1 || cards[0].Child != issue(21) || cards[0].Ship != "ship-1" || !strings.HasPrefix(cards[0].ID, "commitment-"+c.ID+"-launched-") {
		t.Errorf("launched cards %+v; want one for %s on ship-1 with an id of commitment-%s-launched-<child>", cards, issue(21), c.ID)
	}
}

func TestABlockedChildLaunchesOnALaterPassOnceAReadMakesItReady(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(child(issue(21), campaigns.WithoutYou, campaigns.Blocked)))
	g.tick()
	if n := len(g.ships.requests()); n != 0 {
		t.Fatalf("%d launches while the child is blocked, want none", n)
	}
	g.camp.set(readOf(ready(21)))
	g.tick()
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launches after the read made it ready, want 1", n)
	}
	if g.children(c.ID)[issue(21)].State != ledger.ChildLaunched {
		t.Errorf("children = %+v; want 21 launched", g.children(c.ID))
	}
}

func TestTwoReadyChildrenLaunchTwoShipsWithTwoCards(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(ready(21), ready(22)))
	g.tick()
	if n := len(g.ships.requests()); n != 2 {
		t.Fatalf("%d launches, want 2", n)
	}
	cards := g.cardsOf(c.ID, commitments.OutcomeLaunched)
	if len(cards) != 2 || cards[0].ID == cards[1].ID || cards[0].Child == cards[1].Child {
		t.Errorf("launched cards %+v; want two with distinct ids and children", cards)
	}
	if got, _ := g.find(c.ID); got.Launched != 2 {
		t.Errorf("commitment = %+v; want 2 launched", got)
	}
}

func TestAChildListedTwiceInOneReadLaunchesOnce(t *testing.T) {
	g := newRig(t)
	g.work("claude")
	one := ready(21)
	r := readOf(one)
	r.Campaign.Ready = append(r.Campaign.Ready, one)
	g.camp.set(r)
	g.tick()
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launches for one child listed twice, want 1", n)
	}
}

func TestAChildThatMustWaitIsMarkedWaitingOnceRetriedAndLaunchedWhenRoomAppears(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.ships.launch = func(n int, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
		if n <= 2 {
			return commitments.ShipRef{}, refusal(409, "the fleet is at its cap", "cap")
		}
		return commitments.ShipRef{Session: "ship-late", Name: "Late"}, nil
	}
	g.camp.set(readOf(ready(21)))
	g.tick()
	g.tick()
	if got, _ := g.find(c.ID); got.WaitingChildren != 1 || g.children(c.ID)[issue(21)].State != ledger.ChildWaiting {
		t.Fatalf("after two refusals: %+v; want the child waiting", got)
	}
	if n := strings.Count(g.ledgerText(), ledger.EventChildWaiting); n != 1 {
		t.Errorf("%d child_waiting events, want the one", n)
	}
	if n := len(g.cards.cards()); n != 0 {
		t.Errorf("%d cards %+v while waiting, want none", n, g.cards.cards())
	}
	g.tick()
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildLaunched || ch.Ship != "ship-late" {
		t.Errorf("child = %+v; want launched on ship-late", ch)
	}
	if got, _ := g.find(c.ID); got.WaitingChildren != 0 || got.Launched != 1 {
		t.Errorf("commitment = %+v; want 1 launched and none waiting", got)
	}
}

func TestAWaitingChildArmsTheNextPassAThirtySecondsOut(t *testing.T) {
	g := newRig(t)
	g.work("claude")
	g.ships.launch = func(int, launcher.Request) (commitments.ShipRef, *launcher.Error) {
		return commitments.ShipRef{}, refusal(409, "the fleet is at its cap", "cap")
	}
	g.camp.set(readOf(ready(21)))
	g.startRun()
	waitFor(t, "a pass 30s after the first, for the waiting child", func() bool { return g.clk.hasDeadline(t0.Add(30 * time.Second)) })
}

func TestAGateRefusalPostsCantRunWithItsMessageAndTheChildIsNeverRetried(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	msg := "autopilot is not proven for claude in this fleet"
	g.ships.launch = func(n int, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
		if strings.HasSuffix(req.OrderID, "/"+issue(21)) {
			return commitments.ShipRef{}, refusal(422, msg, "")
		}
		return commitments.ShipRef{Session: "ship-ok"}, nil
	}
	g.camp.set(readOf(ready(21), ready(22)))
	g.tick()
	g.tick()
	g.tick()
	cards := g.cardsOf(c.ID, commitments.OutcomeCantRun)
	if len(cards) != 1 || cards[0].Why != msg || cards[0].Child != issue(21) {
		t.Fatalf("can't-run cards %+v; want one for %s whose Why is %q", cards, issue(21), msg)
	}
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildFailed || ch.Reason != msg {
		t.Errorf("refused child = %+v; want failed with the refusal's message", ch)
	}
	if n := len(g.ships.requests()); n != 2 {
		t.Errorf("%d launches over three passes, want 2: the refused child once, the other once", n)
	}
	if g.children(c.ID)[issue(22)].State != ledger.ChildLaunched {
		t.Errorf("the other child = %+v; want it launched despite the refusal", g.children(c.ID)[issue(22)])
	}
}

func TestALaunchedChildIsNotRelaunchedWhileItsIssueIsStillOpenEvenAfterARestart(t *testing.T) {
	g := newRig(t)
	g.work("claude")
	mixed := "https://github.com/Acme/Widgets/issues/21"
	g.camp.set(readOf(child(mixed, campaigns.WithoutYou, campaigns.Ready)))
	g.tick()
	g.restart()
	g.tick()
	g.tick()
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launches of a child whose ship anchored with the issue open, want 1", n)
	}
	cs := g.open()
	if len(cs) != 1 || len(cs[0].Children) != 1 || cs[0].Children[0].URL != mixed {
		t.Errorf("stored children %+v; want the one URL exactly as the planner returned it, %s", cs, mixed)
	}
}

func TestAShipThatAlreadyHoldsTheChildsKeyIsRecordedAndNotLaunchedAgain(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.ships.hold("commitment:"+c.ID+":"+issue(21), commitments.ShipRef{Session: "ship-held", Name: "Held"})
	g.camp.set(readOf(ready(21)))
	g.tick()
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches, want none: a ship already holds the key", n)
	}
	if ch := g.children(c.ID)[issue(21)]; ch.State != ledger.ChildLaunched || ch.Ship != "ship-held" {
		t.Errorf("child = %+v; want launched on ship-held", ch)
	}
}

func TestACrossRepoChildLaunchesIntoItsOwnRepoNotTheParentsWorkRepo(t *testing.T) {
	g := newRig(t)
	g.work("claude")
	other := child("https://github.com/acme/other/issues/40", campaigns.WithoutYou, campaigns.Ready)
	other.Repo = "acme/other"
	r := readOf(other)
	r.Orders[other.URL] = campaigns.Order{Text: "order", CloneRepo: "github.com/acme/other"}
	g.camp.set(r)
	g.tick()
	calls := g.ships.requests()
	if len(calls) != 1 || calls[0].req.CloneRepo != "github.com/acme/other" {
		t.Errorf("launches %+v; want one cloning github.com/acme/other", calls)
	}
}

func TestAChildWhoseIssuesAllClosedEndsTheCommitmentWithOneCard(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(closedRead(child(issue(21), campaigns.WithoutYou, campaigns.Done)))
	g.tick()
	g.tick()
	card := g.wantCard(c.ID, commitments.OutcomeEnded)
	if card.Why != "the campaign ended" {
		t.Errorf("ended card Why = %q; want the campaign ended", card.Why)
	}
	if n := len(g.cards.cards()); n != 1 {
		t.Errorf("%d cards %+v, want the one ended card", n, g.cards.cards())
	}
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches at the end, want none", n)
	}
}

func TestStoppingTheFollowEndsTheCommitmentWithOneCard(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	r := readOf(ready(21))
	r.Followed = false
	g.camp.set(r)
	g.tick()
	g.tick()
	if card := g.wantCard(c.ID, commitments.OutcomeEnded); card.Why != "stopped following" {
		t.Errorf("ended card Why = %q; want stopped following", card.Why)
	}
	if n := len(g.cards.cards()) + len(g.ships.requests()); n != 1 {
		t.Errorf("%d cards and launches in all, want the one card", n)
	}
}

func TestACampaignMissingFromTheFollowStoreEndsTheCommitmentAsStoppedFollowing(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(campaigns.Read{ID: campID})
	g.tick()
	if card := g.wantCard(c.ID, commitments.OutcomeEnded); card.Why != "stopped following" {
		t.Errorf("ended card Why = %q; want stopped following", card.Why)
	}
}

func TestACancelledCommitmentLaunchesNothingMore(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(ready(21)))
	g.tick()
	if n := len(g.ships.requests()); n != 1 {
		t.Fatalf("%d launches before the cancel, want 1", n)
	}
	if err := g.led.CancelCommitment(fleet, c.ID); err != nil {
		t.Fatal(err)
	}
	g.camp.set(readOf(ready(21), ready(22)))
	g.tick()
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launches after the cancel, want still 1", n)
	}
}

func TestACancelThatLandsDuringTheLaunchStopsItThroughTheBeforeCheck(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	var before, after error
	called := false
	g.ships.launch = func(n int, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
		called = true
		before = req.Before("/work/repo")
		if err := g.led.CancelCommitment(fleet, c.ID); err != nil {
			t.Error(err)
		}
		after = req.Before("/work/repo")
		return commitments.ShipRef{}, refusal(409, "the commitment was cancelled", "")
	}
	g.camp.set(readOf(ready(21)))
	g.tick()
	if !called {
		t.Fatal("the child was never launched")
	}
	if before != nil || after == nil {
		t.Errorf("Before gave %v then %v around the cancel; want nil then an error", before, after)
	}
	if n := len(g.cards.cards()); n != 0 {
		t.Errorf("%d cards %+v for a cancelled launch, want none", n, g.cards.cards())
	}
}

func TestAWithYouChildIsNeverLaunchedOrProposedWhileAWithoutYouOneLaunches(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	g.camp.set(readOf(child(issue(21), campaigns.WithYou, campaigns.Ready), ready(22)))
	g.tick()
	g.tick()
	calls := g.ships.requests()
	if len(calls) != 1 || !strings.HasSuffix(calls[0].req.OrderID, "/"+issue(22)) {
		t.Fatalf("launches %+v; want only the Without-you child, 22", calls)
	}
	if n := len(g.cards.cards()); n != 1 {
		t.Errorf("%d cards %+v, want only 22's launched card: nothing is proposed", n, g.cards.cards())
	}
	if got := g.children(c.ID); len(got) != 1 {
		t.Errorf("children = %+v; want only 22", got)
	}
}

func TestACommitmentAtTheChildEventBoundLaunchesNothingMoreAndPostsOneTooLargeCard(t *testing.T) {
	g := newRig(t)
	c := g.work("claude")
	var b strings.Builder
	for i := 0; i < ledger.MaxChildEvents; i++ {
		fmt.Fprintf(&b, `{"v":1,"ts":%q,"event":"child_launched","id":%q,"url":%q,"ship":"ship"}`+"\n", stamp(t0), c.ID, issue(1000+i))
	}
	g.appendRaw(b.String())
	g.camp.set(readOf(ready(21)))
	g.tick()
	g.tick()
	g.tick()
	if n := len(g.ships.requests()); n != 0 {
		t.Errorf("%d launches past the bound, want none", n)
	}
	if cards := g.cardsOf(c.ID, commitments.OutcomeTooLarge); len(cards) != 1 || cards[0].Why == "" {
		t.Errorf("too-large cards %+v; want exactly one that says why", cards)
	}
	if _, ok := g.find(c.ID); !ok {
		t.Error("the commitment closed at the bound, want it open until the campaign ends or the engineer cancels")
	}
}

func TestAPassThatChangesNothingDoesNotKeepTheRunnerWakingItself(t *testing.T) {
	g := newRig(t)
	g.led.OnChange = func() { g.r.Changed() }
	g.work("claude")
	g.camp.set(readOf(ready(21)))
	g.startRun()
	waitFor(t, "the child to launch", func() bool { return len(g.ships.requests()) == 1 })
	time.Sleep(100 * time.Millisecond)
	before := g.camp.count()
	time.Sleep(200 * time.Millisecond)
	if after := g.camp.count(); after != before {
		t.Errorf("the campaign was read %d more times with nothing changing, want a quiet runner", after-before)
	}
}
