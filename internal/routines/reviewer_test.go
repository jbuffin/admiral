package routines_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

const (
	checkOne = "Always run make check before a PR."
	checkTwo = "Never touch the release workflow."
	// the first 8 hex of the sha256 of each text
	idOne = "0bbdf84d"
	idTwo = "a5250c60"
)

// reviewed is the harness with a proven restricted harness, a fleet with two Checks and a scripted reviewer that logs its inputs.
type reviewed struct {
	*harness
	mu     sync.Mutex
	inputs []routines.ReviewInput
	reply  func(routines.ReviewInput) routines.Review
	r      routines.Routine
}

func answers(v standing.Verdict, id, reason string, costs ...routines.Cost) func(routines.ReviewInput) routines.Review {
	return func(routines.ReviewInput) routines.Review {
		return routines.Review{Verdict: v, CheckID: id, Reason: reason, Costs: costs}
	}
}

func newReviewed(t *testing.T) *reviewed {
	t.Helper()
	h := newHarness(t)
	h.withEvents(a3Limits())
	v := &reviewed{harness: h, reply: answers(standing.Allow, "", "fits")}
	v.r = h.actRoutine()
	h.proveRestricted("2.1.300", true, true)
	v.setChecks(checkOne, checkTwo)
	h.sched.Review = func(in routines.ReviewInput) routines.Review {
		v.mu.Lock()
		v.inputs = append(v.inputs, in)
		reply := v.reply
		v.mu.Unlock()
		return reply(in)
	}
	return v
}

func (v *reviewed) setChecks(checks ...string) {
	v.t.Helper()
	if checks == nil {
		checks = []string{}
	}
	if _, err := v.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &checks}); err != nil {
		v.t.Fatal(err)
	}
}

func (v *reviewed) calls() []routines.ReviewInput {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]routines.ReviewInput(nil), v.inputs...)
}

// send handles one event from the engineer and returns the decisions the acts saw and the routine's latest record.
func (v *reviewed) send() ([]routines.Decision, routines.Trigger) {
	v.t.Helper()
	acts := &decisionActs{}
	v.sched.Acts = acts
	v.sched.HandleEvent(evFrom("Eng"))
	return acts.got, v.latest(v.r)
}

func TestAnA3RoutineInAFleetWithChecksIsJudgedAndAllowKeepsItLaunchedAtA3WithTheVerdictRecorded(t *testing.T) {
	v := newReviewed(t)
	v.reply = answers(standing.Allow, idOne, "runs make check first", routines.Cost{Call: "reviewer", USD: 0.04})
	_, rec := v.send()
	calls := v.calls()
	if len(calls) != 1 {
		t.Fatalf("%d reviewer calls, want 1", len(calls))
	}
	in := calls[0]
	if in.Level != standing.A3 || in.Restricted != standing.DraftFix || in.Routine.ID != v.r.ID || in.Fleet != "work" ||
		len(in.Checks) != 2 || in.Checks[0] != checkOne || in.Checks[1] != checkTwo || in.Event.Ref != evFrom("Eng").Ref {
		t.Errorf("reviewer input = %+v, want the provisional A3 draft_fix action, the routine, the event and both Checks", in)
	}
	if rec.Outcome != routines.Launched || rec.Restricted != standing.DraftFix {
		t.Errorf("record = %+v, want a launched draft_fix ship at A3", rec)
	}
	if rec.Verdict != "allow" || rec.CheckID != idOne || rec.ReviewerReason != "runs make check first" {
		t.Errorf("record verdict, check and reason = %q, %q, %q, want allow, %s and the reviewer's reason", rec.Verdict, rec.CheckID, rec.ReviewerReason, idOne)
	}
	if len(rec.Costs) != 1 || rec.Costs[0] != (routines.Cost{Call: "reviewer", USD: 0.04}) {
		t.Errorf("record costs = %+v, want the reviewer's 0.04 counted toward the day's spend", rec.Costs)
	}
}

func TestAnAskVerdictProposesAtA1AndHandsTheActsTheChecksTextAndReason(t *testing.T) {
	v := newReviewed(t)
	v.reply = answers(standing.Ask, idTwo, "touches the release path")
	got, rec := v.send()
	if rec.Outcome != routines.Proposed || rec.Verdict != "ask" || rec.CheckID != idTwo || rec.ReviewerReason != "touches the release path" {
		t.Fatalf("record = %+v, want proposed with verdict ask, check %s and the reason", rec, idTwo)
	}
	if len(got) != 1 {
		t.Fatalf("%d decisions reached the acts, want 1", len(got))
	}
	want := routines.Reviewed{Verdict: "ask", CheckID: idTwo, CheckText: checkTwo, Reason: "touches the release path"}
	if got[0].Level != standing.A1 || got[0].Reviewed != want {
		t.Errorf("decision = level A%d, reviewed %+v, want A1 and %+v", got[0].Level, got[0].Reviewed, want)
	}
}

func TestABlockVerdictReportsAtA0WithTheVerdictCheckAndReasonRecorded(t *testing.T) {
	v := newReviewed(t)
	v.reply = answers(standing.Block, idOne, "skips make check")
	got, rec := v.send()
	if rec.Outcome != routines.Reported || rec.Verdict != "block" || rec.CheckID != idOne || rec.ReviewerReason != "skips make check" {
		t.Fatalf("record = %+v, want reported with verdict block, check %s and the reason", rec, idOne)
	}
	if len(got) != 1 || got[0].Level != standing.A0 || got[0].Reviewed.CheckText != checkOne {
		t.Errorf("decisions = %+v, want one at A0 carrying the text of the first Check", got)
	}
}

func TestTheReviewersReasonStaysOutOfTheDecisionReasonCodes(t *testing.T) {
	v := newReviewed(t)
	v.reply = answers(standing.Ask, idOne, "a distinctive reviewer reason")
	_, rec := v.send()
	if rec.ReviewerReason != "a distinctive reviewer reason" || strings.Contains(rec.Reason, "distinctive") {
		t.Errorf("record reason = %q and reviewer reason = %q, want the reviewer's words in their own field, not folded into the reason codes", rec.Reason, rec.ReviewerReason)
	}
}

// outcome is one event sent through a variation of the shared setup: how many times the reviewer was asked and the record.
type outcome struct {
	calls int
	rec   routines.Trigger
	got   []routines.Decision
}

func run(t *testing.T, setup func(*reviewed)) outcome {
	t.Helper()
	v := newReviewed(t)
	v.reply = answers(standing.Ask, idOne, "judged")
	if setup != nil {
		setup(v)
	}
	got, rec := v.send()
	return outcome{calls: len(v.calls()), rec: rec, got: got}
}

// wantNoCall holds the variation to no reviewer call and no verdict, after the shared setup alone is shown to make one.
func wantNoCall(t *testing.T, setup func(*reviewed)) outcome {
	t.Helper()
	if control := run(t, nil); control.calls != 1 {
		t.Fatalf("control: %d reviewer calls, want 1 before the variation can show none", control.calls)
	}
	got := run(t, setup)
	if got.calls != 0 || got.rec.Verdict != "" || got.rec.CheckID != "" || got.rec.ReviewerReason != "" {
		t.Errorf("%d reviewer calls and record %+v, want no call and no verdict", got.calls, got.rec)
	}
	return got
}

func TestNoReviewerCallInAFleetWithNoChecks(t *testing.T) {
	got := wantNoCall(t, func(v *reviewed) { v.setChecks() })
	if got.rec.Outcome != routines.Launched {
		t.Errorf("outcome = %s, want the A3 launch to go ahead", got.rec.Outcome)
	}
}

func TestNoReviewerCallForARoutineWhoseProvisionalLevelIsA1(t *testing.T) {
	wantNoCall(t, func(v *reviewed) {
		v.r = v.onRoutine(routines.AskFirst, func(r *routines.Routine) { r.Name = "Ask first sweep" })
		if err := v.store.Delete("work", v.actRoutineID()); err != nil {
			v.t.Fatal(err)
		}
	})
}

func (v *reviewed) actRoutineID() string {
	list, err := v.store.List("work")
	if err != nil {
		v.t.Fatal(err)
	}
	for _, r := range list {
		if r.Before == routines.Act {
			return r.ID
		}
	}
	v.t.Fatal("no Act routine")
	return ""
}

func TestNoReviewerCallWhenABudgetAlreadyHoldsTheRoutineAtA1(t *testing.T) {
	t.Run("a failed Lookout", func(t *testing.T) {
		wantNoCall(t, func(v *reviewed) {
			v.sched.Enrich = func(routines.Event) routines.Enrichment { return routines.Enrichment{Failure: "timeout"} }
		})
	})
	t.Run("quiet hours", func(t *testing.T) {
		wantNoCall(t, func(v *reviewed) {
			all := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
			limits := a3Limits()
			limits.QuietHours = []standing.Window{{Start: "00:00", End: "23:59", Weekdays: all}}
			v.withEvents(limits)
		})
	})
}

func TestChecksThatCannotBeReadMeanAskWithNoCallAndTheReasonRecorded(t *testing.T) {
	v := newReviewed(t)
	path := filepath.Join(v.home, "routines", "work", "standing-orders.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, rec := v.send()
	if n := len(v.calls()); n != 0 {
		t.Errorf("%d reviewer calls, want none when the Checks can't be read", n)
	}
	if rec.Outcome != routines.Proposed || rec.Verdict != "ask" || !strings.Contains(rec.ReviewerReason, "checks unreadable") {
		t.Errorf("record = %+v, want proposed with verdict ask and the reason \"checks unreadable\"", rec)
	}
	if len(got) != 1 || got[0].Reviewed.Verdict != "ask" || got[0].Reviewed.CheckText != "" {
		t.Errorf("decisions = %+v, want one ask with no Check text", got)
	}
}

func TestAFleetWithChecksAndNoReviewerWiredAsksInsteadOfLaunching(t *testing.T) {
	v := newReviewed(t)
	v.sched.Review = nil
	_, rec := v.send()
	if rec.Outcome != routines.Proposed || rec.Verdict != "ask" {
		t.Errorf("record = %+v, want proposed with verdict ask: no reviewer fails closed", rec)
	}
}

func TestDailySpendReachedByThisEventsEarlierCostsMeansAskWithNoCall(t *testing.T) {
	v := newReviewed(t)
	limits := a3Limits()
	limits.Spend.DailyWorkerUSD = 1.00
	v.withEvents(limits)
	v.sched.Enrich = func(routines.Event) routines.Enrichment {
		return routines.Enrichment{Costs: []routines.Cost{{Call: "lookout", USD: 1.00}}}
	}
	_, rec := v.send()
	if n := len(v.calls()); n != 0 {
		t.Errorf("%d reviewer calls, want none once the Lookout's cost reached the day's cap", n)
	}
	if rec.Outcome != routines.Proposed || rec.Verdict != "ask" || !strings.Contains(rec.ReviewerReason, "daily spend") {
		t.Errorf("record = %+v, want proposed, verdict ask and a reviewer reason naming the daily spend", rec)
	}
}

func TestTwoA3RoutinesOnOneEventAreJudgedTwiceEachCostOnItsOwnRecordAndTheLookoutsOnTheFirstOnly(t *testing.T) {
	v := newReviewed(t)
	second := v.onRoutine(routines.Act, func(r *routines.Routine) { r.Name = "Second sweep" })
	v.reply = answers(standing.Ask, idOne, "judged", routines.Cost{Call: "reviewer", USD: 0.04})
	v.sched.Enrich = func(routines.Event) routines.Enrichment {
		return routines.Enrichment{Costs: []routines.Cost{{Call: "lookout", USD: 0.02}}}
	}
	v.sched.HandleEvent(evFrom("Eng"))
	if n := len(v.calls()); n != 2 {
		t.Fatalf("%d reviewer calls, want one per routine", n)
	}
	var lookouts, reviewers int
	for _, r := range []routines.Routine{v.r, second} {
		rec := v.latest(r)
		var own int
		for _, c := range rec.Costs {
			switch c.Call {
			case "lookout":
				lookouts++
			case "reviewer":
				own++
				reviewers++
			}
		}
		if own != 1 {
			t.Errorf("record of %s has costs %+v, want its own one reviewer cost", r.Name, rec.Costs)
		}
		if rec.Verdict != "ask" {
			t.Errorf("record of %s has verdict %q, want ask", r.Name, rec.Verdict)
		}
	}
	if lookouts != 1 || reviewers != 2 {
		t.Errorf("%d Lookout and %d reviewer costs across the two records, want 1 and 2", lookouts, reviewers)
	}
}

func TestAnEarlierReviewerCostOfThisEventCountsTowardTheDailySpendForTheNextRoutine(t *testing.T) {
	v := newReviewed(t)
	second := v.onRoutine(routines.Act, func(r *routines.Routine) { r.Name = "Second sweep" })
	limits := a3Limits()
	limits.Spend.DailyWorkerUSD = 0.15
	v.withEvents(limits)
	v.reply = answers(standing.Ask, idOne, "judged", routines.Cost{Call: "reviewer", USD: 0.15})
	v.sched.HandleEvent(evFrom("Eng"))
	if n := len(v.calls()); n != 1 {
		t.Errorf("%d reviewer calls, want 1: the first call's 0.15 reached the cap before the second routine", n)
	}
	var spent int
	for _, r := range []routines.Routine{v.r, second} {
		if rec := v.latest(r); strings.Contains(rec.ReviewerReason, "daily spend") {
			spent++
		}
	}
	if spent != 1 {
		t.Errorf("%d records name the daily spend as the reviewer reason, want exactly the second routine's", spent)
	}
}

func TestASlowReviewerDoesNotHoldTheSchedulerLock(t *testing.T) {
	v := newReviewed(t)
	entered, release := make(chan struct{}), make(chan struct{})
	v.sched.Review = func(routines.ReviewInput) routines.Review {
		close(entered)
		<-release
		return routines.Review{Verdict: standing.Ask, CheckID: idOne, Reason: "slow"}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.sched.HandleEvent(evFrom("Eng"))
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-done
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the reviewer was never asked")
	}
	ticked := make(chan error, 1)
	go func() { ticked <- v.sched.Tick(context.Background()) }() // Tick takes the scheduler lock
	select {
	case <-ticked:
	case <-time.After(5 * time.Second):
		t.Error("Tick blocked while the reviewer was thinking: the call holds the scheduler lock")
	}
	close(release)
}

func TestLaunchingAnAcceptedProposalMakesNoReviewerCall(t *testing.T) {
	v := newReviewed(t)
	v.reply = answers(standing.Ask, idOne, "judged")
	_, rec := v.send()
	if rec.Outcome != routines.Proposed || len(v.calls()) != 1 {
		t.Fatalf("record = %+v after %d reviewer calls, want a proposal made by one call", rec, len(v.calls()))
	}
	if _, err := v.sched.LaunchProposed(context.Background(), "work", v.r.ID, rec.ID); err != nil {
		t.Fatalf("LaunchProposed = %v", err)
	}
	if n := len(v.calls()); n != 1 {
		t.Errorf("%d reviewer calls after accepting the proposal, want still 1", n)
	}
}

func TestARoutineWhoseBreakerTripsOnThisEventIsNeitherJudgedNorLaunchedAtA3(t *testing.T) {
	v := newReviewed(t)
	for d := 1; d <= 3; d++ {
		if _, err := v.store.AppendTrigger("work", routines.Trigger{Routine: v.r.ID, Outcome: routines.Failed, Tier: "T0", At: at(d, 9, 0), Reason: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	v.send()
	if rec := v.latest(v.r); rec.Outcome == routines.Launched {
		t.Errorf("record = %+v, want no launch after three failures in a row", rec)
	}
	if got := v.stored(v.r).Before; got != routines.AskFirst {
		t.Errorf("stored Before acting = %q, want ask_first", got)
	}
	if n := len(v.calls()); n != 0 {
		t.Errorf("%d reviewer calls, want none for a routine the breaker holds at ask first", n)
	}
}

// flipLive holds the event's dedupe key on its first read and lets go of it on the next.
type flipLive struct {
	mu    sync.Mutex
	reads int
}

func (f *flipLive) WorkerShips(string) int { return 0 }
func (f *flipLive) DedupeKeys(string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.reads == 1 {
		return map[string]string{"gh:acme/api#7": "ship-1"}
	}
	return nil
}

func TestARoutineThatRoseToA3BetweenTheGateAndTheDecisionIsProposedUnreviewed(t *testing.T) {
	v := newReviewed(t)
	v.sched.Live = &flipLive{}
	v.sched.Enrich = func(routines.Event) routines.Enrichment { return routines.Enrichment{DedupeKey: "gh:acme/api#7"} }
	_, rec := v.send()
	if n := len(v.calls()); n != 0 {
		t.Errorf("%d reviewer calls, want none: the gate saw the routine held", n)
	}
	if rec.Outcome != routines.Proposed || rec.Verdict != "ask" || rec.ReviewerReason != "not reviewed" {
		t.Errorf("record = %+v, want proposed with verdict ask and the reason \"not reviewed\"", rec)
	}
}
