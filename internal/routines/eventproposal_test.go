package routines_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

func TestAVerifiedRefsRepoWinsOverTheLookoutsRepo(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	h.onRoutine(routines.AskFirst, nil)
	var got routines.Enrichment
	h.sched.Acts = actsFunc(func(en routines.Enrichment) { got = en })
	h.sched.Enrich = func(routines.Event) routines.Enrichment { return routines.Enrichment{Repo: "acme/web"} }
	h.sched.HandleEvent(evFrom("Eng"))
	if got.Repo != "acme/api" {
		t.Errorf("repo handed to the acts = %q, want acme/api from the verified ref, not the Lookout's acme/web", got.Repo)
	}
}

func TestAFailedLookoutCallIsRecordedAsALookoutReason(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.AskFirst, nil)
	h.sched.Enrich = func(routines.Event) routines.Enrichment {
		return routines.Enrichment{Failure: "timeout", Costs: []routines.Cost{{Call: "lookout", USD: 0.02}}}
	}
	h.sched.HandleEvent(evFrom("Eng"))
	rec := h.latest(r)
	if !strings.Contains(rec.Reason, "lookout_timeout") {
		t.Errorf("record reason = %q, want it to hold lookout_timeout", rec.Reason)
	}
	if len(rec.Costs) != 1 || rec.Costs[0].USD != 0.02 {
		t.Errorf("record costs = %+v, want the failed call's reported cost", rec.Costs)
	}
}

// proposed has an Ask first event routine make one open proposal and returns the routine and its record.
func proposed(t *testing.T) (*harness, routines.Routine, routines.Trigger) {
	t.Helper()
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.AskFirst, nil)
	h.sched.HandleEvent(evFrom("Eng"))
	rec := h.latest(r)
	if rec.Outcome != routines.Proposed || rec.ID == "" {
		t.Fatalf("record = %+v, want a proposed record with an ID", rec)
	}
	return h, r, rec
}

func TestLaunchingAnEventProposalStartsOneAutopilotShipWithTheTemplateAndTheLinkOnly(t *testing.T) {
	h, r, rec := proposed(t)
	got, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID)
	if err != nil {
		t.Fatalf("LaunchProposed = %v", err)
	}
	h.wantLaunches(1)
	req := h.ships.requests()[0]
	if !req.Autopilot || req.RoutineKey != "routine:"+r.ID || req.Fleet != "work" {
		t.Errorf("request = %+v, want autopilot, key routine:%s, fleet work", req, r.ID)
	}
	if !strings.Contains(req.Text, r.Do.Launch.Text) || !strings.Contains(req.Text, "https://github.com/acme/api/pull/7") || len(req.Text) > len(r.Do.Launch.Text)+len("https://github.com/acme/api/pull/7")+40 {
		t.Errorf("prompt = %q, want the routine's template and the event link and nothing more", req.Text)
	}
	if got.Outcome != routines.Launched || got.Via != routines.ViaProposal || got.Ship == "" || req.TriggerID != got.ID {
		t.Errorf("launched record = %+v with trigger id %q, want launched via the proposal with the ship and its own id", got, req.TriggerID)
	}
	if _, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID); err == nil {
		t.Error("a second launch of the same proposal succeeded")
	}
	h.wantLaunches(1)
}

func TestAnEventLaunchAcceptsOnlyAnOpenProposalThisFleetIssuedForThisRoutine(t *testing.T) {
	h, r, rec := proposed(t)
	other := h.onRoutine(routines.AskFirst, func(in *routines.Routine) { in.Name = "Another watch" })
	cases := []struct {
		name                    string
		fleet, routine, trigger string
	}{
		{"an unknown trigger", "work", r.ID, "trigger-nobody-issued"},
		{"an empty trigger", "work", r.ID, ""},
		{"another routine's id", "work", other.ID, rec.ID},
		{"another fleet", "play", r.ID, rec.ID},
	}
	for _, c := range cases {
		if _, err := h.sched.LaunchProposed(context.Background(), c.fleet, c.routine, c.trigger); !errors.Is(err, routines.ErrNotIssued) && !errors.Is(err, routines.ErrNotFound) {
			t.Errorf("%s: LaunchProposed = %v, want ErrNotIssued or ErrNotFound", c.name, err)
		}
	}
	h.wantLaunches(0)
}

func TestAnEventLaunchRefusesARecordThatWasNotAProposal(t *testing.T) {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	r := h.onRoutine(routines.Act, func(in *routines.Routine) { in.Before = routines.ReportOnly })
	h.sched.HandleEvent(evFrom("Eng"))
	rec := h.latest(r)
	if rec.Outcome != routines.Reported {
		t.Fatalf("record = %+v, want a reported record", rec)
	}
	if _, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID); !errors.Is(err, routines.ErrNotIssued) {
		t.Errorf("LaunchProposed on a reported record = %v, want ErrNotIssued", err)
	}
	h.wantLaunches(0)
}

func TestAnEventProposalCannotBeLaunchedAfterItExpires(t *testing.T) {
	h, r, rec := proposed(t)
	h.clock.set(rec.At.Add(routines.EventProposalExpiry + time.Second))
	if _, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID); !errors.Is(err, routines.ErrExpired) {
		t.Errorf("LaunchProposed after expiry = %v, want ErrExpired", err)
	}
	h.wantLaunches(0)
}

func TestAnEventLaunchWhileTheRoutinesShipIsListedIsBusyAndTheProposalStaysOpen(t *testing.T) {
	h, r, rec := proposed(t)
	h.ships.set(func() {
		h.ships.holders = map[string]routines.ShipRef{"routine:" + r.ID: {Session: "ship-9", Status: "anchored"}}
	})
	if _, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID); !errors.Is(err, routines.ErrBusy) {
		t.Fatalf("LaunchProposed with a ship listed = %v, want ErrBusy", err)
	}
	h.wantLaunches(0)
	h.ships.set(func() { h.ships.holders = nil })
	if _, err := h.sched.LaunchProposed(context.Background(), "work", r.ID, rec.ID); err != nil {
		t.Errorf("LaunchProposed once the ship is gone = %v, want the proposal still open", err)
	}
	h.wantLaunches(1)
}

func TestLiveCountsTheListedEventShipsOfAFleetAndHoldsTheirDedupeKeys(t *testing.T) {
	live := routines.EventLive{Ships: func(fleet string) []routines.LiveShip {
		if fleet != "work" {
			return nil
		}
		return []routines.LiveShip{{Session: "s1", DedupeKey: "gh:acme/api#7"}, {Session: "s2", DedupeKey: ""}, {Session: "s3", DedupeKey: "gh:acme/api#9"}}
	}}
	if got := live.WorkerShips("work"); got != 3 {
		t.Errorf("WorkerShips(work) = %d, want 3", got)
	}
	keys := live.DedupeKeys("work")
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"gh:acme/api#7", "gh:acme/api#9"}) {
		t.Errorf("DedupeKeys(work) = %v, want the two non-empty keys", keys)
	}
	if live.WorkerShips("play") != 0 || len(live.DedupeKeys("play")) != 0 {
		t.Errorf("play shows %d ships and keys %v, want none", live.WorkerShips("play"), live.DedupeKeys("play"))
	}
}

func TestEventShipsKeepTheNewestRecordOfAReusedName(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 500, time.UTC)
	recs := []routines.Trigger{ // newest first
		{Routine: "rt", Ship: "alpha", Event: "https://github.com/acme/api/issues/9", DedupeKey: "gh:acme/api#9", At: at.Add(time.Hour)},
		{Routine: "rt", Ship: "alpha", Event: "https://github.com/acme/api/issues/7", DedupeKey: "gh:acme/api#7", At: at},
	}
	listed := []routines.ListedShip{{Session: "alpha", Fleet: "work", Routine: "routine:rt", Launched: at.Add(time.Hour).UTC().Format(time.RFC3339)}}
	got := routines.EventShips("work", recs, listed)
	if len(got) != 1 || got[0].DedupeKey != "gh:acme/api#9" {
		t.Errorf("EventShips = %v, want alpha holding the newest key gh:acme/api#9", got)
	}
}

func TestEventShipsSkipAHandLaunchedShipThatReusesAName(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	recs := []routines.Trigger{{Routine: "rt", Ship: "alpha", Event: "https://github.com/acme/api/issues/7", DedupeKey: "gh:acme/api#7", At: at}}
	for name, sh := range map[string]routines.ListedShip{
		"no routine key":     {Session: "alpha", Fleet: "work", Launched: at.Add(time.Hour).Format(time.RFC3339)},
		"another routine":    {Session: "alpha", Fleet: "work", Routine: "routine:other", Launched: at.Add(time.Hour).Format(time.RFC3339)},
		"launched before it": {Session: "alpha", Fleet: "work", Routine: "routine:rt", Launched: at.Add(-time.Hour).Format(time.RFC3339)},
		"another fleet":      {Session: "alpha", Fleet: "play", Routine: "routine:rt", Launched: at.Format(time.RFC3339)},
	} {
		if got := routines.EventShips("work", recs, []routines.ListedShip{sh}); len(got) != 0 {
			t.Errorf("%s: EventShips = %v, want none", name, got)
		}
	}
	if got := routines.EventShips("work", recs, []routines.ListedShip{{Session: "alpha", Fleet: "work", Routine: "routine:rt"}}); len(got) != 1 {
		t.Errorf("a ship with no launch time = %v, want counted by its routine key", got)
	}
}

func TestEventShipsSkipAShipLaunchedFromAnAcceptedProposal(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	recs := []routines.Trigger{
		{Routine: "rt", Ship: "alpha", Event: "https://github.com/acme/api/issues/9", Via: routines.ViaProposal, At: at.Add(time.Hour)},
		{Routine: "rt", Ship: "alpha", Event: "https://github.com/acme/api/issues/7", At: at},
	}
	listed := []routines.ListedShip{{Session: "alpha", Fleet: "work", Routine: "routine:rt", Launched: at.Add(time.Hour).UTC().Format(time.RFC3339), Status: "underway"}}
	if got := routines.EventShips("work", recs, listed); len(got) != 0 {
		t.Errorf("EventShips = %v, want none: the engineer launched alpha from a proposal, so no cap, wall clock or Stop all applies", got)
	}
}
