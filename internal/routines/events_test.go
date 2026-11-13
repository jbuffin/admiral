package routines_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

// The tier a login earns: the less trusted of actor and author, decided in Go from the viewer and the allowlist.
func TestTierIsTheLessTrustedOfActorAndAuthor(t *testing.T) {
	limits := standing.Limits{SenderAllowlist: []string{"Alice"}}
	cases := []struct {
		name          string
		actor, author string
		viewer        string
		verified      bool
		want          standing.Tier
	}{
		{"engineer both", "Eng", "Eng", "Eng", true, standing.T1},
		{"engineer compares case-insensitively", "eng", "ENG", "Eng", true, standing.T1},
		{"engineer acting on an allowlisted author", "Eng", "alice", "Eng", true, standing.T2},
		{"allowlisted actor on the engineer's item", "alice", "Eng", "Eng", true, standing.T2},
		{"allowlist compares case-insensitively", "ALICE", "alice", "Eng", true, standing.T2},
		{"engineer touching a stranger's item", "Eng", "stranger", "Eng", true, standing.T3},
		{"stranger acting on the engineer's item", "stranger", "Eng", "Eng", true, standing.T3},
		{"allowlisted next to a stranger", "alice", "stranger", "Eng", true, standing.T3},
		{"strangers", "x", "y", "Eng", true, standing.T3},
		{"an unverified engineer is never T1", "Eng", "Eng", "Eng", false, standing.T3},
		{"an unverified allowlisted login is still T2", "alice", "alice", "Eng", false, standing.T2},
		{"no viewer means no T1", "Eng", "Eng", "", true, standing.T3},
		{"a ghost author is a stranger's", "Eng", "", "Eng", true, standing.T3},
		{"an empty actor is a stranger's even on the engineer's item", "", "Eng", "Eng", true, standing.T3},
		{"an unattributable actor is a stranger's", "(unknown)", "Eng", "Eng", true, standing.T3},
		{"an unattributable author is a stranger's", "Eng", "(unknown)", "Eng", true, standing.T3},
		{"no actor and no author is a stranger", "", "", "Eng", true, standing.T3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := routines.Event{Actor: c.actor, Author: c.author, Viewer: c.viewer, Verified: c.verified}
			if got := routines.TierOf(ev, limits); got != c.want {
				t.Errorf("TierOf(actor %q, author %q, viewer %q, verified %v) = T%d, want T%d", c.actor, c.author, c.viewer, c.verified, got, c.want)
			}
		})
	}
}

var thursday = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func decideIn(mod func(*routines.DecideInput)) routines.DecideInput {
	in := routines.DecideInput{
		Routine: routines.Routine{ID: "r1", Before: routines.Act},
		Tier:    standing.T1,
		Limits:  standing.PresetLimits(standing.Home),
		Top:     standing.A5,
		Now:     thursday,
	}
	if mod != nil {
		mod(&in)
	}
	return in
}

func wantDecision(t *testing.T, got routines.Decision, outcome routines.Outcome, level standing.Level, reasons ...string) {
	t.Helper()
	if got.Outcome != outcome || got.Level != level || !slices.Equal(got.Reasons, reasons) {
		t.Errorf("decision = {%s A%d %v}, want {%s A%d %v}", got.Outcome, got.Level, got.Reasons, outcome, level, reasons)
	}
}

func TestTheDecisionFollowsTheRoutineAndTheCeilingsUntilTheTop(t *testing.T) {
	g := routines.Decide(decideIn(nil))
	wantDecision(t, g, routines.Launched, standing.A5)
	if g.Computed != standing.A5 {
		t.Errorf("Computed = A%d, want A5", g.Computed)
	}
	g = routines.Decide(decideIn(func(in *routines.DecideInput) { in.Top = routines.EventTop }))
	wantDecision(t, g, routines.Proposed, standing.A1, routines.ReasonTop)
	if g.Computed != standing.A5 {
		t.Errorf("Computed = A%d, want A5 before the top", g.Computed)
	}
}

func TestTheRoutinesBeforeActingSetsTheLevelItStartsFrom(t *testing.T) {
	for _, top := range []standing.Level{routines.EventTop, standing.A5} {
		g := routines.Decide(decideIn(func(in *routines.DecideInput) { in.Top, in.Routine.Before = top, routines.ReportOnly }))
		wantDecision(t, g, routines.Reported, standing.A0)
		g = routines.Decide(decideIn(func(in *routines.DecideInput) { in.Top, in.Routine.Before = top, routines.AskFirst }))
		wantDecision(t, g, routines.Proposed, standing.A1)
	}
}

func TestATierCeilingLowersTheDecisionAndNoStoredValueRaisesTheThirdTier(t *testing.T) {
	g := routines.Decide(decideIn(func(in *routines.DecideInput) {
		in.Tier, in.Limits.Ceilings.T3 = standing.T3, standing.Ceiling{Level: standing.A5}
	}))
	wantDecision(t, g, routines.Proposed, standing.A1)
	if g.Computed != standing.A1 {
		t.Errorf("a hand-edited T3 ceiling gave Computed A%d, want A1", g.Computed)
	}
	g = routines.Decide(decideIn(func(in *routines.DecideInput) {
		in.Tier, in.Limits = standing.T2, standing.PresetLimits(standing.Work)
	}))
	wantDecision(t, g, routines.Proposed, standing.A1)
	if g.Computed != standing.A1 {
		t.Errorf("a stranger-free T2 on Work gave Computed A%d, want A1", g.Computed)
	}
	g = routines.Decide(decideIn(func(in *routines.DecideInput) { in.Tier, in.Limits = standing.T1, standing.PresetLimits(standing.Work) }))
	if g.Computed != standing.A1 {
		t.Errorf("T1 on Work asks first, so Computed A%d, want A1", g.Computed)
	}
}

func TestASkipFilterDropsTheEventForItsRoutineOnly(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*routines.DecideInput)
	}{
		{"label", func(in *routines.DecideInput) {
			in.Limits.Skip.Labels, in.Labels = []standing.Scoped{{Value: "wip"}}, []string{"WIP"}
		}},
		{"sender", func(in *routines.DecideInput) {
			in.Limits.Skip.Senders, in.Sender = []standing.Scoped{{Routine: "r1", Value: "bob"}}, "BOB"
		}},
		{"dedupe key", func(in *routines.DecideInput) {
			in.Limits.Skip.DedupeKeys, in.DedupeKey = []standing.Scoped{{Value: "k1"}}, "k1"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantDecision(t, routines.Decide(decideIn(c.mod)), routines.Skipped, standing.A0, routines.ReasonSkipFilter)
			// the same filter scoped to another routine leaves this one alone
			other := func(in *routines.DecideInput) {
				c.mod(in)
				for _, l := range []*[]standing.Scoped{&in.Limits.Skip.Labels, &in.Limits.Skip.Senders, &in.Limits.Skip.DedupeKeys} {
					for i := range *l {
						(*l)[i].Routine = "someone-else"
					}
				}
			}
			wantDecision(t, routines.Decide(decideIn(other)), routines.Launched, standing.A5)
		})
	}
}

func TestTheRepoAllowlistSkipsOtherReposAndAnEmptyListAllowsAll(t *testing.T) {
	list := func(in *routines.DecideInput) { in.Limits.RepoAllowlist = []string{"acme/api"} }
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { list(in); in.Repo = "acme/web" })),
		routines.Skipped, standing.A0, routines.ReasonRepo)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { list(in); in.Repo = "ACME/API" })), routines.Launched, standing.A5)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.Repo = "acme/web" })), routines.Launched, standing.A5)
}

func TestASkipFilterIsCheckedBeforeTheRepoAllowlist(t *testing.T) {
	g := routines.Decide(decideIn(func(in *routines.DecideInput) {
		in.Limits.Skip.Senders, in.Sender = []standing.Scoped{{Value: "bob"}}, "bob"
		in.Limits.RepoAllowlist, in.Repo = []string{"acme/api"}, "acme/web"
	}))
	wantDecision(t, g, routines.Skipped, standing.A0, routines.ReasonSkipFilter)
}

func TestSpendReachedStopsAnActionAtA1AndLeavesAReportAlone(t *testing.T) {
	cap := standing.PresetLimits(standing.Home).Spend.DailyWorkerUSD
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.SpentTodayUSD = cap })),
		routines.Proposed, standing.A1, routines.ReasonSpend)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.SpentTodayUSD = cap - 0.01 })), routines.Launched, standing.A5)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.SpentTodayUSD, in.Routine.Before = cap, routines.ReportOnly })),
		routines.Reported, standing.A0)
}

func TestWithTheTopAtA1SpendReachedStillSaysSo(t *testing.T) {
	cap := standing.PresetLimits(standing.Home).Spend.DailyWorkerUSD
	g := routines.Decide(decideIn(func(in *routines.DecideInput) { in.Top, in.SpentTodayUSD = routines.EventTop, cap }))
	wantDecision(t, g, routines.Proposed, standing.A1, routines.ReasonTop, routines.ReasonSpend)
}

func TestQuietHoursHoldAnActionAtA1ByTheClockOnTheNowGiven(t *testing.T) {
	quiet := func(in *routines.DecideInput) {
		in.Limits.QuietHours = []standing.Window{{Weekdays: []string{"thu"}, Start: "09:00", End: "11:00"}}
	}
	wantDecision(t, routines.Decide(decideIn(quiet)), routines.Proposed, standing.A1, routines.ReasonQuiet)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { quiet(in); in.Now = thursday.Add(time.Hour) })),
		routines.Launched, standing.A5)
	// the same instant read on a fixed +10:00 wall clock is 20:00 Thursday, outside the window
	plus10 := time.FixedZone("plus10", 10*3600)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { quiet(in); in.Now = thursday.In(plus10) })),
		routines.Launched, standing.A5)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) {
		quiet(in)
		in.Now = time.Date(2026, 11, 12, 10, 0, 0, 0, plus10)
	})), routines.Proposed, standing.A1, routines.ReasonQuiet)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { quiet(in); in.Routine.Before = routines.ReportOnly })),
		routines.Reported, standing.A0)
}

func TestHoldFireTurnsActIntoAskFirstAndLeavesAReportAlone(t *testing.T) {
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.HoldOn = true })),
		routines.Proposed, standing.A1, routines.ReasonHold)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.HoldOn, in.Routine.Before = true, routines.ReportOnly })),
		routines.Reported, standing.A0)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.HoldOn, in.Routine.Before = true, routines.AskFirst })),
		routines.Proposed, standing.A1)
}

func TestARateReachedHoldsALaunchAtA1AndNamesTheLimit(t *testing.T) {
	home := standing.PresetLimits(standing.Home).Rates
	cases := []struct {
		name   string
		counts standing.Counts
		reason string
	}{
		{"per hour", standing.Counts{LaunchesLastHour: home.LaunchesPerHour}, standing.ReasonPerHour},
		{"per sender", standing.Counts{SenderLaunchesToday: home.LaunchesPerSenderPerDay}, standing.ReasonPerSender},
		{"concurrent", standing.Counts{WorkerShips: home.Concurrent}, standing.ReasonConcurrent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.Counts = c.counts })), routines.Proposed, standing.A1, c.reason)
		})
	}
	under := standing.Counts{LaunchesLastHour: home.LaunchesPerHour - 1, SenderLaunchesToday: home.LaunchesPerSenderPerDay - 1, WorkerShips: home.Concurrent - 1}
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.Counts = under })), routines.Launched, standing.A5)
}

func TestRatesApplyToLaunchesOnly(t *testing.T) {
	full := standing.Counts{LaunchesLastHour: 99, SenderLaunchesToday: 99, WorkerShips: 99}
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.Counts, in.Routine.Before = full, routines.AskFirst })),
		routines.Proposed, standing.A1)
	wantDecision(t, routines.Decide(decideIn(func(in *routines.DecideInput) { in.Counts, in.Routine.Before = full, routines.ReportOnly })),
		routines.Reported, standing.A0)
}

func TestALiveShipOnTheSameDedupeKeyHoldsALaunchAtA1(t *testing.T) {
	live := func(key string) func(*routines.DecideInput) {
		return func(in *routines.DecideInput) { in.LiveDedupeKeys, in.DedupeKey = []string{"k1"}, key }
	}
	wantDecision(t, routines.Decide(decideIn(live("k1"))), routines.Proposed, standing.A1, routines.ReasonDedupe)
	wantDecision(t, routines.Decide(decideIn(live("k2"))), routines.Launched, standing.A5)
	wantDecision(t, routines.Decide(decideIn(live(""))), routines.Launched, standing.A5)
}

func TestTheDecisionNamesCausesInTheOrderSpendQuietRatesDedupe(t *testing.T) {
	limits := standing.PresetLimits(standing.Home)
	g := routines.Decide(decideIn(func(in *routines.DecideInput) {
		in.Limits.QuietHours = []standing.Window{{Weekdays: []string{"thu"}, Start: "09:00", End: "11:00"}}
		in.SpentTodayUSD = limits.Spend.DailyWorkerUSD
		in.Counts = standing.Counts{LaunchesLastHour: limits.Rates.LaunchesPerHour}
		in.LiveDedupeKeys, in.DedupeKey = []string{"k1"}, "k1"
	}))
	wantDecision(t, g, routines.Proposed, standing.A1,
		routines.ReasonSpend, routines.ReasonQuiet, standing.ReasonPerHour, routines.ReasonDedupe)
}

func TestARoutineAlreadyAtA1NamesNoLimitThatCouldNotLowerIt(t *testing.T) {
	limits := standing.PresetLimits(standing.Home)
	g := routines.Decide(decideIn(func(in *routines.DecideInput) {
		in.Routine.Before, in.Top = routines.AskFirst, routines.EventTop
		in.SpentTodayUSD = limits.Spend.DailyWorkerUSD
		in.Limits.QuietHours = []standing.Window{{Weekdays: []string{"thu"}, Start: "09:00", End: "11:00"}}
	}))
	wantDecision(t, g, routines.Proposed, standing.A1)
}

func ago(d time.Duration) time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC).Add(-d) }

func TestATriggerRecordKeepsItsDedupeKeyAndCostsAndOldRecordsReadAsZero(t *testing.T) {
	r := newRig(t)
	rec, err := r.store.AppendTrigger("work", routines.Trigger{Routine: "r1", Outcome: routines.Proposed, Tier: "T2",
		DedupeKey: "k1", Costs: []routines.Cost{{Call: "lookout", USD: 0.05}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.store.Recent("work", "r1", 0)
	if err != nil || len(got) != 1 || got[0].DedupeKey != "k1" || len(got[0].Costs) != 1 || got[0].Costs[0] != (routines.Cost{Call: "lookout", USD: 0.05}) {
		t.Fatalf("Recent = %+v, %v, want the record %+v back with its key and cost", got, err, rec)
	}
	raw, err := os.ReadFile(filepath.Join(r.home, "routines", "work", "triggers.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"dedupe_key":"k1"`, `"call":"lookout"`, `"usd":0.05`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("triggers.jsonl lacks %s: %s", want, raw)
		}
	}
	old := `{"id":"old1","at":"2026-11-10T09:00:00Z","routine":"r2","outcome":"launched","tier":"T0"}` + "\n"
	f, err := os.OpenFile(filepath.Join(r.home, "routines", "work", "triggers.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(old); err != nil {
		t.Fatal(err)
	}
	f.Close()
	all, err := r.store.Records("work")
	if err != nil || len(all) != 2 {
		t.Fatalf("Records = %+v, %v, want both records", all, err)
	}
	for _, x := range all {
		if x.ID == "old1" && (x.DedupeKey != "" || len(x.Costs) != 0) {
			t.Errorf("an old record reads %+v, want no key and no costs", x)
		}
	}
}

func TestRecordsReadsEveryRoutinesLogOfOneFleetNewestFirstWithUndoFolded(t *testing.T) {
	r := newRig(t)
	a, _ := r.store.AppendTrigger("work", routines.Trigger{Routine: "r1", Outcome: routines.Launched, Tier: "T0", At: ago(3 * time.Hour), Ship: "s1"})
	r.store.AppendTrigger("work", routines.Trigger{Routine: "r2", Outcome: routines.Proposed, Tier: "T2", At: ago(1 * time.Hour)})
	r.store.AppendTrigger("play", routines.Trigger{Routine: "r3", Outcome: routines.Proposed, Tier: "T2", At: ago(2 * time.Hour)})
	if err := r.store.Undo("work", a.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.store.Records("work")
	if err != nil || len(got) != 2 {
		t.Fatalf("Records(work) = %+v, %v, want the two work records only", got, err)
	}
	if got[0].Routine != "r2" || got[1].Routine != "r1" || !got[1].Undone {
		t.Errorf("Records = %+v, want r2 then an undone r1", got)
	}
}

func TestDailySpendSumsTodaysCostsInTheZoneAcrossMidnight(t *testing.T) {
	r := newRig(t)
	zone := time.FixedZone("plus9", 9*3600)
	add := func(fleet string, at time.Time, costs ...routines.Cost) {
		t.Helper()
		if _, err := r.store.AppendTrigger(fleet, routines.Trigger{Routine: "r1", Outcome: routines.Proposed, Tier: "T2", At: at, Costs: costs}); err != nil {
			t.Fatal(err)
		}
	}
	add("work", time.Date(2026, 11, 11, 14, 30, 0, 0, time.UTC), routines.Cost{Call: "lookout", USD: 1.00}) // 23:30 on the 11th at +9
	add("work", time.Date(2026, 11, 11, 15, 30, 0, 0, time.UTC), routines.Cost{Call: "lookout", USD: 0.25}) // 00:30 on the 12th at +9
	add("work", time.Date(2026, 11, 12, 5, 0, 0, 0, time.UTC), routines.Cost{Call: "lookout", USD: 0.10}, routines.Cost{Call: "reviewer", USD: 0.05})
	add("work", time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC))
	add("play", time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC), routines.Cost{Call: "lookout", USD: 9})
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	got, err := r.store.SpendToday("work", now, zone)
	if err != nil || got < 0.3999 || got > 0.4001 {
		t.Fatalf("SpendToday = %v, %v, want 0.40 (today's three costs at +09:00, not yesterday's, not another fleet's)", got, err)
	}
	if got, err := r.store.SpendToday("work", now, time.UTC); err != nil || got < 0.1499 || got > 0.1501 {
		t.Fatalf("SpendToday in UTC = %v, %v, want 0.15", got, err)
	}
}

func TestRateCountsComeFromTheFleetsLogIncludingScheduledLaunches(t *testing.T) {
	r := newRig(t)
	now := ago(0)
	add := func(tr routines.Trigger) {
		t.Helper()
		if _, err := r.store.AppendTrigger("work", tr); err != nil {
			t.Fatal(err)
		}
	}
	add(routines.Trigger{Routine: "cron", Outcome: routines.Launched, Tier: "T0", At: ago(30 * time.Minute)})
	add(routines.Trigger{Routine: "ev", Outcome: routines.Launched, Tier: "T2", Sender: "Bob", Event: "https://x/1", At: ago(10 * time.Minute)})
	add(routines.Trigger{Routine: "ev", Outcome: routines.Launched, Tier: "T2", Sender: "bob", Event: "https://x/2", At: ago(90 * time.Minute)})
	add(routines.Trigger{Routine: "ev", Outcome: routines.Launched, Tier: "T2", Sender: "bob", Event: "https://x/3", At: ago(13 * time.Hour)})
	add(routines.Trigger{Routine: "ev", Outcome: routines.Proposed, Tier: "T2", Sender: "bob", Event: "https://x/4", At: ago(5 * time.Minute)})
	got, err := r.store.CountsFor("work", "bob", now, time.UTC)
	want := standing.Counts{LaunchesLastHour: 2, SenderLaunchesToday: 2}
	if err != nil || got != want {
		t.Fatalf("CountsFor = %+v, %v, want %+v (launched records only; the hour counts the scheduled one; today counts bob's, yesterday's not)", got, err, want)
	}
}

func validOn() routines.Routine {
	in := launchRoutine()
	in.When = ""
	in.Before = routines.AskFirst
	in.On = &routines.On{Source: "gh", Kind: "review_requested"}
	return in
}

func (r *rig) catalog() {
	r.store.Events = func(fleet, source string) ([]routines.EventKind, bool) {
		if fleet != "work" || source != "gh" {
			return nil, false
		}
		return []routines.EventKind{{Kind: "review_requested", Title: "Review requested"}, {Kind: "new_in_view", Title: "New in a view", Panel: "mine"}}, true
	}
}

func TestAnEventRoutineSavesWithItsOnAndAKeyedFileKeepsCronRoutinesPlain(t *testing.T) {
	r := newRig(t)
	r.catalog()
	got := r.create("work", validOn())
	if got.On == nil || *got.On != (routines.On{Source: "gh", Kind: "review_requested"}) || got.When != "" {
		t.Fatalf("saved = %+v, want the on object kept and no when", got)
	}
	cron := r.create("work", launchRoutine())
	raw, err := os.ReadFile(filepath.Join(r.home, "routines", "work", "routines.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Routines []map[string]json.RawMessage `json:"routines"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	keyed := 0
	for _, x := range file.Routines {
		if on, ok := x["on"]; ok {
			keyed++
			var o routines.On
			if err := json.Unmarshal(on, &o); err != nil || o != (routines.On{Source: "gh", Kind: "review_requested"}) {
				t.Errorf("on = %s (%v), want the gh review_requested object", on, err)
			}
		}
	}
	if keyed != 1 {
		t.Errorf("%d routines in the file carry an on key, want 1 (a cron routine writes none): %s", keyed, raw)
	}
	list, err := r.store.List("work")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == cron.ID && (x.On != nil || x.When != "30 8 * * 1-5") {
			t.Errorf("the cron routine reads back as %+v, want it unchanged", x)
		}
	}
}

func TestAnEventRoutineIsRefusedWhenItsWhenIsNotExactlyOne(t *testing.T) {
	r := newRig(t)
	r.catalog()
	r.create("work", validOn())
	in := validOn()
	in.When = "0 9 * * *"
	if _, err := r.store.Create("work", in); !errors.Is(err, routines.ErrInvalid) {
		t.Errorf("a routine with both a schedule and an event = %v, want ErrInvalid", err)
	}
}

func TestAnEventRoutineNamesASourceAndKindTheFleetHas(t *testing.T) {
	r := newRig(t)
	r.catalog()
	r.create("work", validOn())
	for name, on := range map[string]routines.On{
		"unknown source":          {Source: "nope", Kind: "review_requested"},
		"unknown kind":            {Source: "gh", Kind: "starred"},
		"a panel kind with none":  {Source: "gh", Kind: "new_in_view"},
		"a panel kind, wrong one": {Source: "gh", Kind: "new_in_view", Panel: "other"},
		"a panel on a plain kind": {Source: "gh", Kind: "review_requested", Panel: "mine"},
		"empty source":            {Kind: "review_requested"},
		"empty kind":              {Source: "gh"},
	} {
		in := validOn()
		in.On = &on
		if _, err := r.store.Create("work", in); !errors.Is(err, routines.ErrInvalid) {
			t.Errorf("%s: Create = %v, want ErrInvalid", name, err)
		}
	}
	in := validOn()
	in.On = &routines.On{Source: "gh", Kind: "new_in_view", Panel: "mine"}
	r.create("work", in)
}

func TestAnEventRoutineWithoutACatalogForItsFleetIsRefused(t *testing.T) {
	r := newRig(t)
	r.catalog()
	r.create("work", validOn())
	if _, err := r.store.Create("play", validOn()); !errors.Is(err, routines.ErrInvalid) {
		t.Errorf("Create in a fleet with no such source = %v, want ErrInvalid", err)
	}
}

func TestAnEventRoutineNeverPromptsAdmiralBot(t *testing.T) {
	r := newRig(t)
	r.catalog()
	r.create("work", validOn())
	in := validOn()
	in.Do = routines.Do{Prompt: "look at the event"}
	_, err := r.store.Create("work", in)
	if !errors.Is(err, routines.ErrInvalid) || !strings.Contains(err.Error(), "prompt") {
		t.Errorf("an event routine with a prompt Do = %v, want ErrInvalid naming the prompt", err)
	}
	cron := launchRoutine()
	cron.Do = routines.Do{Prompt: "look at the board"}
	r.create("work", cron) // a scheduled routine still may prompt
}

// the event routine harness

func (h *harness) withEvents(limits standing.Limits) {
	h.t.Helper()
	h.catalog()
	base := h.store.Fleets
	h.store.Fleets = func(k string) (config.Fleet, bool) {
		f, ok := base(k)
		f.Limits = limits
		return f, ok
	}
}

func (h *harness) onRoutine(before routines.Mode, mod func(*routines.Routine)) routines.Routine {
	h.t.Helper()
	in := validOn()
	in.Before = before
	if mod != nil {
		mod(&in)
	}
	return h.create("work", in)
}

func evFrom(actor string) routines.Event {
	return routines.Event{Fleet: "work", Source: "gh", Kind: "review_requested", Ref: "https://github.com/acme/api/pull/7",
		Actor: actor, Author: actor, Viewer: "Eng", Verified: true}
}

func TestAnEventReachesAMatchingRoutineAndWritesItsRecordWithoutLaunching(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.Act, nil)
	h.sched.HandleEvent(evFrom("Eng"))
	recs := h.records(r)
	if len(recs) != 1 {
		t.Fatalf("%d records, want 1: %+v", len(recs), recs)
	}
	got := recs[0]
	if got.Outcome != routines.Proposed || got.Tier != "T1" || got.Event != "https://github.com/acme/api/pull/7" || got.Sender != "Eng" ||
		got.Rule != r.ID || !strings.Contains(got.Reason, routines.ReasonTop) {
		t.Errorf("record = %+v, want proposed, T1, the ref, sender Eng, rule %s and the top as the reason", got, r.ID)
	}
	h.wantLaunches(0)
	if n := len(h.proposer.proposals()); n != 0 {
		t.Errorf("%d proposals through today's Proposer, want none: an event proposal is not a scheduled run's", n)
	}
}

func TestReportOnlyEventRoutineWritesAReportedRecord(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.ReportOnly, nil)
	h.sched.HandleEvent(evFrom("Eng"))
	if recs := h.records(r); len(recs) != 1 || recs[0].Outcome != routines.Reported {
		t.Fatalf("records = %+v, want one reported", recs)
	}
}

func TestAnEventOnlyReachesTheEnabledRoutinesItMatches(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	match := h.onRoutine(routines.AskFirst, nil)
	viewRoutine := h.onRoutine(routines.AskFirst, func(r *routines.Routine) { r.On = &routines.On{Source: "gh", Kind: "new_in_view", Panel: "mine"} })
	off := h.onRoutine(routines.AskFirst, func(r *routines.Routine) { r.Enabled = false })
	cron := h.act(nil)
	for _, ev := range []routines.Event{
		{Fleet: "work", Source: "gh", Kind: "new_in_view", Panel: "other", Ref: "https://github.com/acme/api/issues/1", Actor: "Eng", Author: "Eng", Viewer: "Eng", Verified: true},
		{Fleet: "work", Source: "other", Kind: "review_requested", Ref: "https://github.com/acme/api/pull/8", Actor: "Eng", Author: "Eng", Viewer: "Eng", Verified: true},
		{Fleet: "play", Source: "gh", Kind: "review_requested", Ref: "https://github.com/acme/api/pull/9", Actor: "Eng", Author: "Eng", Viewer: "Eng", Verified: true},
	} {
		h.sched.HandleEvent(ev)
	}
	h.sched.HandleEvent(evFrom("Eng")) // the matching one, so a stub that records nothing cannot pass
	if n := len(h.records(match)); n != 1 {
		t.Errorf("the matching routine has %d records, want 1", n)
	}
	for name, r := range map[string]routines.Routine{"other panel": viewRoutine, "disabled": off, "cron": cron} {
		if n := len(h.records(r)); n != 0 {
			t.Errorf("the %s routine has %d records, want 0", name, n)
		}
	}
}

func TestAnEventRecordsTheTierGoComputesFromTheEngineersAllowlist(t *testing.T) {
	h := newHarness(t)
	limits := standing.PresetLimits(standing.Home)
	limits.SenderAllowlist = []string{"alice"}
	h.withEvents(limits)
	r := h.onRoutine(routines.AskFirst, nil)
	unverified := evFrom("Eng")
	unverified.Verified = false
	for _, ev := range []routines.Event{evFrom("Eng"), evFrom("Alice"), evFrom("stranger"), unverified} {
		h.sched.HandleEvent(ev)
	}
	var tiers []string
	for _, rec := range slices.Backward(h.records(r)) {
		tiers = append(tiers, rec.Tier)
	}
	if want := []string{"T1", "T2", "T3", "T3"}; !slices.Equal(tiers, want) {
		t.Errorf("tiers = %v, want %v (engineer, allowlisted, stranger, unverified engineer)", tiers, want)
	}
}

func TestAnEventAboutAdmiralsOwnWorkProducesNoRecord(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.AskFirst, nil)
	own := "https://github.com/acme/api/pull/7"
	h.sched.Own = func(fleet, ref string) bool { return fleet == "work" && ref == own }
	h.sched.HandleEvent(evFrom("Eng"))
	if n := len(h.records(r)); n != 0 {
		t.Fatalf("%d records for an event about a ship's own PR, want 0", n)
	}
	other := evFrom("Eng")
	other.Ref = "https://github.com/acme/api/pull/8"
	h.sched.HandleEvent(other)
	if n := len(h.records(r)); n != 1 {
		t.Errorf("%d records for another PR, want 1", n)
	}
}

func TestALookoutsFieldsEnterTheDecisionAndTheRecord(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.AskFirst, nil)
	h.sched.Enrich = func(routines.Event) routines.Enrichment {
		return routines.Enrichment{DedupeKey: "k1", Repo: "acme/api", Costs: []routines.Cost{{Call: "lookout", USD: 0.05}}}
	}
	h.sched.HandleEvent(evFrom("Eng"))
	recs := h.records(r)
	if len(recs) != 1 || recs[0].DedupeKey != "k1" || len(recs[0].Costs) != 1 || recs[0].Costs[0] != (routines.Cost{Call: "lookout", USD: 0.05}) {
		t.Fatalf("records = %+v, want one carrying the dedupe key and the cost", recs)
	}
}

func TestALookoutsDedupeKeyCanMatchASkipFilterForTheRoutinesID(t *testing.T) {
	h := newHarness(t)
	limits := standing.PresetLimits(standing.Home)
	h.withEvents(limits)
	r := h.onRoutine(routines.AskFirst, nil)
	limits.Skip.DedupeKeys = []standing.Scoped{{Routine: r.ID, Value: "k1"}}
	h.withEvents(limits)
	h.sched.Enrich = func(ev routines.Event) routines.Enrichment {
		if strings.HasSuffix(ev.Ref, "/7") {
			return routines.Enrichment{DedupeKey: "k1"}
		}
		return routines.Enrichment{DedupeKey: "k2"}
	}
	h.sched.HandleEvent(evFrom("Eng"))
	second := evFrom("Eng")
	second.Ref = "https://github.com/acme/api/pull/8"
	h.sched.HandleEvent(second)
	var skipped, proposed int
	for _, rec := range h.records(r) {
		switch rec.Outcome {
		case routines.Skipped:
			skipped++
			if !strings.Contains(rec.Reason, routines.ReasonSkipFilter) || rec.DedupeKey != "k1" {
				t.Errorf("skipped record = %+v, want the skip-filter reason and key k1", rec)
			}
		case routines.Proposed:
			proposed++
		}
	}
	if skipped != 1 || proposed != 1 {
		t.Errorf("%d skipped and %d proposed, want 1 and 1", skipped, proposed)
	}
}

func TestSpendReachedTodayShowsInTheEventRecord(t *testing.T) {
	h := newHarness(t)
	limits := standing.PresetLimits(standing.Home)
	h.withEvents(limits)
	r := h.onRoutine(routines.Act, nil)
	h.sched.HandleEvent(evFrom("Eng"))
	if rec := h.latest(r); strings.Contains(rec.Reason, routines.ReasonSpend) {
		t.Fatalf("a first event under the cap says %q", rec.Reason)
	}
	if _, err := h.store.AppendTrigger("work", routines.Trigger{Routine: r.ID, Outcome: routines.Proposed, Tier: "T1", At: day0,
		Costs: []routines.Cost{{Call: "lookout", USD: limits.Spend.DailyWorkerUSD}}}); err != nil {
		t.Fatal(err)
	}
	second := evFrom("Eng")
	second.Ref = "https://github.com/acme/api/pull/8"
	h.sched.HandleEvent(second)
	if rec := h.latest(r); rec.Outcome != routines.Proposed || !strings.Contains(rec.Reason, routines.ReasonSpend) {
		t.Errorf("record = %+v, want proposed with the spend reason", rec)
	}
}

func TestQuietHoursAreReadOnTheSchedulersZoneClock(t *testing.T) {
	h := newHarness(t)
	limits := standing.PresetLimits(standing.Home)
	limits.QuietHours = []standing.Window{{Weekdays: []string{"thu"}, Start: "09:00", End: "11:00"}}
	h.withEvents(limits)
	plus10 := time.FixedZone("plus10", 10*3600)
	h.sched.Zone = plus10
	r := h.onRoutine(routines.Act, nil)
	h.clock.set(time.Date(2026, 11, 12, 2, 0, 0, 0, time.UTC)) // 12:00 Thursday at +10
	h.sched.HandleEvent(evFrom("Eng"))
	if rec := h.latest(r); strings.Contains(rec.Reason, routines.ReasonQuiet) {
		t.Fatalf("at 12:00 local the record says %q", rec.Reason)
	}
	h.clock.set(time.Date(2026, 11, 11, 23, 30, 0, 0, time.UTC)) // 09:30 Thursday at +10, Wednesday in UTC
	second := evFrom("Eng")
	second.Ref = "https://github.com/acme/api/pull/8"
	h.sched.HandleEvent(second)
	if rec := h.latest(r); !strings.Contains(rec.Reason, routines.ReasonQuiet) {
		t.Errorf("record = %+v, want the quiet-hours reason at 09:30 local", rec)
	}
}

func TestHoldFireShowsInTheEventRecord(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.Act, nil)
	if _, err := h.store.SetHold("work", true, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	h.sched.HandleEvent(evFrom("Eng"))
	if rec := h.latest(r); rec.Outcome != routines.Proposed || !strings.Contains(rec.Reason, routines.ReasonHold) {
		t.Errorf("record = %+v, want proposed with the hold-fire reason", rec)
	}
}

type fakeActs struct{ got []routines.Trigger }

func (f *fakeActs) Act(_ string, _ routines.Routine, _ routines.Event, rec routines.Trigger, _ routines.Decision, _ routines.Enrichment) {
	f.got = append(f.got, rec)
}

func TestAReportedOrProposedDecisionIsHandedToTheActsSeamAfterItsRecordIsWritten(t *testing.T) {
	h := newHarness(t)
	limits := standing.PresetLimits(standing.Home)
	limits.RepoAllowlist = []string{"acme/api"}
	h.withEvents(limits)
	r := h.onRoutine(routines.AskFirst, nil)
	acts := &fakeActs{}
	h.sched.Acts = acts
	h.sched.Enrich = func(ev routines.Event) routines.Enrichment {
		if strings.HasSuffix(ev.Ref, "/8") {
			return routines.Enrichment{Repo: "acme/web"} // skipped by the allowlist
		}
		return routines.Enrichment{Repo: "acme/api"}
	}
	h.sched.HandleEvent(evFrom("Eng"))
	skipped := evFrom("Eng")
	skipped.Ref = "https://github.com/acme/web/pull/8"
	h.sched.HandleEvent(skipped)
	recs := h.records(r)
	if len(recs) != 2 {
		t.Fatalf("%d records, want 2: %+v", len(recs), recs)
	}
	if len(acts.got) != 1 || acts.got[0].Outcome != routines.Proposed || acts.got[0].ID == "" || acts.got[0].ID != recs[1].ID {
		t.Errorf("Acts got %+v, want the one proposed record %s and not the skipped one", acts.got, recs[1].ID)
	}
}

func TestAnEventRoutineIsNeverFiredByTheClock(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.Act, nil)
	h.tickAt(at(0, 9, 0))
	h.tickAt(at(1, 9, 0))
	if err := h.sched.CatchUp(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.wantLaunches(0)
	if n := len(h.records(r)); n != 0 {
		t.Errorf("%d records from ticks and catch-up, want 0: %+v", n, h.records(r))
	}
}

func TestAnEventWhoseActorIsNoLoginReadsAsAStrangersEvenOnTheEngineersItem(t *testing.T) {
	ev := routines.Event{Actor: "(unknown)", Author: "Eng", Viewer: "Eng", Verified: true}
	if got := routines.TierOf(ev, standing.PresetLimits(standing.Home)); got != standing.T3 {
		t.Errorf("tier = %v, want T3", got)
	}
}

func TestAnEventSaysWhoSentItAndANewInViewEventIsAtBestAnAllowlistedAuthorsT2(t *testing.T) {
	limits := standing.Limits{SenderAllowlist: []string{"Alice"}}
	view := func(author string) routines.Event {
		return routines.Event{Kind: "new_in_view", Panel: "mine", Author: author, Viewer: "Eng", Verified: true}
	}
	for name, c := range map[string]struct {
		ev   routines.Event
		want standing.Tier
	}{
		"a stranger's item entering the view":       {view("stranger"), standing.T3},
		"the engineer's own item entering the view": {view("Eng"), standing.T3},
		"an allowlisted author's item":              {view("alice"), standing.T2},
		"a ghost author's item":                     {view(""), standing.T3},
		"an unattributable author's item":           {view("(unknown)"), standing.T3},
		"a plugin's new_in_view with no actor":      {routines.Event{Kind: "new_in_view", Author: "alice"}, standing.T3},
	} {
		if got := routines.TierOf(c.ev, limits); got != c.want {
			t.Errorf("%s: tier = T%d, want T%d", name, got, c.want)
		}
	}
}

func TestANewInViewRecordNamesTheItemsAuthorAndASenderSkipMatchesIt(t *testing.T) {
	h := newHarness(t)
	limits := standing.PresetLimits(standing.Home)
	h.withEvents(limits)
	r := h.onRoutine(routines.AskFirst, func(r *routines.Routine) { r.On = &routines.On{Source: "gh", Kind: "new_in_view", Panel: "mine"} })
	view := routines.Event{Fleet: "work", Source: "gh", Kind: "new_in_view", Panel: "mine", Ref: "https://github.com/acme/api/pull/9",
		Author: "stranger", Viewer: "Eng", Verified: true}
	h.sched.HandleEvent(view)
	if rec := h.latest(r); rec.Sender != "stranger" || rec.Tier != "T3" {
		t.Fatalf("record = %+v, want sender stranger at T3", rec)
	}
	limits.Skip.Senders = []standing.Scoped{{Routine: r.ID, Value: "stranger"}}
	h.withEvents(limits)
	h.sched.HandleEvent(view)
	if rec := h.latest(r); rec.Outcome != routines.Skipped || !strings.Contains(rec.Reason, routines.ReasonSkipFilter) {
		t.Errorf("record = %+v, want skipped by the sender filter", rec)
	}
}

func TestAnUnattributedActorFallsBackToTheAuthorAsSenderElseUnknown(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.AskFirst, nil)
	ev := evFrom("")
	ev.Actor, ev.Author = routines.UnknownLogin, "amy"
	h.sched.HandleEvent(ev)
	if rec := h.latest(r); rec.Sender != "amy" || rec.Tier != "T3" {
		t.Errorf("record = %+v, want sender amy at T3", rec)
	}
	ev.Actor, ev.Author = "", ""
	h.sched.HandleEvent(ev)
	if rec := h.latest(r); rec.Sender != routines.UnknownLogin || rec.Tier != "T3" {
		t.Errorf("record = %+v, want sender (unknown) at T3", rec)
	}
}

func TestWhenTheDailySpendIsReachedTheLookoutIsNotAskedAndTheRecordSaysWhy(t *testing.T) {
	h := newHarness(t)
	limits := standing.PresetLimits(standing.Home)
	h.withEvents(limits)
	r := h.onRoutine(routines.ReportOnly, nil)
	if _, err := h.store.AppendTrigger("work", routines.Trigger{Routine: r.ID, Outcome: routines.Reported, Tier: "T1", At: day0,
		Costs: []routines.Cost{{Call: "lookout", USD: limits.Spend.DailyWorkerUSD}}}); err != nil {
		t.Fatal(err)
	}
	asked := 0
	h.sched.Enrich = func(routines.Event) routines.Enrichment { asked++; return routines.Enrichment{} }
	h.sched.HandleEvent(evFrom("Eng"))
	if asked != 0 {
		t.Errorf("Enrich called %d times with the spend reached, want 0", asked)
	}
	if rec := h.latest(r); rec.Outcome != routines.Reported || !strings.Contains(rec.Reason, routines.ReasonSpend) {
		t.Errorf("record = %+v, want reported with the spend reason", rec)
	}
}

func TestAMutedSenderCostsNoLookoutCallAndADedupeSkipStillNeedsOne(t *testing.T) {
	h := newHarness(t)
	limits := standing.PresetLimits(standing.Home)
	h.withEvents(limits)
	r := h.onRoutine(routines.AskFirst, nil)
	limits.Skip.Senders = []standing.Scoped{{Routine: r.ID, Value: "bob"}}
	h.withEvents(limits)
	asked := 0
	h.sched.Enrich = func(routines.Event) routines.Enrichment { asked++; return routines.Enrichment{} }
	h.sched.HandleEvent(evFrom("bob"))
	if asked != 0 {
		t.Errorf("Enrich called %d times for a muted sender, want 0", asked)
	}
	if rec := h.latest(r); rec.Outcome != routines.Skipped {
		t.Errorf("record = %+v, want skipped", rec)
	}
	h.sched.HandleEvent(evFrom("Eng"))
	if asked != 1 {
		t.Errorf("Enrich called %d times for an unmuted sender, want 1", asked)
	}
}

func TestTheEnrichmentReachesTheActsSeam(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	h.onRoutine(routines.AskFirst, nil)
	var got routines.Enrichment
	h.sched.Acts = actsFunc(func(en routines.Enrichment) { got = en })
	h.sched.Enrich = func(routines.Event) routines.Enrichment { return routines.Enrichment{Summary: "a summary"} }
	h.sched.HandleEvent(evFrom("Eng"))
	if got.Summary != "a summary" {
		t.Errorf("Act got %+v, want the Lookout's summary", got)
	}
}

type actsFunc func(routines.Enrichment)

func (f actsFunc) Act(_ string, _ routines.Routine, _ routines.Event, _ routines.Trigger, _ routines.Decision, en routines.Enrichment) {
	f(en)
}

func TestAnEventRoutineWhoseDoIsAPromptNeverReachesTheActs(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.AskFirst, nil)
	list, _ := h.store.List("work")
	var path string
	matches, _ := filepath.Glob(filepath.Join(h.home, "*", "*", "routines.json"))
	for _, m := range matches {
		path = m
	}
	if path == "" || len(list) != 1 {
		t.Skip("routines file not found: " + h.home)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), `"launch"`, `"prompt":"do it","x"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	acts := &fakeActs{}
	h.sched.Acts = acts
	h.sched.HandleEvent(evFrom("Eng"))
	if len(acts.got) != 0 || len(h.records(r)) != 0 {
		t.Errorf("acts %+v and records %+v, want neither for a routine whose Do prompts", acts.got, h.records(r))
	}
}

func TestAnUnreadableTriggerLogCountsSpendAsUnknownAndHoldsAtA1(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.Act, nil)
	h.sched.HandleEvent(evFrom("Eng"))
	matches, _ := filepath.Glob(filepath.Join(h.home, "*", "*", "triggers.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("trigger log not found under %s: %v", h.home, matches)
	}
	if err := os.Chmod(matches[0], 0o200); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(matches[0], 0o600) })
	if f, err := os.Open(matches[0]); err == nil {
		f.Close()
		t.Skip("the log is readable despite its mode, so the read error can't be made")
	}
	asked := 0
	h.sched.Enrich = func(routines.Event) routines.Enrichment { asked++; return routines.Enrichment{} }
	h.sched.HandleEvent(evFrom("Eng"))
	os.Chmod(matches[0], 0o600)
	if asked != 0 {
		t.Errorf("Enrich called %d times with the log unreadable, want 0", asked)
	}
	rec := h.latest(r)
	if !strings.Contains(rec.Reason, routines.ReasonSpendRead) || rec.Outcome != routines.Proposed {
		t.Errorf("record = %+v, want proposed and the unknown-spend reason", rec)
	}
}

func TestAPluginsEventNamesNoRepoSoANonEmptyRepoAllowlistSkipsItAndAnEmptyOneIsSkippedToo(t *testing.T) {
	limits := standing.PresetLimits(standing.Home)
	limits.RepoAllowlist = []string{"acme/api"}
	h := newHarness(t)
	h.withEvents(limits)
	r := h.onRoutine(routines.AskFirst, nil)
	plugin := evFrom("alice")
	plugin.Verified, plugin.Viewer = false, ""
	h.sched.HandleEvent(plugin)
	if rec := h.latest(r); rec.Outcome != routines.Skipped || !strings.Contains(rec.Reason, routines.ReasonRepo) {
		t.Errorf("a plugin event on a github.com ref: record = %+v, want skipped by the repo allowlist", rec)
	}
	h.sched.HandleEvent(evFrom("Eng"))
	if rec := h.latest(r); rec.Outcome != routines.Proposed {
		t.Errorf("a verified event on an allowlisted repo: record = %+v, want proposed", rec)
	}
	empty := evFrom("Eng")
	empty.Ref = "https://example.com/x"
	h.sched.HandleEvent(empty)
	if rec := h.latest(r); rec.Outcome != routines.Skipped || !strings.Contains(rec.Reason, routines.ReasonRepo) {
		t.Errorf("a verified event with no repo: record = %+v, want skipped", rec)
	}
}

func TestAnEventRoutineUpdatesWhileItsSourceHasNoCatalogIfItsOnIsUnchanged(t *testing.T) {
	r := newRig(t)
	r.catalog()
	saved := r.create("work", validOn())
	r.store.Events = func(string, string) ([]routines.EventKind, bool) { return nil, false }
	saved.Name = "renamed"
	if _, err := r.store.Update("work", saved.ID, saved); err != nil {
		t.Errorf("Update with the same on and no catalog = %v, want ok", err)
	}
	saved.On = &routines.On{Source: "gh", Kind: "new_in_view", Panel: "mine"}
	if _, err := r.store.Update("work", saved.ID, saved); !errors.Is(err, routines.ErrInvalid) {
		t.Errorf("Update with a changed on and no catalog = %v, want ErrInvalid", err)
	}
}
