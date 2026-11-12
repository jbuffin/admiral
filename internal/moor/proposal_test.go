package moor

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

var proposalIDShape = regexp.MustCompile(`^wake-[0-9a-f]{20}$`)

func validTestLogin(l string) bool { return l != "" && !strings.ContainsAny(l, " !") }

func strangerEvent(pr, id, login string, at time.Time) Event {
	return Event{Kind: EventPRComment, PR: pr, ID: id, Author: wUser(login), At: at, Body: "plain prose from " + login}
}

func t3Decision(events ...Event) Decision {
	return Decision{
		T3: events,
		Next: WakeState{PRs: map[string]PRWake{
			wpr1: {Cursor: Cursor{At: wT0}},
			wpr2: {Cursor: Cursor{At: wT0}},
		}},
	}
}

// byAuthor reads batches by PR and login, so the order they come in does not matter.
func byAuthor(batches []ProposalBatch) map[string][]string {
	out := map[string][]string{}
	for _, b := range batches {
		var ids []string
		for _, e := range b.Events {
			ids = append(ids, e.ID)
		}
		sort.Strings(ids)
		out[b.PR+" "+b.Login] = ids
	}
	return out
}

func proposedIDs(s WakeState, pr string) []string {
	var ids []string
	for _, p := range s.PRs[pr].Proposed {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestProposeT3MakesOneBatchPerPRAndAuthor(t *testing.T) {
	d := t3Decision(
		strangerEvent(wpr1, "a1", "stranger-1", wEv),
		strangerEvent(wpr1, "a2", "stranger-1", wEv.Add(time.Minute)),
		strangerEvent(wpr1, "b1", "stranger-2", wEv),
		strangerEvent(wpr2, "c1", "stranger-1", wEv),
	)
	batches, _ := ProposeT3("Nelson", d, wNow, validTestLogin)
	want := map[string][]string{
		wpr1 + " stranger-1": {"a1", "a2"},
		wpr1 + " stranger-2": {"b1"},
		wpr2 + " stranger-1": {"c1"},
	}
	if got := byAuthor(batches); !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

func TestProposeT3SkipsEventsAlreadyProposed(t *testing.T) {
	d := t3Decision(
		strangerEvent(wpr1, "a1", "stranger-1", wEv),
		strangerEvent(wpr1, "a2", "stranger-1", wEv.Add(time.Minute)),
	)
	ps := d.Next.PRs[wpr1]
	ps.Proposed = []ProposedEvent{{ID: "a1", At: wEv}}
	d.Next.PRs[wpr1] = ps

	batches, next := ProposeT3("Nelson", d, wNow, validTestLogin)
	if got, want := byAuthor(batches), map[string][]string{wpr1 + " stranger-1": {"a2"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want only the new event %v", got, want)
	}
	if got, want := proposedIDs(next, wpr1), []string{"a1", "a2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded IDs = %v, want %v", got, want)
	}

	d.Next = next
	if again, _ := ProposeT3("Nelson", d, wNow, validTestLogin); len(again) != 0 {
		t.Fatalf("a repeated poll proposed again: %v", byAuthor(again))
	}
}

func TestProposeT3HoldsAFreshAuthorsBatchUntilItsNewestEventIsOldEnough(t *testing.T) {
	d := t3Decision(
		strangerEvent(wpr1, "old", "stranger-1", wNow.Add(-3*WakeDebounce)),
		strangerEvent(wpr1, "fresh", "stranger-2", wNow.Add(-time.Minute)),
		strangerEvent(wpr1, "mixed-old", "stranger-3", wNow.Add(-3*WakeDebounce)),
		strangerEvent(wpr1, "mixed-fresh", "stranger-3", wNow.Add(-time.Minute)),
	)
	batches, next := ProposeT3("Nelson", d, wNow, validTestLogin)
	want := map[string][]string{wpr1 + " stranger-1": {"old"}}
	if got := byAuthor(batches); !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want only the author whose newest event is old enough: %v", got, want)
	}
	if got, w := proposedIDs(next, wpr1), []string{"old"}; !reflect.DeepEqual(got, w) {
		t.Fatalf("recorded IDs = %v, want %v: held events are not recorded", got, w)
	}
}

func TestProposeT3ProposesFreshEventsOnThePRThatWakes(t *testing.T) {
	d := t3Decision(
		strangerEvent(wpr1, "on-waking-pr", "stranger-1", wNow.Add(-time.Minute)),
		strangerEvent(wpr2, "on-other-pr", "stranger-1", wNow.Add(-time.Minute)),
	)
	d.Wake, d.PR = true, wpr1
	batches, _ := ProposeT3("Nelson", d, wNow, validTestLogin)
	want := map[string][]string{wpr1 + " stranger-1": {"on-waking-pr"}}
	if got := byAuthor(batches); !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v: the round's cursor passes the events of the PR that wakes only", got, want)
	}
}

func TestProposeT3PrunesRecordedEventsTheCursorHasPassed(t *testing.T) {
	cursor := wT0.Add(30 * time.Minute)
	d := t3Decision(strangerEvent(wpr1, "new", "stranger-1", cursor.Add(10*time.Minute)))
	d.Next.PRs[wpr1] = PRWake{
		Cursor: Cursor{At: cursor},
		Proposed: []ProposedEvent{
			{ID: "before", At: cursor.Add(-time.Minute)},
			{ID: "after", At: cursor.Add(5 * time.Minute)},
		},
	}
	_, next := ProposeT3("Nelson", d, wNow, validTestLogin)
	if got, want := proposedIDs(next, wpr1), []string{"after", "new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded IDs = %v, want %v: an entry goes once the cursor is past its time", got, want)
	}
	if next.PRs[wpr1].Cursor.At != cursor {
		t.Fatalf("the cursor moved to %v: proposing never moves it", next.PRs[wpr1].Cursor.At)
	}
}

func TestProposeT3GroupsAGhostUnderGhost(t *testing.T) {
	d := t3Decision(
		strangerEvent(wpr1, "g1", "", wEv),
		strangerEvent(wpr1, "g2", "not a login!", wEv),
		strangerEvent(wpr1, "ok", "stranger-1", wEv),
	)
	batches, _ := ProposeT3("Nelson", d, wNow, validTestLogin)
	want := map[string][]string{wpr1 + " ghost": {"g1", "g2"}, wpr1 + " stranger-1": {"ok"}}
	if got := byAuthor(batches); !reflect.DeepEqual(got, want) {
		t.Fatalf("batches = %v, want %v", got, want)
	}
}

func TestProposeT3BatchIDIsTheProposalID(t *testing.T) {
	d := t3Decision(
		strangerEvent(wpr1, "a2", "stranger-1", wEv.Add(time.Minute)),
		strangerEvent(wpr1, "a1", "stranger-1", wEv),
	)
	batches, _ := ProposeT3("Nelson", d, wNow, validTestLogin)
	if len(batches) != 1 {
		t.Fatalf("batches = %v, want one", byAuthor(batches))
	}
	if got, want := batches[0].ID, ProposalID("Nelson", wpr1, "stranger-1", []string{"a1", "a2"}); got != want || !proposalIDShape.MatchString(got) {
		t.Fatalf("batch id = %q, want %q", got, want)
	}
}

func TestProposalIDIsAShortHashOfShipPRLoginAndTheEventIDs(t *testing.T) {
	id := ProposalID("Nelson", wpr1, "stranger-1", []string{"a1", "a2"})
	if !proposalIDShape.MatchString(id) {
		t.Fatalf("id = %q, want wake- and 20 hex characters", id)
	}
	if again := ProposalID("Nelson", wpr1, "stranger-1", []string{"a2", "a1"}); again != id {
		t.Fatalf("the order of the event IDs changed the id: %q and %q", id, again)
	}
	for name, other := range map[string]string{
		"ship":   ProposalID("Dory", wpr1, "stranger-1", []string{"a1", "a2"}),
		"pr":     ProposalID("Nelson", wpr2, "stranger-1", []string{"a1", "a2"}),
		"login":  ProposalID("Nelson", wpr1, "stranger-2", []string{"a1", "a2"}),
		"events": ProposalID("Nelson", wpr1, "stranger-1", []string{"a1", "a3"}),
		"fewer":  ProposalID("Nelson", wpr1, "stranger-1", []string{"a1"}),
		"joined": ProposalID("Nelson", wpr1, "stranger-1", []string{"a1a2"}),
	} {
		if other == id {
			t.Errorf("a different %s gave the same id %q", name, id)
		}
	}
}
