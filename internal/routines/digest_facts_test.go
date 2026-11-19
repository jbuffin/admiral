package routines_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/routines"
)

// What a fleet's fact sheet holds for a window (from, to], read through the narrow inputs FleetFacts takes.

var (
	winFrom = at(0, 9, 0)
	winTo   = at(1, 9, 0)
)

func inWindow() time.Time { return at(0, 20, 0) }

type factInputs struct {
	cards    []routines.CardFact
	ships    []routines.ShipFact
	inbox    []routines.InboxFact
	children []routines.ChildFact
}

func (in *factInputs) source(r *rig) *routines.FleetFacts {
	return &routines.FleetFacts{
		Store: r.store,
		Cards: func(fleet string, _ time.Time) []routines.CardFact {
			if fleet != "work" {
				return nil
			}
			return in.cards
		},
		Ships: func(fleet string) []routines.ShipFact {
			if fleet != "work" {
				return nil
			}
			return in.ships
		},
		Inbox:    func() []routines.InboxFact { return in.inbox },
		Children: func(fleet string) []routines.ChildFact { return in.children },
	}
}

func joined(sections []routines.FactSection) string {
	var all []string
	for _, s := range sections {
		all = append(all, s.Lines...)
	}
	return strings.Join(all, "\n")
}

func (in *factInputs) facts(t *testing.T) []routines.FactSection {
	t.Helper()
	got, _, err := in.source(newRig(t)).Facts("work", "r1", winFrom, winTo)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if len(s.Lines) == 0 {
			t.Errorf("section %q has no lines; an empty section is left out", s.Title)
		}
	}
	return got
}

func TestShipsThatSailedOffOrAnchoredInTheWindowAreFactsAndTheWindowIsOpenAtItsStartAndClosedAtItsEnd(t *testing.T) {
	in := &factInputs{cards: []routines.CardFact{
		{At: winFrom, Name: "at-start", Result: "sailed_off"},
		{At: winFrom.Add(time.Second), Name: "dory", Result: "sailed_off"},
		{At: inWindow(), Name: "skiff", Result: "anchored"},
		{At: winTo, Name: "at-end", Result: "anchored"},
		{At: winTo.Add(time.Second), Name: "later", Result: "sailed_off"},
		{At: winFrom.Add(-time.Hour), Name: "earlier", Result: "anchored"},
		// no source records a lost ship today, so a lost card is not a fact
		{At: inWindow(), Name: "ghost", Result: "lost"},
	}}
	got := joined(in.facts(t))
	for _, want := range []string{"dory", "skiff", "at-end"} {
		if !strings.Contains(got, want) {
			t.Errorf("facts = %q, want %q in the window", got, want)
		}
	}
	for _, not := range []string{"at-start", "later", "earlier", "ghost"} {
		if strings.Contains(got, not) {
			t.Errorf("facts = %q, want %q left out (outside the window (start, end], or a lost ship)", got, not)
		}
	}
}

func TestAShipMooredWithAPRInTheWindowShowsItsReadyToMergeStateAndMergeProblems(t *testing.T) {
	in := &factInputs{
		cards: []routines.CardFact{
			{At: inWindow(), Ship: "s-dory", Name: "dory", Result: "moored", PRs: []string{"https://github.com/o/r/pull/7"}},
			{At: inWindow(), Ship: "s-skiff", Name: "skiff", Result: "moored", PRs: []string{"https://github.com/o/r/pull/8"}},
			{At: winFrom.Add(-time.Hour), Ship: "s-old-mooring", Name: "old-mooring", Result: "moored", PRs: []string{"https://github.com/o/r/pull/3"}},
		},
		ships: []routines.ShipFact{
			{Ship: "s-dory", Moored: true, Ready: true},
			{Ship: "s-skiff", Moored: true, MergeReasons: []string{"checks are failing"}},
			{Ship: "s-old-mooring", Moored: true, Ready: true},
		},
	}
	lines := strings.Split(joined(in.facts(t)), "\n")
	var dory, skiff string
	for _, l := range lines {
		switch {
		case strings.Contains(l, "dory"):
			dory = l
		case strings.Contains(l, "skiff"):
			skiff = l
		case strings.Contains(l, "old-mooring"):
			t.Errorf("line %q: a ship moored before the window is no fact, ready or not", l)
		}
	}
	if !strings.Contains(dory, "pull/7") || !strings.Contains(strings.ToLower(dory), "ready to merge") {
		t.Errorf("dory's line = %q, want its PR and that it is ready to merge", dory)
	}
	if !strings.Contains(skiff, "pull/8") || !strings.Contains(skiff, "checks are failing") || strings.Contains(strings.ToLower(skiff), "ready to merge") {
		t.Errorf("skiff's line = %q, want its PR and its merge problem, and no ready mark", skiff)
	}
}

func TestTheInboxSectionHoldsThisFleetsCountAndOldestItemWhenOneWasCreatedInTheWindow(t *testing.T) {
	in := &factInputs{inbox: []routines.InboxFact{
		{Fleet: "work", Kind: "question", From: "Oldest-ship", Created: winFrom.Add(-72 * time.Hour)},
		{Fleet: "work", Kind: "review", From: "Fresh-ship", Created: inWindow()},
		{Fleet: "work", Kind: "question", From: "Another", Created: winFrom.Add(-time.Hour)},
		{Fleet: "play", Kind: "question", From: "Other-fleet-older", Created: winFrom.Add(-96 * time.Hour)},
		{Fleet: "play", Kind: "question", From: "Other-fleet-fresh", Created: inWindow()},
	}}
	got := joined(in.facts(t))
	if !strings.Contains(got, "Oldest-ship") || !strings.Contains(got, "3") {
		t.Errorf("facts = %q, want work's three items and its oldest, Oldest-ship", got)
	}
	if strings.Contains(got, "Other fleet") {
		t.Errorf("facts = %q, want no item from the play fleet", got)
	}

	// an Inbox that holds only older items is not a change; another fleet's fresh item is not this fleet's
	in.inbox = []routines.InboxFact{
		{Fleet: "work", Kind: "question", From: "Waiting", Created: winFrom.Add(-72 * time.Hour)},
		{Fleet: "play", Kind: "question", From: "Other-fleet-fresh", Created: inWindow()},
	}
	if got := in.facts(t); len(got) != 0 {
		t.Errorf("sections = %+v, want none: nothing was created in work's Inbox in the window", got)
	}
}

func TestCampaignChildrenThatClosedOrBecameReadyAreFactsAgainstTheSnapshotTheLastDigestStored(t *testing.T) {
	r := newRig(t)
	children := []routines.ChildFact{
		{Campaign: "c1", Child: "a", Title: "Wire the thing", Closed: false, Ready: false},
		{Campaign: "c1", Child: "b", Title: "Docs pass", Closed: false, Ready: false},
		{Campaign: "c1", Child: "c", Title: "Steady one", Closed: false, Ready: true},
	}
	in := &factInputs{children: children}
	src := in.source(r)
	facts := func(fleet string) string {
		t.Helper()
		got, commit, err := src.Facts(fleet, "r1", winFrom, winTo)
		if err != nil {
			t.Fatal(err)
		}
		commit()
		return joined(got)
	}

	if got := facts("work"); got != "" {
		t.Fatalf("first digest = %q, want no campaign facts: there is no snapshot to compare with", got)
	}
	in.children = []routines.ChildFact{
		{Campaign: "c1", Child: "a", Title: "Wire the thing", Closed: true},
		{Campaign: "c1", Child: "b", Title: "Docs pass", Ready: true},
		{Campaign: "c1", Child: "c", Title: "Steady one", Ready: true},
	}
	got := facts("work")
	if !strings.Contains(got, "Wire the thing") || !strings.Contains(got, "Docs pass") {
		t.Errorf("second digest = %q, want the child that closed and the one that became ready", got)
	}
	if strings.Contains(got, "Steady one") {
		t.Errorf("second digest = %q, want a child that was already ready left out", got)
	}
	if again := facts("work"); again != "" {
		t.Errorf("third digest = %q, want the second run's state to be the new snapshot, so nothing changed", again)
	}
	if other := facts("play"); other != "" {
		t.Errorf("play's first digest = %q, want its own snapshot, not work's", other)
	}
}

func TestAnInboxThatHoldsOnlyRoutineAndEventProposalsIsNothingNewBecauseTheRoutineRunsReportThem(t *testing.T) {
	in := &factInputs{inbox: []routines.InboxFact{
		{Fleet: "work", Kind: "routine_proposal", From: "Admiral", Created: inWindow()},
		{Fleet: "work", Kind: "event_proposal", From: "Admiral", Created: inWindow()},
	}}
	if got := in.facts(t); len(got) != 0 {
		t.Errorf("sections = %+v, want none: proposals are in the routine runs section", got)
	}
	in.inbox = append(in.inbox, routines.InboxFact{Fleet: "work", Kind: "question", From: "dory", Created: inWindow()})
	got := joined(in.facts(t))
	if !strings.Contains(got, "1 waiting") || !strings.Contains(got, "dory") {
		t.Errorf("facts = %q, want one waiting item, the question, and no proposal counted", got)
	}
}

func TestAMooredCardTakesItsReadyStateFromTheShipOfItsOwnVoyageNotAnotherThatReusedTheSession(t *testing.T) {
	in := &factInputs{
		cards: []routines.CardFact{{At: inWindow(), Ship: "s-dory", Voyage: "v1", Name: "dory", Result: "moored", PRs: []string{"https://github.com/o/r/pull/7"}}},
		ships: []routines.ShipFact{{Ship: "s-dory", Voyage: "v2", Moored: true, Ready: true}},
	}
	got := strings.ToLower(joined(in.facts(t)))
	if !strings.Contains(got, "pull/7") || strings.Contains(got, "ready to merge") {
		t.Errorf("facts = %q, want the PR and no ready mark from another voyage's ship", got)
	}
	in.ships[0].Voyage = "v1"
	if got := strings.ToLower(joined(in.facts(t))); !strings.Contains(got, "ready to merge") {
		t.Errorf("facts = %q, want the ready mark of the same voyage", got)
	}
	in.ships[0].Moored = false
	if got := strings.ToLower(joined(in.facts(t))); strings.Contains(got, "ready to merge") {
		t.Errorf("facts = %q, want no ready mark from a ship that is no longer moored", got)
	}
}

func TestTheClosingOfACampaignsLastChildIsAFactEvenThoughItsFollowEndedInTheSameRead(t *testing.T) {
	r := newRig(t)
	in := &factInputs{children: []routines.ChildFact{{Campaign: "c1", Child: "a", Title: "Last child"}}}
	src := in.source(r)
	_, commit, err := src.Facts("work", "r1", winFrom, winTo)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	in.children = []routines.ChildFact{{Campaign: "c1", Child: "a", Title: "Last child", Closed: true, Ended: inWindow()}}
	got, commit, err := src.Facts("work", "r1", winFrom, winTo)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if !strings.Contains(joined(got), "Last child closed") {
		t.Errorf("facts = %q, want the last child's closing", joined(got))
	}
	// a follow that ended before the window opened is old news
	in.children[0].Ended = winFrom.Add(-time.Hour)
	if got, _, _ := src.Facts("work", "r1", winFrom, winTo); len(got) != 0 {
		t.Errorf("sections = %+v, want none for a campaign that ended before the window", got)
	}
}

func TestChildFactsWalksNestedParentsAndReadsEachChildsState(t *testing.T) {
	root := &campaigns.Node{Parent: true, Root: true, Children: []*campaigns.Node{
		{URL: "u1", Title: "One", Closed: true},
		{Parent: true, Children: []*campaigns.Node{{URL: "u2", Title: "Two", Status: campaigns.Ready}}},
	}}
	ended := inWindow()
	got := routines.ChildFacts("c1", ended, root)
	want := []routines.ChildFact{
		{Campaign: "c1", Child: "u1", Title: "One", Closed: true, Ended: ended},
		{Campaign: "c1", Child: "u2", Title: "Two", Ready: true, Ended: ended},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("children = %+v, want %+v", got, want)
	}
	if got := routines.ChildFacts("c1", time.Time{}, nil); len(got) != 0 {
		t.Errorf("children of no tree = %+v, want none", got)
	}
}

func TestTwoDigestRoutinesInOneFleetEachSeeTheSameCampaignChangeAndAFailedRunKeepsIt(t *testing.T) {
	r := newRig(t)
	in := &factInputs{children: []routines.ChildFact{{Campaign: "c1", Child: "a", Title: "Wire it"}}}
	src := in.source(r)
	run := func(routine string, keep bool) string {
		t.Helper()
		got, commit, err := src.Facts("work", routine, winFrom, winTo)
		if err != nil {
			t.Fatal(err)
		}
		if keep {
			commit()
		}
		return joined(got)
	}
	run("daily", true)
	run("weekly", true)
	in.children = []routines.ChildFact{{Campaign: "c1", Child: "a", Title: "Wire it", Closed: true}}
	for _, routine := range []string{"daily", "weekly"} {
		if got := run(routine, routine == "weekly"); !strings.Contains(got, "Wire it closed") {
			t.Errorf("%s digest facts = %q, want the closing: each routine has its own snapshot", routine, got)
		}
	}
	if got := run("daily", true); !strings.Contains(got, "Wire it closed") {
		t.Errorf("daily digest again = %q, want the closing still there: its run was not committed", got)
	}
	if got := run("weekly", true); got != "" {
		t.Errorf("weekly digest again = %q, want nothing: its run was committed", got)
	}
}

func TestAShipDoneOnItsBranchInTheWindowIsAFactNamingTheBranchAndOutOfWindowOnesAreNot(t *testing.T) {
	in := &factInputs{cards: []routines.CardFact{
		{At: inWindow(), Ship: "s-dory", Name: "dory", Result: "done", Branch: "admiral/s-dory"},
		{At: winFrom.Add(-time.Hour), Ship: "s-old", Name: "old-skiff", Result: "done", Branch: "admiral/s-old"},
	}}
	got := joined(in.facts(t))
	if !strings.Contains(got, "dory") || !strings.Contains(got, "admiral/s-dory") || !strings.Contains(strings.ToLower(got), "done") {
		t.Errorf("facts = %q, want dory listed as done on admiral/s-dory", got)
	}
	if strings.Contains(got, "old-skiff") {
		t.Errorf("facts = %q, want no ship done before the window", got)
	}
}
