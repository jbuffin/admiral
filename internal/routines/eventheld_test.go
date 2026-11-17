// An event on a PR or issue some ship already holds is skipped and logged naming that ship, whoever launched it.
package routines_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/standing"
)

const (
	heldPR    = "https://github.com/acme/api/pull/7"
	heldIssue = "https://github.com/acme/api/issues/42"
)

// shipList is the listing a harness's fake ships share with the scheduler's Holder, as the server wires ships.RefHolder.
type shipList struct {
	mu   sync.Mutex
	list []ships.Ship
}

func (l *shipList) add(sh ships.Ship) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if sh.Fleet == "" {
		sh.Fleet = "work"
	}
	if sh.Status == "" {
		sh.Status = ships.StatusUnderway
	}
	l.list = append(l.list, sh)
}

func (l *shipList) holder(fleet, ref string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var mine []ships.Ship
	for _, sh := range l.list {
		if sh.Fleet == fleet {
			mine = append(mine, sh)
		}
	}
	sh, ok := ships.RefHolder(mine, ref)
	return sh.Session, ok
}

// withListing wires the scheduler's Holder to a shipList, and makes each fake launch list a ship whose URL is the event's link.
func (h *harness) withListing() *shipList {
	l := &shipList{}
	h.sched.Holder = l.holder
	h.ships.set(func() {
		h.ships.launch = func(n int, req launcher.Request) (routines.ShipRef, *launcher.Error) {
			session := "ship-" + strconv.Itoa(n)
			l.add(ships.Ship{Session: session, Name: session, URL: req.EventURL, Routine: req.RoutineKey})
			return routines.ShipRef{Session: session, Status: "underway"}, nil
		}
	})
	return l
}

func (h *harness) wantOnlySkippedHeldBy(r routines.Routine, holder string) {
	h.t.Helper()
	recs := h.records(r)
	if len(recs) != 1 || recs[0].Outcome != routines.Skipped || recs[0].Reason != "ref_has_ship" || recs[0].HeldBy != holder || recs[0].Ship != "" {
		h.t.Errorf("routine %q records = %+v, want one skipped ref_has_ship record held_by %q and no ship", r.Name, recs, holder)
	}
}

func TestAnEventOnAPRAHandLaunchedShipHoldsIsSkippedNamingItOnEveryMatchingRoutineWhateverItAsks(t *testing.T) {
	h, act, acts := eventRun(t, standing.Ceiling{Level: standing.A4}, nil)
	ask := h.onRoutine(routines.AskFirst, func(r *routines.Routine) { r.Name = "ask" })
	report := h.onRoutine(routines.ReportOnly, func(r *routines.Routine) { r.Name = "report" })
	commodore := h.onRoutine(routines.Act, func(r *routines.Routine) { r.Name = "commodore"; commodoreSpec(r) })
	h.withListing().add(ships.Ship{Session: "dory", Name: "dory", URL: heldPR})
	h.sched.HandleEvent(evFrom("Eng"))
	h.wantLaunches(0)
	for _, r := range []routines.Routine{act, ask, report, commodore} {
		h.wantOnlySkippedHeldBy(r, "dory")
	}
	if len(acts.got) != 0 {
		t.Errorf("%d decisions at the acts, want none: a held event leaves no proposal and no Lookout work", len(acts.got))
	}
}

func TestAHeldEventDoesNotCallTheLookout(t *testing.T) {
	h, _, _ := eventRun(t, standing.Ceiling{Level: standing.A4}, nil)
	h.withListing().add(ships.Ship{Session: "dory", Name: "dory", URL: heldPR})
	called := 0
	h.sched.Enrich = func(routines.Event) routines.Enrichment { called++; return routines.Enrichment{} }
	h.sched.HandleEvent(evFrom("Eng"))
	h.wantLaunches(0)
	if called != 0 {
		t.Errorf("the Lookout was called %d times for an event on a held PR, want 0", called)
	}
}

func TestAnEventOnAnIssueAnOrderShipClaimsOrOnAPRAMooredShipCarriesIsSkippedNamingTheShip(t *testing.T) {
	cases := map[string]struct {
		ship ships.Ship
		ref  string
	}{
		"an order ship's claimed issue": {ships.Ship{Session: "order-1", Source: "linear", Issues: []string{heldIssue}}, heldIssue},
		"a moored ship's PR":            {ships.Ship{Session: "moored-1", Status: "moored", PRs: []moor.PR{{URL: heldPR}}}, heldPR},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, r, _ := eventRun(t, standing.Ceiling{Level: standing.A4}, nil)
			h.withListing().add(c.ship)
			ev := evFrom("Eng")
			ev.Ref = c.ref
			h.sched.HandleEvent(ev)
			h.wantLaunches(0)
			h.wantOnlySkippedHeldBy(r, c.ship.Session)
		})
	}
}

func TestAnEventOnAnotherPRThanTheHeldOneStillLaunches(t *testing.T) {
	h, r, _ := eventRun(t, standing.Ceiling{Level: standing.A4}, nil)
	h.withListing().add(ships.Ship{Session: "dory", Name: "dory", URL: heldPR})
	other := evFrom("Eng")
	other.Ref = "https://github.com/acme/api/pull/8"
	h.sched.HandleEvent(other)
	h.wantLaunches(1)
	if got := h.ofOutcome(r, routines.Launched); len(got) != 1 {
		t.Errorf("launched records = %+v, want one for the PR no ship holds", got)
	}
}

func TestARepeatEventOnAPRTheRoutineLaunchedForIsSkippedByItsRefBeforeTheRoutineIsFoundBusy(t *testing.T) {
	h, r, acts := eventRun(t, standing.Ceiling{Level: standing.A4}, nil)
	h.withListing()
	h.sched.HandleEvent(evFrom("Eng"))
	h.wantLaunches(1)
	h.ships.set(func() {
		h.ships.holders = map[string]routines.ShipRef{"routine:" + r.ID: {Session: "ship-1", Status: "underway"}}
	})
	actsBefore := len(acts.got) // the first event's launch card
	h.sched.HandleEvent(evFrom("Eng"))
	h.wantLaunches(1)
	skipped := h.ofOutcome(r, routines.Skipped)
	if len(skipped) != 1 || skipped[0].Reason != "ref_has_ship" || skipped[0].HeldBy != "ship-1" || skipped[0].Ship != "" {
		t.Fatalf("skipped = %+v, want one ref_has_ship record held_by ship-1 for the repeat event", skipped)
	}
	if n := len(h.ofOutcome(r, routines.Proposed)); n != 0 || len(acts.got) != actsBefore {
		t.Errorf("%d proposals and %d new decisions for the repeat event, want none: it is not a routine_busy proposal", n, len(acts.got)-actsBefore)
	}
}

func TestAPluginEventWhoseLookoutKeyAnEventShipHoldsIsSkippedNamingItNotProposed(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.AskFirst, nil)
	key := "tracker:T-42"
	at := h.clock.Now()
	rec := routines.Trigger{ID: "first-launch", Routine: r.ID, Outcome: routines.Launched, Tier: "T1", Event: "https://tracker.example/t/42", DedupeKey: key, Ship: "s1", At: at}
	if _, err := h.store.AppendTrigger("work", rec); err != nil {
		t.Fatal(err)
	}
	listed := func() []routines.ListedShip {
		return []routines.ListedShip{{Session: "s1", Fleet: "work", Routine: "routine:" + r.ID, Launched: at.UTC().Format(time.RFC3339), Status: "underway"}}
	}
	h.sched.Listed = listed
	h.sched.Live = routines.EventLive{Listed: listed}
	h.sched.Enrich = func(routines.Event) routines.Enrichment { return routines.Enrichment{DedupeKey: key} }
	plugin := evFrom("Eng")
	plugin.Verified, plugin.Ref = false, "https://tracker.example/t/42"
	h.sched.HandleEvent(plugin)
	if n := len(h.ofOutcome(r, routines.Proposed)); n != 0 {
		t.Errorf("%d proposals for an event whose key a live ship holds, want none", n)
	}
	skipped := h.ofOutcome(r, routines.Skipped)
	if len(skipped) != 1 || skipped[0].Reason != "ref_has_ship" || skipped[0].HeldBy != "s1" || skipped[0].Ship != "" {
		t.Errorf("skipped = %+v, want one ref_has_ship record held_by s1", skipped)
	}
}

func TestASkipNamingAShipLeavesThatShipAsTheOnlyEventLaunchedShipWithItsOwnTrigger(t *testing.T) {
	h, first, second := twoRoutinesOnOneEvent(t, func(*standing.Limits) {})
	h.sched.HandleEvent(evFrom("Eng"))
	launched := h.ofOutcome(first, routines.Launched)
	if len(launched) != 1 {
		t.Fatalf("launched = %+v, want one", launched)
	}
	if skipped := h.ofOutcome(second, routines.Skipped); len(skipped) != 1 || skipped[0].HeldBy != "ship-1" {
		t.Fatalf("second routine skipped = %+v, want one record held_by ship-1", skipped)
	}
	recs, err := h.store.Records("work")
	if err != nil {
		t.Fatal(err)
	}
	listed := []routines.ListedShip{{Session: "ship-1", Fleet: "work", Routine: "routine:" + first.ID, Launched: h.clock.Now().UTC().Format(time.RFC3339), Status: "underway"}}
	for name, got := range map[string][]routines.LiveShip{
		"EventLaunchedShips": routines.EventLaunchedShips("work", recs, listed),
		"EventShips":         routines.EventShips("work", recs, listed),
	} {
		if len(got) != 1 || got[0].Session != "ship-1" || got[0].Trigger != launched[0].ID || got[0].Proposal {
			t.Errorf("%s = %+v, want ship-1 alone with the first routine's launch record and not a proposal ship", name, got)
		}
	}
}

func TestAnIssueHeldRefusalFromTheShipsIsASkippedRecordNamingTheHolderNotAFailure(t *testing.T) {
	for _, c := range []struct{ name, holder string }{
		{"a listed ship holds it", "dory"},
		{"only a launch in flight holds it", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, r, acts := eventRun(t, standing.Ceiling{Level: standing.A4}, nil)
			h.ships.set(func() {
				h.ships.launch = func(int, launcher.Request) (routines.ShipRef, *launcher.Error) {
					return routines.ShipRef{}, &launcher.Error{Status: http.StatusConflict, Msg: "this issue already has a ship", Cause: launcher.CauseIssueHeld, HeldBy: c.holder}
				}
			})
			h.sched.HandleEvent(evFrom("Eng"))
			recs := h.records(r)
			if len(recs) != 1 || recs[0].Outcome != routines.Skipped || recs[0].Reason != "ref_has_ship" || recs[0].HeldBy != c.holder || recs[0].Ship != "" {
				t.Errorf("records = %+v, want one skipped ref_has_ship record held_by %q, not a failed one", recs, c.holder)
			}
			if len(acts.got) != 0 {
				t.Errorf("%d decisions at the acts, want none for a skip", len(acts.got))
			}
			if n := len(h.ships.requests()); n != 1 {
				t.Errorf("%d launch attempts, want 1: nothing retries", n)
			}
		})
	}
}

func TestAcceptingAProposalOnAnItemAnotherShipNowHoldsIsAConflictAndASkippedRecordNotALaunch(t *testing.T) {
	cases := map[string]func(h *harness){
		"a ship now lists the PR": func(h *harness) { h.sched.Holder = func(string, string) (string, bool) { return "dory", true } },
		"the ships refuse the launch as issue held": func(h *harness) {
			h.ships.set(func() {
				h.ships.launch = func(int, launcher.Request) (routines.ShipRef, *launcher.Error) {
					return routines.ShipRef{}, &launcher.Error{Status: http.StatusConflict, Msg: "this issue already has a ship (dory)", Cause: launcher.CauseIssueHeld, HeldBy: "dory"}
				}
			})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h, r, rec := proposed(t)
			setup(h)
			_, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID)
			if !errors.Is(err, routines.ErrBusy) {
				t.Fatalf("LaunchProposed = %v, want ErrBusy, which the server answers with a 409", err)
			}
			if msg := err.Error(); !strings.Contains(msg, "already has a ship (dory)") {
				t.Errorf("409 message = %q, want it to name the ship that holds the item", msg)
			}
			skipped := h.ofOutcome(r, routines.Skipped)
			if len(skipped) != 1 || skipped[0].Reason != "ref_has_ship" || skipped[0].HeldBy != "dory" || skipped[0].Ship != "" {
				t.Errorf("skipped = %+v, want one ref_has_ship record held_by dory", skipped)
			}
			if n := len(h.ofOutcome(r, routines.Launched)) + len(h.ofOutcome(r, routines.Failed)); n != 0 {
				t.Errorf("%d launched or failed records, want none", n)
			}
		})
	}
}

func TestAHeldPRKeepsTheSkipFilterAndRepoAllowlistReasonsBeforeRefHasShip(t *testing.T) {
	cases := map[string]struct {
		limits func(*standing.Limits, string)
		reason string
	}{
		"a muted sender": {func(l *standing.Limits, id string) {
			l.Skip.Senders = []standing.Scoped{{Routine: id, Value: "bob"}}
		}, "skip_filter"},
		"a repo off the allowlist": {func(l *standing.Limits, _ string) { l.RepoAllowlist = []string{"acme/web"} }, "repo_not_allowed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, r, _ := eventRun(t, standing.Ceiling{Level: standing.A4}, nil)
			l := standing.PresetLimits(standing.Home)
			l.Ceilings.T1 = standing.Ceiling{Level: standing.A4}
			l.Rates = standing.Rates{LaunchesPerHour: 100, LaunchesPerSenderPerDay: 100, Concurrent: 100}
			c.limits(&l, r.ID)
			h.withEvents(l)
			h.withListing().add(ships.Ship{Session: "dory", Name: "dory", URL: heldPR})
			h.sched.HandleEvent(evFrom("bob"))
			recs := h.records(r)
			if len(recs) != 1 || recs[0].Outcome != routines.Skipped || recs[0].Reason != c.reason || recs[0].HeldBy != "" {
				t.Errorf("records = %+v, want one skipped %s record naming no holder", recs, c.reason)
			}
		})
	}
}

func TestAPluginEventsLookoutKeyInTheGitHubNamespaceIsNoKey(t *testing.T) {
	h, r, _ := eventRun(t, standing.Ceiling{Level: standing.A4}, nil)
	h.sched.Enrich = func(routines.Event) routines.Enrichment {
		return routines.Enrichment{Text: evText, DedupeKey: "gh:acme/api#7"}
	}
	ev := evFrom("Eng")
	ev.Verified, ev.Ref = false, "https://tracker.example/t/42"
	h.sched.HandleEvent(ev)
	recs := h.records(r)
	if len(recs) != 1 || recs[0].DedupeKey != "" {
		t.Errorf("records = %+v, want one record with no dedupe key: gh: is for keys Admiral reads from a verified ref", recs)
	}
}
