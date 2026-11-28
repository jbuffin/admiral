package chat

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	x.observe(5*time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)},
		ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true},
		ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "moored"})
	x.wantResults("work", "moored")
}

func TestAMooredShipWhosePRsAllClosedPostsOneSailedOffCard(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	x.observe(3*time.Minute, nil, ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "stopped"})
	x.wantResults("work")
}

func TestReadyToMergePostsForEveryShipOnceEachPerVoyage(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	bot := viaBot(moored("calm-otter", "work", pr7))
	hand := moored("brisk-finch", "work", "https://github.com/acme/api/pull/9")
	x.observe(time.Minute, []ships.Ship{bot, hand})
	x.wantResults("work", "moored", "moored")
	x.observe(2*time.Minute, []ships.Ship{ready(bot), ready(hand)})
	x.wantResults("work", "moored", "moored", "ready_to_merge", "ready_to_merge")
	var readied []string
	for _, c := range shipCardsIn(t, x.r, "work") {
		if c.Result == "ready_to_merge" {
			readied = append(readied, c.Ship)
		}
	}
	if !slices.Equal(readied, []string{"calm-otter", "brisk-finch"}) {
		t.Errorf("the ready cards are for %v, want the AdmiralBot ship's and the hand-launched ship's", readied)
	}
	// Ready goes away and comes back, and the server restarts: still one each.
	x.observe(3*time.Minute, []ships.Ship{bot, hand})
	x.restart()
	x.observe(4*time.Minute, []ships.Ship{ready(bot), ready(hand)})
	x.wantResults("work", "moored", "moored", "ready_to_merge", "ready_to_merge")
}

func TestAShipNotYetPolledIsNotReadyToMerge(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	m := viaBot(moored("calm-otter", "work", pr7))
	m.StackReady = true // a restart leaves it true-looking until the first poll answers; PolledAt is still empty
	x.observe(time.Minute, []ships.Ship{m})
	x.wantResults("work", "moored")
}

func TestAReusedShipNameWithANewFirstLaunchedPostsItsOwnCards(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	x.observe(4*time.Minute+ShipSettleGrace, []ships.Ship{fresh, old[1], old[2]}, ShipRecord{At: t0.Add(4 * time.Minute), Ship: "old-mooring", Event: "unmoored", Reason: "merged"})
	x.wantResults("work", "moored", "sailed_off")
}

func TestARecordOfAShipTheDetectorNeverSawPostsNothing(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, nil, ShipRecord{At: t0, Ship: "ghost", Event: "ended", Clean: true})
	x.wantResults("work")
	x.wantResults("home")
}

func TestAMergedShipReturnedToPortPostsOneReturnedCardAndNoSailedOffCard(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	end := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true}
	x.observe(5*time.Minute, nil, end, ShipRecord{At: t0.Add(2*time.Minute + time.Second), Ship: "calm-otter", Event: "stopped"})
	x.wantResults("work")
}

func TestASailOffIsNotDecidedWhileTheShipIsStillInTheSnapshotSailingOff(t *testing.T) {
	t.Parallel()
	end := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true}
	sailing := ship("calm-otter", "work", ships.StatusSailingOff)
	t.Run("a restart before settle finishes, then a moor", func(t *testing.T) {
		x := seeded(t)
		x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
		x.restart()
		x.observe(3*time.Minute, []ships.Ship{sailing}, end)
		x.wantResults("work")
		x.observe(3*time.Minute+10*time.Second, []ships.Ship{moored("calm-otter", "work", pr7)}, end,
			ShipRecord{At: t0.Add(3*time.Minute + 5*time.Second), Ship: "calm-otter", Event: "moored"})
		x.wantResults("work", "moored")
	})
	t.Run("a sail-off with no moor posts once the session leaves the snapshot", func(t *testing.T) {
		x := seeded(t)
		x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
		x.observe(3*time.Minute, []ships.Ship{sailing}, end)
		x.wantResults("work")
		x.observe(4*time.Minute, nil, end)
		x.observe(5*time.Minute, nil, end)
		x.wantResults("work", "sailed_off")
	})
	t.Run("a new voyage under the same name does not hold it", func(t *testing.T) {
		x := seeded(t)
		x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
		again := ship("calm-otter", "work", ships.StatusUnderway)
		again.Launched, again.FirstLaunched = "2026-11-17T10:03:00Z", "2026-11-17T10:03:00Z"
		x.observe(4*time.Minute, []ships.Ship{again}, end)
		x.wantResults("work", "sailed_off")
	})
}

func TestAMergedReturnWhoseUnmooredAndReturningRecordsSplitAcrossASecondPostsOnlyReturned(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{moored("calm-otter", "work", pr7)})
	unmoored := ShipRecord{At: t0.Add(2*time.Minute + 59*time.Second), Ship: "calm-otter", Event: "unmoored", Reason: "merged"}
	returning := ShipRecord{At: t0.Add(3 * time.Minute), Ship: "calm-otter", Event: "returning"}
	stopped := ShipRecord{At: t0.Add(4 * time.Minute), Ship: "calm-otter", Event: "stopped", Returned: true}
	x.observe(3*time.Minute, nil, unmoored)
	x.wantResults("work", "moored")
	x.observe(3*time.Minute+20*time.Second, nil, unmoored, returning)
	x.wantResults("work", "moored")
	x.observe(5*time.Minute, nil, unmoored, returning, stopped)
	x.wantResults("work", "moored", "returned")
}

func TestAReturnThatEndsCleanWithoutStoppingPostsSailedOff(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	returning := ShipRecord{At: t0.Add(90 * time.Second), Ship: "calm-otter", Event: "returning"}
	end := ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true}
	x.observe(5*time.Minute, nil, returning, end)
	x.wantResults("work", "sailed_off")
}

func TestAShipMooredAtTheSeedPostsNoReadyToMergeAtItsFirstAnsweredPoll(t *testing.T) {
	t.Parallel()
	x := newDetector(t)
	old := viaBot(moored("calm-otter", "work", pr7))
	x.observe(0, []ships.Ship{old})
	x.restart()
	x.observe(time.Minute, []ships.Ship{ready(old)})
	x.wantResults("work")
	fresh := viaBot(moored("new-otter", "work", "https://github.com/acme/api/pull/20"))
	x.observe(2*time.Minute, []ships.Ship{ready(old), fresh})
	x.observe(3*time.Minute, []ships.Ship{ready(old), ready(fresh)})
	x.wantResults("work", "moored", "ready_to_merge")
	if c := shipCardsIn(t, x.r, "work")[1]; c.Ship != "new-otter" {
		t.Errorf("ready card is %+v, want the ship moored after the seed", c)
	}
}

func TestACardCarriesItsVoyageAndTheShipsName(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	m := moored("calm-otter", "work", pr7)
	m.Name = "Fix the login"
	x.observe(time.Minute, []ships.Ship{m})
	got, err := x.r.history().Recent("work", 500)
	if err != nil {
		t.Fatal(err)
	}
	var cards []struct {
		Text string
		Card struct {
			Ship          string `json:"ship"`
			Name          string `json:"name"`
			FirstLaunched string `json:"first_launched"`
		}
	}
	for _, e := range got {
		if e.Kind == chatlog.KindShipCard {
			var c struct {
				Ship          string `json:"ship"`
				Name          string `json:"name"`
				FirstLaunched string `json:"first_launched"`
			}
			if err := json.Unmarshal(e.Card, &c); err != nil {
				t.Fatal(err)
			}
			cards = append(cards, struct {
				Text string
				Card struct {
					Ship          string `json:"ship"`
					Name          string `json:"name"`
					FirstLaunched string `json:"first_launched"`
				}
			}{e.Text, c})
		}
	}
	if len(cards) != 1 || cards[0].Text != "calm-otter" || cards[0].Card.Ship != "calm-otter" || cards[0].Card.Name != "Fix the login" || cards[0].Card.FirstLaunched != "2026-11-17T10:00:00Z" {
		t.Errorf("cards = %+v, want the session as text and ship, the name and the first_launched stamp", cards)
	}
}

func TestAVoyageWithNoParsableLaunchStampPostsNoCard(t *testing.T) {
	t.Parallel()
	for _, stamp := range []string{"", "yesterday"} {
		x := seeded(t)
		m := moored("calm-otter", "work", pr7)
		m.Launched, m.FirstLaunched = stamp, stamp
		x.observe(time.Minute, []ships.Ship{m})
		x.wantResults("work")
	}
}

func scout(name, fleet string) ships.Ship {
	s := ship(name, fleet, ships.StatusUnderway)
	s.Skill, s.SkillKind, s.Scout = nil, "none", true
	return s
}

// A scout's end writes a KILL `ended` and then a `stopped` marked scout; a clean `ended` is never what it writes.
func scoutEnd() []ShipRecord {
	return []ShipRecord{
		{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended"},
		{At: t0.Add(2*time.Minute + time.Second), Ship: "calm-otter", Event: "stopped", Scout: true},
	}
}

func TestAScoutThatRunsToItsEndPostsOneSailedOffCardInItsFleetOnly(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{scout("calm-otter", "work")})
	end := scoutEnd()
	x.observe(2*time.Minute+10*time.Second, nil, end...)
	x.wantResults("work")
	x.observe(5*time.Minute, nil, end...)
	x.observe(6*time.Minute, nil, end...)
	x.restart()
	x.observe(7*time.Minute, nil, end...)
	x.wantResults("work", "sailed_off")
	x.wantResults("home")
	if cards := shipCardsIn(t, x.r, "work"); len(cards) != 1 || cards[0].Ship != "calm-otter" || cards[0].SkillKind != "scout" {
		t.Errorf("cards = %+v, want the scout's own with skill_kind scout", cards)
	}
}

func TestAScoutStillInTheSnapshotPostsNoSailedOffCardYet(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{scout("calm-otter", "work")})
	x.observe(5*time.Minute, []ships.Ship{scout("calm-otter", "work")}, scoutEnd()...)
	x.wantResults("work")
	x.observe(6*time.Minute, nil, scoutEnd()...)
	x.observe(7*time.Minute, nil, scoutEnd()...)
	x.wantResults("work", "sailed_off")
}

func TestAScoutStopAloneMakesTheCardAScoutsEvenWhenTheShipWasNeverListedAsOne(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	x.observe(5*time.Minute, nil, scoutEnd()...)
	if cards := shipCardsIn(t, x.r, "work"); len(cards) != 1 || cards[0].SkillKind != "scout" {
		t.Errorf("cards = %+v, want one with skill_kind scout", cards)
	}
}

func TestAScoutStopAnOlderBuildSavedPostsNothingAfterTheUpgradeButALaterOneStillPosts(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{scout("calm-otter", "work")})
	x.observe(2*time.Minute, []ships.Ship{scout("calm-otter", "work")})
	old, err := os.ReadFile(x.d.path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(old, &state); err != nil {
		t.Fatal(err)
	}
	delete(state, "Scouts")
	if old, err = json.Marshal(state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(x.d.path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	x.restart()
	x.observe(5*time.Minute, nil, scoutEnd()...)
	x.observe(6*time.Minute, nil, scoutEnd()...)
	x.wantResults("work")
	later := ship("calm-lark", "work", ships.StatusUnderway)
	later.Scout = true
	x.observe(7*time.Minute, []ships.Ship{later})
	x.observe(10*time.Minute, nil, append(scoutEnd(), ShipRecord{At: t0.Add(8 * time.Minute), Ship: "calm-lark", Event: "stopped", Scout: true})...)
	x.wantResults("work", "sailed_off")
}

func TestStopShipOfALiveShipPostsNoCardAlthoughItsKilledEndIsLogged(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{ship("calm-otter", "work", ships.StatusUnderway)})
	x.observe(5*time.Minute, nil,
		ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended"},
		ShipRecord{At: t0.Add(2*time.Minute + time.Second), Ship: "calm-otter", Event: "stopped"})
	x.wantResults("work")
}

func TestTheKindOfAMooredShipsCardIsDecidedOnTheServer(t *testing.T) {
	t.Parallel()
	noSkill := func(s ships.Ship, kind string) ships.Ship { s.Skill, s.SkillKind = nil, kind; return s }
	for name, tc := range map[string]struct {
		ship ships.Ship
		want string
	}{
		"an AdmiralBot ship with no skill is a quick ship":               {viaBot(noSkill(moored("calm-otter", "work", pr7), "")), "quick"},
		"an AdmiralBot ship whose skill kind reads none is a quick ship": {viaBot(noSkill(moored("calm-otter", "work", pr7), "none")), "quick"},
		"an AdmiralBot ship with a skill keeps its skill":                {viaBot(moored("calm-otter", "work", pr7)), "skill"},
		"a Commodore ship is Commodore, even launched by AdmiralBot":     {viaBot(noSkill(moored("calm-otter", "work", pr7), "commodore")), "commodore"},
		"a Commodore ship the engineer launched is Commodore":            {noSkill(moored("calm-otter", "work", pr7), "commodore"), "commodore"},
		"an engineer's ship with no skill has none":                      {noSkill(moored("calm-otter", "work", pr7), ""), "none"},
	} {
		t.Run(name, func(t *testing.T) {
			x := seeded(t)
			x.observe(time.Minute, []ships.Ship{tc.ship})
			got := shipCardsIn(t, x.r, "work")
			if len(got) != 1 || got[0].SkillKind != tc.want {
				t.Errorf("cards = %+v, want one with skill_kind %q", got, tc.want)
			}
		})
	}
}

func TestTheCardOfAShipWithASettledTitleCarriesItAndAShipNotYetTitledCarriesNone(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	titled := moored("calm-otter", "work", pr7)
	titled.Name, titled.Title, titled.Titled = "Calm Otter", "Fix the login redirect", true
	plain := moored("brisk-heron", "work", "https://github.com/acme/api/pull/8")
	plain.Name = "Brisk Heron"
	// moored before the Titler named it: its title is still its prompt's first line
	plain.Title = "Read the logs and fix the 500."
	x.observe(time.Minute, []ships.Ship{titled, plain})
	byShip := map[string]shipCardView{}
	for _, c := range shipCardsIn(t, x.r, "work") {
		byShip[c.Ship] = c
	}
	if c := byShip["calm-otter"]; c.Title != "Fix the login redirect" {
		t.Errorf("titled ship's card = %+v, want its title", c)
	}
	if c := byShip["brisk-heron"]; c.Title != "" {
		t.Errorf("untitled ship's card = %+v, want no title", c)
	}
}

// A scout without findings: its `ended` is a kill that carries result failed, and its `stopped` follows.
func failedScoutEnd() []ShipRecord {
	return []ShipRecord{
		{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Result: "failed"},
		{At: t0.Add(2*time.Minute + time.Second), Ship: "calm-otter", Event: "stopped", Scout: true},
	}
}

func TestAScoutThatLeftNoFindingsPostsOneFailedCardAndNoSailedOffCard(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{scout("calm-otter", "work")})
	end := failedScoutEnd()
	x.observe(2*time.Minute+10*time.Second, nil, end...)
	x.wantResults("work")
	x.observe(5*time.Minute, nil, end...)
	x.observe(6*time.Minute, nil, end...)
	x.restart()
	x.observe(7*time.Minute, nil, end...)
	x.wantResults("work", "failed")
	x.wantResults("home")
}

func TestAScoutThatLeftNoFindingsAndExitedCleanOnItsOwnPostsFailedNotSailedOff(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{scout("calm-otter", "work")})
	x.observe(5*time.Minute, nil, ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true, Result: "failed"})
	x.observe(6*time.Minute, nil, ShipRecord{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended", Clean: true, Result: "failed"})
	x.wantResults("work", "failed")
}

func TestAFailedScoutStillInTheSnapshotPostsNoFailedCardYet(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{scout("calm-otter", "work")})
	x.observe(5*time.Minute, []ships.Ship{scout("calm-otter", "work")}, failedScoutEnd()...)
	x.wantResults("work")
	x.observe(6*time.Minute, nil, failedScoutEnd()...)
	x.observe(7*time.Minute, nil, failedScoutEnd()...)
	x.wantResults("work", "failed")
}

func TestAFailedScoutStopAnOlderBuildSavedPostsNoLateFailedCardButALaterOneStillPosts(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{scout("calm-otter", "work")})
	x.observe(2*time.Minute, []ships.Ship{scout("calm-otter", "work")})
	old, err := os.ReadFile(x.d.path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(old, &state); err != nil {
		t.Fatal(err)
	}
	delete(state, "Scouts")
	if old, err = json.Marshal(state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(x.d.path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	x.restart()
	x.observe(5*time.Minute, nil, failedScoutEnd()...)
	x.observe(6*time.Minute, nil, failedScoutEnd()...)
	x.wantResults("work")
	later := ship("calm-lark", "work", ships.StatusUnderway)
	later.Scout = true
	x.observe(7*time.Minute, []ships.Ship{later})
	x.observe(10*time.Minute, nil, append(failedScoutEnd(),
		ShipRecord{At: t0.Add(8 * time.Minute), Ship: "calm-lark", Event: "ended", Result: "failed"},
		ShipRecord{At: t0.Add(8*time.Minute + time.Second), Ship: "calm-lark", Event: "stopped", Scout: true})...)
	x.wantResults("work", "failed")
}

func TestAScoutThatSailedOffBeforeTheResultWasReadPostsNoSecondFailedCardWhenItsRecordsGainOne(t *testing.T) {
	t.Parallel()
	x := seeded(t)
	x.observe(time.Minute, []ships.Ship{scout("calm-otter", "work")})
	plain := []ShipRecord{
		{At: t0.Add(2 * time.Minute), Ship: "calm-otter", Event: "ended"},
		{At: t0.Add(2*time.Minute + time.Second), Ship: "calm-otter", Event: "stopped", Scout: true},
	}
	x.observe(5*time.Minute, nil, plain...)
	x.wantResults("work", "sailed_off")
	x.restart()
	x.observe(6*time.Minute, nil, failedScoutEnd()...)
	x.observe(7*time.Minute, nil, failedScoutEnd()...)
	x.wantResults("work", "sailed_off")
}

func TestPostedShipCardsAreTheFleetsCardsLoggedAfterTheTimeWithTheirVoyage(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	before := time.Now().Add(-time.Hour)
	for _, c := range []ShipCard{
		{Ship: "s-dory", Name: "dory", FirstLaunched: "v1", Result: ShipMoored, PRs: []ShipCardPR{{URL: "https://github.com/o/r/pull/7", Number: 7}}},
		{Ship: "s-skiff", Name: "skiff", FirstLaunched: "v2", Result: ShipSailedOff},
	} {
		if err := r.PostShipCard("work", c); err != nil {
			t.Fatal(err)
		}
	}
	got := r.PostedShipCards("work", before)
	if len(got) != 2 || got[0].Ship != "s-dory" || got[0].Voyage != "v1" || got[0].Result != ShipMoored || len(got[0].PRs) != 1 || got[0].PRs[0] != "https://github.com/o/r/pull/7" || got[0].At.IsZero() {
		t.Fatalf("cards = %+v, want dory's moored card with its voyage, PR and logged time, then skiff's", got)
	}
	if got[1].Name != "skiff" || got[1].Voyage != "v2" {
		t.Errorf("second card = %+v, want skiff on voyage v2", got[1])
	}
	if later := r.PostedShipCards("work", time.Now().Add(time.Hour)); len(later) != 0 {
		t.Errorf("cards after the future = %+v, want none", later)
	}
	if other := r.PostedShipCards("home", before); len(other) != 0 {
		t.Errorf("home's cards = %+v, want none: cards are per fleet", other)
	}
}

func TestCardsSettledInOnePassPostInTheOrderTheirShipsEndedTiesByShipName(t *testing.T) {
	t.Parallel()
	x := newDetector(t)
	x.observe(time.Minute, []ships.Ship{ship("ship-h", "work", ships.StatusUnderway), ship("ship-g", "work", ships.StatusUnderway),
		scout("scout-f", "work"), ship("ship-e", "work", ships.StatusUnderway), ship("ship-d", "work", ships.StatusUnderway),
		ship("ship-b", "work", ships.StatusUnderway), ship("ship-a", "work", ships.StatusUnderway)})
	clean := func(name string, when time.Duration) ShipRecord {
		return ShipRecord{At: t0.Add(when), Ship: name, Event: "ended", Clean: true}
	}
	x.observe(10*time.Minute, nil,
		clean("ship-b", 7*time.Minute), clean("ship-a", 7*time.Minute), clean("ship-d", 6*time.Minute), clean("ship-e", 5*time.Minute),
		clean("ship-g", 3*time.Minute), clean("ship-h", 2*time.Minute),
		ShipRecord{At: t0.Add(4 * time.Minute), Ship: "scout-f", Event: "ended"},
		ShipRecord{At: t0.Add(4*time.Minute + time.Second), Ship: "scout-f", Event: "stopped", Scout: true})
	var got []string
	for _, c := range shipCardsIn(x.t, x.r, "work") {
		got = append(got, c.Ship)
	}
	want := []string{"ship-h", "ship-g", "scout-f", "ship-e", "ship-d", "ship-a", "ship-b"}
	if !slices.Equal(got, want) {
		t.Errorf("work chat ship cards = %v, want %v", got, want)
	}
}
