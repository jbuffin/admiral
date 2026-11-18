package chat

import (
	"slices"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/ships"
)

var t0 = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

// detector is a ShipCards over the rig's home; restart makes a new one over the same home, as a server restart does.
type detector struct {
	t *testing.T
	r *briefRig
	d *ShipCards
}

func newDetector(t *testing.T) *detector {
	r := newBriefRig(t)
	return &detector{t: t, r: r, d: NewShipCards(r.home, r.PostShipCard)}
}

func (x *detector) restart() { x.d = NewShipCards(x.r.home, x.r.PostShipCard) }

func (x *detector) observe(at time.Duration, snap []ships.Ship, recs ...ShipRecord) {
	x.t.Helper()
	if err := x.d.Observe(snap, recs, t0.Add(at)); err != nil {
		x.t.Fatalf("Observe = %v", err)
	}
}

// results is the results of the ship cards in the fleet's chat, in order.
func (x *detector) results(fleet string) []string {
	x.t.Helper()
	var out []string
	for _, c := range shipCardsIn(x.t, x.r, fleet) {
		out = append(out, c.Result)
	}
	return out
}

func (x *detector) wantResults(fleet string, want ...string) {
	x.t.Helper()
	if got := x.results(fleet); !slices.Equal(got, want) {
		x.t.Errorf("%s chat ship cards = %v, want %v", fleet, got, want)
	}
}

func ship(name, fleet, status string) ships.Ship {
	skill := "tdd"
	return ships.Ship{Session: name, Name: name, Fleet: fleet, Skill: &skill, SkillKind: "skill", Status: status,
		Launched: "2026-11-17T10:00:00Z", FirstLaunched: "2026-11-17T10:00:00Z"}
}

func moored(name, fleet string, prs ...string) ships.Ship {
	s := ship(name, fleet, ships.StatusMoored)
	for _, u := range prs {
		s.PRs = append(s.PRs, moor.PR{URL: u, State: "OPEN"})
	}
	return s
}

func viaBot(s ships.Ship) ships.Ship { s.Via = "admiralbot"; return s }

func ready(s ships.Ship) ships.Ship {
	s.StackReady, s.PolledAt = true, t0.Format(time.RFC3339)
	return s
}

const pr7 = "https://github.com/acme/api/pull/7"

// seeded makes the first pass over a home with an unrelated ship underway, so later passes are not the seed.
func seeded(t *testing.T) *detector {
	x := newDetector(t)
	x.observe(0, []ships.Ship{ship("idle-gull", "home", ships.StatusUnderway)})
	return x
}

func TestAShipThatFirstMoorsWithAnOpenPRPostsOneMooredCardInItsFleetOnly(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)})
	x.wantResults("work", "moored")
	x.wantResults("home")
	c := shipCardsIn(t, x.r, "work")[0]
	if c.Ship != "calm-otter" || c.Skill != "tdd" || c.SkillKind != "skill" || len(c.PRs) != 1 || c.PRs[0].URL != pr7 || c.PRs[0].Number != 7 {
		t.Errorf("card = %+v, want the ship, its skill and kind and PR 7 with its number read from the URL", c)
	}
}

func TestOnlyTheFirstMoorPostsNotAReMoorOrAnotherPassOrARestart(t *testing.T) {
	x := seeded(t)
	m := moored("calm-otter", "work", pr7)
	x.observe(time.Minute, []ships.Ship{m})
	x.observe(2*time.Minute, []ships.Ship{m})
	// A feedback round wakes the ship and it moors again: Launched moves, FirstLaunched does not.
	woken := ship("calm-otter", "work", ships.StatusUnderway)
	woken.Launched = "2026-11-17T10:05:00Z"
	x.observe(3*time.Minute, []ships.Ship{woken})
	again := m
	again.Launched = "2026-11-17T10:05:00Z"
	x.observe(4*time.Minute, []ships.Ship{again}, ShipRecord{At: t0.Add(4 * time.Minute), Ship: "calm-otter", Event: "moored"})
	x.restart()
	x.observe(5*time.Minute, []ships.Ship{again})
	x.wantResults("work", "moored")
}

func TestAShipThatEndsWithAnErrorPostsOneAnchoredCardInItsFleetOnly(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	a := ship("calm-otter", "work", ships.StatusAnchored)
	x.observe(2*time.Minute, []ships.Ship{a})
	x.observe(3*time.Minute, []ships.Ship{a})
	x.restart()
	x.observe(4*time.Minute, []ships.Ship{a})
	x.wantResults("work", "anchored")
	x.wantResults("home")
}

func TestALiveShipThatEndsCleanWithNoMoorPostsOneSailedOffCardInItsFleetOnly(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	end := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true}
	x.observe(5*time.Minute, nil, end)
	x.observe(6*time.Minute, nil, end)
	x.restart()
	x.observe(7*time.Minute, nil, end)
	x.wantResults("work", "sailed_off")
	x.wantResults("home")
}

func TestACleanEndThatMoorsAfterwardsPostsMooredAndNotSailedOff(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	x.observe(5*time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)},
		ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true},
		ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "moored"})
	x.wantResults("work", "moored")
}

func TestAMooredShipWhosePRsAllClosedPostsOneSailedOffCard(t *testing.T) {
	for _, reason := range []string{"merged", "closed", "prs_done"} {
		t.Run(reason, func(t *testing.T) {
			x := seeded(t)
			x.observe(time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)})
			gone := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "unmoored", Reason: reason}
			x.observe(3*time.Minute, nil, gone)
			x.observe(4*time.Minute, nil, gone)
			x.restart()
			x.observe(5*time.Minute, nil, gone)
			x.wantResults("work", "moored", "sailed_off")
			x.wantResults("home")
		})
	}
}

func TestAMooredShipUnmooredByTheEngineerPostsNoSailedOffCard(t *testing.T) {
	for _, reason := range []string{"dismissed", "unfollowed", "stopped"} {
		t.Run(reason, func(t *testing.T) {
			x := seeded(t)
			x.observe(time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)})
			x.observe(3*time.Minute, nil, ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "unmoored", Reason: reason})
			x.wantResults("work", "moored")
		})
	}
}

func TestAShipThatReturnsToPortPostsOneReturnedCardWhoeverLaunchedIt(t *testing.T) {
	for name, s := range map[string]ships.Ship{"engineer": moored("calm-otter", "work", pr7), "admiralbot": viaBot(moored("calm-otter", "work", pr7))} {
		t.Run(name, func(t *testing.T) {
			x := seeded(t)
			x.observe(time.Minute, []ships.Ship{s})
			back := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "stopped", Returned: true}
			x.observe(3*time.Minute, nil, back)
			x.observe(4*time.Minute, nil, back)
			x.restart()
			x.observe(5*time.Minute, nil, back)
			x.wantResults("work", "moored", "returned")
			x.wantResults("home")
		})
	}
}

func TestStopShipPostsNoCard(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	x.observe(3*time.Minute, nil, ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "stopped"})
	x.wantResults("work")
}

func TestReadyToMergePostsOnlyForAShipAdmiralBotLaunchedAndOncePerVoyage(t *testing.T) {
	x := seeded(t)
	bot := viaBot(moored("calm-otter", "work", pr7))
	hand := moored("brisk-finch", "work", "https://github.com/acme/api/pull/9")
	x.observe(time.Minute, []ships.Ship{bot, hand})
	x.wantResults("work", "moored", "moored")
	x.observe(2*time.Minute, []ships.Ship{ready(bot), ready(hand)})
	x.wantResults("work", "moored", "moored", "ready_to_merge")
	if c := shipCardsIn(t, x.r, "work")[2]; c.Ship != "calm-otter" || c.Result != "ready_to_merge" {
		t.Errorf("the ready card is %+v, want the AdmiralBot ship's", c)
	}
	// Ready goes away and comes back, and the server restarts: still one.
	x.observe(3*time.Minute, []ships.Ship{bot, hand})
	x.restart()
	x.observe(4*time.Minute, []ships.Ship{ready(bot), ready(hand)})
	x.wantResults("work", "moored", "moored", "ready_to_merge")
}

func TestAShipNotYetPolledIsNotReadyToMerge(t *testing.T) {
	x := seeded(t)
	m := viaBot(moored("calm-otter", "work", pr7))
	m.StackReady = true // a restart leaves it true-looking until the first poll answers; PolledAt is still empty
	x.observe(time.Minute, []ships.Ship{m})
	x.wantResults("work", "moored")
}

func TestAReusedShipNameWithANewFirstLaunchedPostsItsOwnCards(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)})
	x.observe(3*time.Minute, nil, ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "unmoored", Reason: "merged"})
	again := moored("calm-otter", "work", "https://github.com/acme/api/pull/12")
	again.Launched, again.FirstLaunched = "2026-11-18T09:00:00Z", "2026-11-18T09:00:00Z"
	x.restart()
	x.observe(time.Hour, []ships.Ship{again})
	x.wantResults("work", "moored", "sailed_off", "moored")
}

func TestShipsAlreadyMooredOrAnchoredOnTheFirstPassPostNothingAndLaterEventsStillPostOnceAfterRestarts(t *testing.T) {
	x := newDetector(t)
	old := []ships.Ship{moored("old-mooring", "work", pr7), ship("old-anchor", "work", ships.StatusAnchored), ready(viaBot(moored("old-ready", "home", pr7)))}
	x.observe(0, old)
	x.wantResults("work")
	x.wantResults("home")

	x.restart()
	x.observe(time.Minute, old)
	x.wantResults("work")
	x.wantResults("home")

	fresh := moored("new-mooring", "work", "https://github.com/acme/api/pull/20")
	x.observe(2*time.Minute, append(slices.Clone(old), fresh))
	x.restart()
	x.observe(3*time.Minute, append(slices.Clone(old), fresh))
	x.wantResults("work", "moored")
	x.wantResults("home")

	// An old ship's later end still posts: the seed holds back only what was already true.
	x.observe(4*time.Minute, []ships.Ship{fresh, old[1], old[2]}, ShipRecord{At: t0.Add(4 * time.Minute), Ship: "old-mooring", Event: "unmoored", Reason: "merged"})
	x.wantResults("work", "moored", "sailed_off")
}

func TestARecordOfAShipTheDetectorNeverSawPostsNothing(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, nil, ShipRecord{At: t0, Ship: "ghost", Event: "ended", Clean: true})
	x.wantResults("work")
	x.wantResults("home")
}

func TestAMergedShipReturnedToPortPostsOneReturnedCardAndNoSailedOffCard(t *testing.T) {
	at := t0.Add(2 * time.Minute)
	unmoored := ShipRecord{At: at, Ship: "calm-otter", Event: "unmoored", Reason: "merged"}
	returning := ShipRecord{At: at, Ship: "calm-otter", Event: "returning"}
	stopped := ShipRecord{At: at.Add(30 * time.Second), Ship: "calm-otter", Event: "stopped", Returned: true}
	t.Run("one batch", func(t *testing.T) {
		x := seeded(t)
		x.observe(time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)})
		x.observe(5*time.Minute, nil, unmoored, returning, stopped)
		x.wantResults("work", "moored", "returned")
	})
	t.Run("separate passes", func(t *testing.T) {
		x := seeded(t)
		x.observe(time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)})
		x.observe(3*time.Minute, nil, unmoored, returning)
		x.wantResults("work", "moored")
		x.observe(5*time.Minute, nil, unmoored, returning, stopped)
		x.restart()
		x.observe(6*time.Minute, nil, unmoored, returning, stopped)
		x.wantResults("work", "moored", "returned")
	})
}

func TestTheEngineersReturnOfALiveShipPostsOneReturnedCard(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	returning := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "returning"}
	stopped := ShipRecord{At: t0.Add(3 * time.Minute), Ship: "calm-otter", Event: "stopped", Returned: true}
	x.observe(150*time.Second, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)}, returning)
	x.wantResults("work")
	x.observe(4*time.Minute, nil, returning, stopped)
	x.observe(5*time.Minute, nil, returning, stopped)
	x.wantResults("work", "returned")
}

func TestStopShipAfterACleanEndPostsNoSailedOffCard(t *testing.T) {
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	end := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true}
	x.observe(5*time.Minute, nil, end, ShipRecord{At: t0.Add(2*time.Minute + time.Second), Ship: "calm-otter", Event: "stopped"})
	x.wantResults("work")
}
