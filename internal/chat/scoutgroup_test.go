package chat

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/ships"
)

// Scouts launched in one engineer turn report once, after the last of them ends and the turn is over.

type scoutCall struct {
	fleet   string
	trigger ShipCard
	members []string
}

type scoutLog struct {
	calls []scoutCall
	fail  error
}

func (l *scoutLog) report(fleet string, trigger ShipCard, group []ShipCard) error {
	if l.fail != nil {
		return l.fail
	}
	var names []string
	for _, c := range group {
		names = append(names, c.Ship)
	}
	slices.Sort(names)
	l.calls = append(l.calls, scoutCall{fleet, trigger, names})
	return nil
}

// grouping is a detector with both report hooks wired and a turn that stays open until a test ends it.
type grouping struct {
	*reporting
	scouts   *scoutLog
	turnOver bool
}

func newGrouping(t *testing.T) *grouping {
	g := &grouping{reporting: newReporting(t), scouts: &scoutLog{}, turnOver: true}
	g.wire()
	return g
}

func (g *grouping) wire() {
	g.d.QueueScoutReports(g.scouts.report)
	g.d.TurnOver(func(fleet, key string) bool { return g.turnOver })
}

func (g *grouping) restart() {
	g.reporting.restart()
	g.wire()
}

func (g *grouping) wantTurns(want ...string) {
	g.t.Helper()
	var got []string
	for _, c := range g.scouts.calls {
		got = append(got, strings.Join(c.members, "+")+"<-"+c.trigger.Ship+":"+c.trigger.Result)
	}
	if !slices.Equal(got, want) {
		g.t.Errorf("scout report turns = %v, want %v", got, want)
	}
}

const turnOne = "nonce-1"

func botScout(name, key string) ships.Ship {
	s := scout(name, "work")
	s.Via, s.PromptKey = "admiralbot", key
	return s
}

func at(d time.Duration) time.Time { return t0.Add(d) }

// sailedOff is a scout's own end: a kill and its scout stop.
func sailedOff(name string, when time.Duration) []ShipRecord {
	return []ShipRecord{
		{At: at(when), Ship: name, Event: "ended"},
		{At: at(when + time.Second), Ship: name, Event: "stopped", Scout: true},
	}
}

// leftNothing is a scout's end with no findings.
func leftNothing(name string, when time.Duration) []ShipRecord {
	return []ShipRecord{
		{At: at(when), Ship: name, Event: "ended", Result: "failed"},
		{At: at(when + time.Second), Ship: name, Event: "stopped", Scout: true},
	}
}

func join(lists ...[]ShipRecord) []ShipRecord {
	var out []ShipRecord
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

func cardIDOf(ship, result string) string {
	return "ship-" + ship + "-2026-11-17T10:00:00Z-" + result
}

func TestScoutsFromOneTurnQueueOneReportAfterTheLastEndsNamingAllOfThemUnderTheLastsCard(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne)})
	g.observe(3*time.Minute, []ships.Ship{botScout("scout-b", turnOne)}, sailedOff("scout-a", 2*time.Minute)...)
	g.wantResults("work", "sailed_off")
	g.wantTurns()
	ends := join(sailedOff("scout-a", 2*time.Minute), sailedOff("scout-b", 4*time.Minute))
	g.observe(5*time.Minute, nil, ends...)
	g.observe(6*time.Minute, nil, ends...)
	g.wantResults("work", "sailed_off", "sailed_off")
	g.wantTurns("scout-a+scout-b<-scout-b:sailed_off")
	if len(g.scouts.calls) == 0 {
		return
	}
	if id := g.scouts.calls[0].trigger.id(); id != cardIDOf("scout-b", "sailed_off") {
		t.Errorf("the turn's card = %s, want the last scout's card %s", id, cardIDOf("scout-b", "sailed_off"))
	}
	if g.scouts.calls[0].fleet != "work" {
		t.Errorf("the turn is for fleet %q, want work", g.scouts.calls[0].fleet)
	}
}

func TestAScoutThatEndedAndSettledBeforeItsSiblingWasLaunchedStillJoinsTheOneReport(t *testing.T) {
	g := newGrouping(t)
	g.turnOver = false
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne)})
	g.observe(3*time.Minute, nil, sailedOff("scout-a", 2*time.Minute)...)
	g.observe(4*time.Minute, []ships.Ship{botScout("scout-b", turnOne)}, sailedOff("scout-a", 2*time.Minute)...)
	ends := join(sailedOff("scout-a", 2*time.Minute), sailedOff("scout-b", 5*time.Minute))
	g.observe(7*time.Minute, nil, ends...)
	g.wantTurns()
	g.turnOver = true
	g.observe(8*time.Minute, nil, ends...)
	g.wantTurns("scout-a+scout-b<-scout-b:sailed_off")
}

func TestAGroupWhoseScoutsAllEndedMidTurnReportsOnceWhenTheTurnEndsAndNotBefore(t *testing.T) {
	g := newGrouping(t)
	g.turnOver = false
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne)})
	ends := join(sailedOff("scout-a", 2*time.Minute), sailedOff("scout-b", 3*time.Minute))
	g.observe(5*time.Minute, nil, ends...)
	g.observe(6*time.Minute, nil, ends...)
	g.wantResults("work", "sailed_off", "sailed_off")
	g.wantTurns()
	g.turnOver = true
	g.observe(7*time.Minute, nil, ends...)
	g.observe(8*time.Minute, nil, ends...)
	g.wantTurns("scout-a+scout-b<-scout-b:sailed_off")
}

func TestAScoutGroupWaitsForEveryScoutStillOut(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne), botScout("scout-c", turnOne)})
	ends := join(sailedOff("scout-a", 2*time.Minute), sailedOff("scout-b", 3*time.Minute))
	g.observe(5*time.Minute, []ships.Ship{botScout("scout-c", turnOne)}, ends...)
	g.wantResults("work", "sailed_off", "sailed_off")
	g.wantTurns()
	g.observe(9*time.Minute, nil, join(ends, sailedOff("scout-c", 6*time.Minute))...)
	g.wantTurns("scout-a+scout-b+scout-c<-scout-c:sailed_off")
}

func TestScoutsOfDifferentTurnsReportSeparately(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", "nonce-1"), botScout("scout-b", "nonce-2")})
	g.observe(5*time.Minute, nil, join(sailedOff("scout-a", 2*time.Minute), sailedOff("scout-b", 3*time.Minute))...)
	g.wantTurns("scout-a<-scout-a:sailed_off", "scout-b<-scout-b:sailed_off")
}

func TestAScoutWithNoRecordedTurnReportsAloneAndASingleScoutOfATurnDoes(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", ""), botScout("scout-b", ""), botScout("scout-c", turnOne)})
	g.observe(5*time.Minute, []ships.Ship{botScout("scout-b", "")}, join(sailedOff("scout-a", 2*time.Minute), sailedOff("scout-c", 3*time.Minute))...)
	g.wantTurns("scout-a<-scout-a:sailed_off", "scout-c<-scout-c:sailed_off")
	g.observe(9*time.Minute, nil, join(sailedOff("scout-a", 2*time.Minute), sailedOff("scout-c", 3*time.Minute), sailedOff("scout-b", 6*time.Minute))...)
	g.wantTurns("scout-a<-scout-a:sailed_off", "scout-c<-scout-c:sailed_off", "scout-b<-scout-b:sailed_off")
}

func TestAScoutsReportNeverWaitsOnAQuickShipFromTheSameTurnAndTheQuickShipKeepsItsOwnTurn(t *testing.T) {
	quick := func(s ships.Ship) ships.Ship { s.PromptKey = turnOne; return s }
	t.Run("the quick ship moors first", func(t *testing.T) {
		g := newGrouping(t)
		g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), quick(quickBot("quick-q"))})
		g.observe(2*time.Minute, []ships.Ship{botScout("scout-a", turnOne), quick(withStatus(quickBot("quick-q"), ships.StatusMoored, pr7))})
		g.want("quick-q:moored")
		g.observe(5*time.Minute, []ships.Ship{quick(withStatus(quickBot("quick-q"), ships.StatusMoored, pr7))}, sailedOff("scout-a", 3*time.Minute)...)
		g.wantTurns("scout-a<-scout-a:sailed_off")
		g.want("quick-q:moored")
	})
	t.Run("the quick ship is still sailing", func(t *testing.T) {
		g := newGrouping(t)
		g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), quick(quickBot("quick-q"))})
		g.observe(5*time.Minute, []ships.Ship{quick(quickBot("quick-q"))}, sailedOff("scout-a", 3*time.Minute)...)
		g.wantTurns("scout-a<-scout-a:sailed_off")
		g.want()
	})
	t.Run("a Commodore ship neither holds nor joins the group", func(t *testing.T) {
		g := newGrouping(t)
		g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), quick(commodoreBot("cmdr-c"))})
		g.observe(5*time.Minute, []ships.Ship{quick(commodoreBot("cmdr-c"))}, sailedOff("scout-a", 3*time.Minute)...)
		g.wantTurns("scout-a<-scout-a:sailed_off")
	})
}

func TestAScoutThatLeftNoFindingsEndsTheGroupAsFailedOnItsCardAndInTheTurn(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne)})
	g.observe(5*time.Minute, nil, join(sailedOff("scout-a", 2*time.Minute), leftNothing("scout-b", 3*time.Minute))...)
	g.wantResults("work", "sailed_off", "failed")
	g.wantTurns("scout-a+scout-b<-scout-b:failed")
}

func TestEveryEndOfTheLastScoutQueuesTheGroupsOneTurnWithItsCard(t *testing.T) {
	cases := map[string]struct {
		last   func(g *grouping)
		result string
	}{
		"the engineer's Stop": {func(g *grouping) {
			g.observe(5*time.Minute, nil, join(sailedOff("scout-a", 2*time.Minute), []ShipRecord{
				{At: at(3 * time.Minute), Ship: "scout-b", Event: "ended", Result: "failed"},
				{At: at(3*time.Minute + time.Second), Ship: "scout-b", Event: "stopped"},
			})...)
		}, "failed"},
		"a loss": {func(g *grouping) {
			g.observe(5*time.Minute, nil, sailedOff("scout-a", 2*time.Minute)...)
		}, ""},
		"an anchor": {func(g *grouping) {
			anchored := withStatus(botScout("scout-b", turnOne), ships.StatusAnchored)
			g.observe(5*time.Minute, []ships.Ship{anchored}, sailedOff("scout-a", 2*time.Minute)...)
		}, "anchored"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g := newGrouping(t)
			g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne)})
			c.last(g)
			if got := g.results("work"); len(got) != 2 || !slices.Contains(got, "sailed_off") {
				t.Fatalf("cards = %v, want scout-a's sailed_off and one for scout-b", got)
			}
			if len(g.scouts.calls) != 1 || !slices.Equal(g.scouts.calls[0].members, []string{"scout-a", "scout-b"}) || g.scouts.calls[0].trigger.Ship != "scout-b" {
				t.Fatalf("scout report turns = %+v, want one naming both under scout-b", g.scouts.calls)
			}
			if c.result != "" && g.scouts.calls[0].trigger.Result != c.result {
				t.Errorf("the turn's card result = %s, want %s", g.scouts.calls[0].trigger.Result, c.result)
			}
		})
	}
}

func TestAWokenScoutLeavesItsGroupAndNeverHoldsIt(t *testing.T) {
	g := newGrouping(t)
	woken := botScout("scout-b", turnOne)
	woken.Launched, woken.Round = "2026-11-17T10:05:00Z", 1
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), woken})
	g.observe(5*time.Minute, []ships.Ship{woken}, sailedOff("scout-a", 2*time.Minute)...)
	g.wantTurns("scout-a<-scout-a:sailed_off")
}

func TestReplayingTheSameRecordsAndRestartingQueueNoSecondGroupTurn(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne)})
	ends := join(sailedOff("scout-a", 2*time.Minute), sailedOff("scout-b", 3*time.Minute))
	g.observe(5*time.Minute, nil, ends...)
	g.observe(6*time.Minute, nil, ends...)
	g.restart()
	g.observe(7*time.Minute, nil, ends...)
	g.observe(8*time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne)}, ends...)
	g.wantTurns("scout-a+scout-b<-scout-b:sailed_off")
}

func TestAGroupTurnThatFailsToQueueIsQueuedByTheNextPassAndOnceAfterThat(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne)})
	g.scouts.fail = errors.New("the queue file is not writable")
	ends := sailedOff("scout-a", 2*time.Minute)
	if err := g.d.Observe(nil, ends, at(5*time.Minute)); err == nil {
		t.Fatal("Observe = nil, want the enqueue error")
	}
	g.scouts.fail = nil
	g.observe(6*time.Minute, nil, ends...)
	g.observe(7*time.Minute, nil, ends...)
	g.wantTurns("scout-a<-scout-a:sailed_off")
}

// A ship-cards.json from before scouts reported: the scout's card is settled and no group is recorded.
const savedBeforeGroups = `{"Seeded":true,"Scouts":true,"Voyages":{"scout-old|2026-11-17T10:00:00Z":{"Ship":"scout-old","Name":"scout-old","Fleet":"work","Via":"admiralbot","SkillKind":"none","Scout":true,"Round":0,"Reported":false,"FirstLaunched":"2026-11-17T10:00:00Z","Done":{"sailed_off":true},"ScoutEndedAt":"2026-11-17T10:02:00Z","LastSeen":"2026-11-17T10:03:00Z"}}}`

func TestAScoutCardSettledByAnOlderBuildQueuesNoGroupTurnOnTheFirstPassAfterTheUpgrade(t *testing.T) {
	g := newGrouping(t)
	if err := os.WriteFile(filepath.Join(g.r.home, "ship-cards.json"), []byte(savedBeforeGroups), 0o600); err != nil {
		t.Fatal(err)
	}
	g.restart()
	old := sailedOff("scout-old", 2*time.Minute)
	g.observe(5*time.Minute, nil, old...)
	g.observe(6*time.Minute, nil, old...)
	g.wantTurns()
	g.observe(7*time.Minute, []ships.Ship{botScout("scout-new", turnOne)}, old...)
	g.observe(9*time.Minute, nil, join(old, sailedOff("scout-new", 8*time.Minute))...)
	g.wantTurns("scout-new<-scout-new:sailed_off")
}

func TestAScoutAnchoredAtTheSeedQueuesNoGroupTurnForThatFirstEvent(t *testing.T) {
	x := newDetector(t)
	log := &scoutLog{}
	x.d.QueueScoutReports(log.report)
	x.d.TurnOver(func(string, string) bool { return true })
	x.observe(0, []ships.Ship{withStatus(botScout("scout-old", turnOne), ships.StatusAnchored)})
	x.observe(time.Minute, []ships.Ship{withStatus(botScout("scout-old", turnOne), ships.StatusAnchored), botScout("scout-new", turnOne)})
	if len(log.calls) != 0 {
		t.Fatalf("scout report turns = %+v, want none for a scout whose anchor the seed found", log.calls)
	}
	x.observe(5*time.Minute, []ships.Ship{withStatus(botScout("scout-old", turnOne), ships.StatusAnchored)}, sailedOff("scout-new", 2*time.Minute)...)
	if len(log.calls) != 1 || !slices.Equal(log.calls[0].members, []string{"scout-new"}) {
		t.Errorf("scout report turns = %+v, want one for scout-new alone", log.calls)
	}
}

func TestAScoutTheEngineerStoppedOrThatWasLostIsFailedWithoutFindingsAndSailedOffWithThem(t *testing.T) {
	stopped := []ShipRecord{
		{At: at(2 * time.Minute), Ship: "scout-a", Event: "ended"},
		{At: at(2*time.Minute + time.Second), Ship: "scout-a", Event: "stopped"},
	}
	cases := []struct {
		name string
		recs []ShipRecord
		kept bool
		want string
	}{
		{"stopped with findings", stopped, true, "sailed_off"},
		{"stopped without findings", stopped, false, "failed"},
		{"lost with findings", nil, true, "sailed_off"},
		{"lost without findings", nil, false, "failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newGrouping(t)
			g.d.KeptFindings(func(ship, first string) bool { return ship == "scout-a" && c.kept })
			g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne)})
			g.observe(5*time.Minute, nil, c.recs...)
			g.wantResults("work", c.want)
			g.wantTurns("scout-a<-scout-a:" + c.want)
		})
	}
}

func TestAScoutStillInTheSnapshotIsNotLostAndAnchoredOneIsNotCardedAgainWhenItLeaves(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne)})
	anchored := withStatus(botScout("scout-b", turnOne), ships.StatusAnchored)
	g.observe(5*time.Minute, []ships.Ship{botScout("scout-a", turnOne), anchored})
	g.observe(10*time.Minute, []ships.Ship{botScout("scout-a", turnOne)})
	g.wantResults("work", "anchored")
	g.wantTurns()
}

// hostState sets what the host says it cannot show: lost tmux, and ships whose options could not be read.
func (g *grouping) hostState(lost bool, unreadable ...string) {
	g.d.HostState(func() (bool, []string) { return lost, unreadable })
}

func TestAScoutMissingWhileTmuxIsLostIsNotLostAndItsRealEndPostsTheRightCard(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne)})
	g.hostState(true)
	g.observe(2*time.Minute, nil)
	g.observe(5*time.Minute, nil)
	g.observe(10*time.Minute, nil)
	g.wantResults("work")
	g.wantTurns()
	g.hostState(false)
	g.observe(11*time.Minute, []ships.Ship{botScout("scout-a", turnOne)})
	g.observe(12*time.Minute, nil, sailedOff("scout-a", 11*time.Minute+30*time.Second)...)
	g.wantResults("work", "sailed_off")
	g.wantTurns("scout-a<-scout-a:sailed_off")
}

func TestAScoutWhoseOptionsCouldNotBeReadIsNotLostWhileTmuxStillListsIt(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne)})
	g.hostState(false, "scout-a")
	g.observe(5*time.Minute, nil)
	g.observe(10*time.Minute, nil)
	g.wantResults("work")
	g.wantTurns()
}

func TestAScoutKeepsTheEngineerTurnItWasFirstSeenInWhateverItsOptionLaterSays(t *testing.T) {
	g := newGrouping(t)
	g.observe(time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", turnOne)})
	g.observe(2*time.Minute, []ships.Ship{botScout("scout-a", turnOne), botScout("scout-b", "nonce-9")})
	g.observe(3*time.Minute, nil, append(sailedOff("scout-a", 150*time.Second), sailedOff("scout-b", 160*time.Second)...)...)
	g.observe(4*time.Minute, nil)
	if len(g.scouts.calls) != 1 || len(g.scouts.calls[0].members) != 2 {
		t.Errorf("turns = %+v, want one group of both scouts", g.scouts.calls)
	}
}
