package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/ships"
)

// A local-only ship has no PR, so its card says it is done and names its branch.

func localShip(name string) ships.Ship {
	s := ship(name, "work", ships.StatusUnderway)
	s.LocalOnly, s.Branch = true, "admiral/"+name
	return s
}

func TestALocalOnlyShipAdmiralEndedAsFinishedPostsOneDoneCardNamingItsBranch(t *testing.T) {
	t.Parallel()
	x := newDetector(t)
	x.observe(0, []ships.Ship{localShip("calm-otter")})
	finished := ShipRecord{At: t0.Add(time.Minute), Ship: "calm-otter", Event: "finished"}
	x.observe(2*time.Minute, nil, finished)
	x.observe(4*time.Minute, nil, finished)
	x.restart()
	x.observe(5*time.Minute, nil, finished)
	x.wantResults("work", "done")
	if cards := shipCardsIn(t, x.r, "work"); len(cards) == 1 && cards[0].Branch != "admiral/calm-otter" {
		t.Errorf("done card branch = %q, want admiral/calm-otter", cards[0].Branch)
	}
}

func TestAFinishedShipsKilledEndedRecordAddsNoCardToItsDoneCard(t *testing.T) {
	t.Parallel()
	x := newReporting(t)
	s := localShip("calm-otter")
	s.Via = "admiralbot"
	x.observe(time.Minute, []ships.Ship{launchedBy(s, "chat:m1")})
	finished := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "finished"}
	ended := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", ReportResult: "done"}
	x.observe(5*time.Minute, nil, finished, ended)
	x.observe(6*time.Minute, nil, finished, ended)
	x.want("calm-otter:done")
	x.wantResults("work", "done")
}

func TestACleanEndAfterADoneReportOnALocalOnlyShipPostsTheDoneCardNotSailedOff(t *testing.T) {
	t.Parallel()
	x := newDetector(t)
	x.observe(0, []ships.Ship{localShip("calm-otter")})
	x.observe(5*time.Minute, nil, ShipRecord{At: t0.Add(time.Minute), Ship: "calm-otter", Event: "ended", Clean: true, ReportResult: "done", Result: "done"})
	x.wantResults("work", "done")
	if cards := shipCardsIn(t, x.r, "work"); len(cards) == 1 && cards[0].Branch != "admiral/calm-otter" {
		t.Errorf("done card branch = %q, want admiral/calm-otter", cards[0].Branch)
	}
}

func TestACleanEndOfAShipThatIsNotLocalOnlyOrReportedNoDoneStillSailsOff(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		snap   ships.Ship
		report string
	}{
		"a GitHub ship that reported done":        {ship("calm-otter", "work", ships.StatusUnderway), "done"},
		"a local-only ship that reported partial": {localShip("calm-otter"), "partial"},
		"a local-only ship with no report":        {localShip("calm-otter"), ""},
	} {
		t.Run(name, func(t *testing.T) {
			x := newDetector(t)
			x.observe(0, []ships.Ship{c.snap})
			x.observe(5*time.Minute, nil, ShipRecord{At: t0.Add(time.Minute), Ship: "calm-otter", Event: "ended", Clean: true, ReportResult: c.report})
			x.wantResults("work", "sailed_off")
		})
	}
}

func TestALocalOnlyShipWithNoWorktreeBranchPostsADoneCardWithNoBranch(t *testing.T) {
	t.Parallel()
	x := newDetector(t)
	s := localShip("calm-otter")
	s.Branch = ""
	x.observe(0, []ships.Ship{s})
	x.observe(2*time.Minute, nil, ShipRecord{At: t0.Add(time.Minute), Ship: "calm-otter", Event: "finished"})
	x.wantResults("work", "done")
	if cards := shipCardsIn(t, x.r, "work"); len(cards) == 1 && cards[0].Branch != "" {
		t.Errorf("done card branch = %q, want none", cards[0].Branch)
	}
}

func TestADoneShipAdmiralBotLaunchedQueuesOneReportTurn(t *testing.T) {
	t.Parallel()
	x := newReporting(t)
	s := localShip("calm-otter")
	s.Via = "admiralbot"
	x.observe(time.Minute, []ships.Ship{launchedBy(s, "chat:m1")})
	finished := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "finished"}
	x.observe(5*time.Minute, nil, finished)
	x.observe(6*time.Minute, nil, finished)
	x.want("calm-otter:done")
}

func TestAReportTurnForADoneShipNamesItsBranchAndDoesNotAskForAPR(t *testing.T) {
	t.Parallel()
	c := sampleCard()
	c.Result, c.PRs, c.Branch = "done", nil, "admiral/calm-otter"
	text := ReportTurnText(c, "launch text", "done", false)
	if !strings.Contains(text, "admiral/calm-otter") {
		t.Errorf("report turn = %q, want it to name the branch", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "Say the ship is done only when") && !strings.Contains(strings.ToLower(line), "branch") {
			t.Errorf("closing rule = %q, want it to say a done ship in a local-only checkout has a commit on its branch", line)
		}
	}
}

func TestADoneShipCardPostsWithItsBranch(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	c := sampleShipCard()
	c.Result, c.Branch = "done", "admiral/calm-otter"
	if err := r.PostShipCard("work", c); err != nil {
		t.Fatalf("PostShipCard = %v", err)
	}
	cards := shipCardsIn(t, r, "work")
	if len(cards) != 1 || cards[0].Result != "done" || cards[0].Branch != "admiral/calm-otter" {
		t.Errorf("posted cards = %+v, want one done card on branch admiral/calm-otter", cards)
	}
}

func TestPostedShipCardsCarryADoneCardsBranch(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	before := time.Now().Add(-time.Hour)
	if err := r.PostShipCard("work", ShipCard{Ship: "s-dory", Name: "dory", FirstLaunched: "v1", Result: "done", Branch: "admiral/s-dory"}); err != nil {
		t.Fatal(err)
	}
	got := r.PostedShipCards("work", before)
	if len(got) != 1 || got[0].Result != "done" || got[0].Branch != "admiral/s-dory" {
		t.Errorf("cards = %+v, want dory's done card on branch admiral/s-dory", got)
	}
}
