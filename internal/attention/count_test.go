package attention_test

import (
	"testing"

	"github.com/jbuffin/admiral/internal/attention"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/ships"
)

func stop(session, reason string) ships.Ship {
	return ships.Ship{Session: session, Status: "needs_you", Reason: reason}
}

func TestCount(t *testing.T) {
	seen := stop("a", "idle")
	seen.Seen = true
	crewHolder := stop("c", "crew")
	crewHolder.Crew = []ships.CrewBoat{{Name: "x", Status: "needs_you"}, {Name: "y", Status: "underway"}, {Name: "z", Status: "needs_you"}}
	sailing := ships.Ship{Session: "s", Status: "sailing_off", Crew: []ships.CrewBoat{{Name: "x", Status: "needs_you"}}}
	underwayWithBoat := ships.Ship{Session: "u", Status: "underway", Crew: []ships.CrewBoat{{Name: "x", Status: "needs_you"}}}
	ready := ships.Ship{Session: "r", Status: "moored", StackReady: true, PRs: []moor.PR{{URL: "p1", State: "OPEN"}}}
	reviewedReady := ships.Ship{Session: "r", Status: "moored", StackReady: true, PRs: []moor.PR{{URL: "p1", State: "OPEN"}, {URL: "p2", State: "MERGED", Reviewing: true}}}
	liveReady := ships.Ship{Session: "r", Status: "underway", StackReady: true}
	reReview := ships.Ship{Session: "v", Status: "moored", PRs: []moor.PR{{URL: "p1", State: "OPEN", Reviewing: true, Responded: true}}}
	respondedClosed := ships.Ship{Session: "v", Status: "moored", PRs: []moor.PR{{URL: "p1", State: "MERGED", Reviewing: true, Responded: true}}}
	respondedLive := ships.Ship{Session: "v", Status: "underway", PRs: []moor.PR{{URL: "p1", State: "OPEN", Reviewing: true, Responded: true}}}
	otherFleet := stop("f2", "idle")
	otherFleet.Fleet = "other"

	question := inbox.Item{Session: "q", ID: "1", Kind: inbox.KindQuestion}
	review := inbox.Item{Session: "q", ID: "2", Kind: inbox.KindReview}
	notice := inbox.Item{Session: inbox.AdmiralSession, ID: "n", Kind: inbox.KindNotice}

	for _, c := range []struct {
		name  string
		ships []ships.Ship
		items []inbox.Item
		want  int
	}{
		{"nothing waits", nil, nil, 0},
		{"a needs-you ship, a question and a notice", []ships.Ship{stop("a", "idle")}, []inbox.Item{question, notice}, 2},
		{"every non-notice kind counts", nil, []inbox.Item{question, review, notice}, 2},
		{"a notice never counts", nil, []inbox.Item{notice}, 0},
		{"a seen stop still counts", []ships.Ship{seen}, nil, 1},
		{"a ship needing you for its crew counts only its waiting boats", []ships.Ship{crewHolder}, nil, 2},
		{"a waiting boat on an underway ship counts", []ships.Ship{underwayWithBoat}, nil, 1},
		{"a boat on a sailing-off ship does not", []ships.Ship{sailing}, nil, 0},
		{"a moored ship whose stack is ready counts", []ships.Ship{ready}, nil, 1},
		{"a ready stack that isn't moored does not", []ships.Ship{liveReady}, nil, 0},
		{"any reviewing PR, open or not, keeps a ship from ready", []ships.Ship{reviewedReady}, nil, 0},
		{"a moored ship with a responded open reviewed PR is a re-review", []ships.Ship{reReview}, nil, 1},
		{"a responded PR that isn't open is no re-review", []ships.Ship{respondedClosed}, nil, 0},
		{"a re-review needs a moored ship", []ships.Ship{respondedLive}, nil, 0},
		{"ships of every fleet add up", []ships.Ship{stop("a", "idle"), otherFleet, ready}, []inbox.Item{question}, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := attention.Count(c.ships, c.items); got != c.want {
				t.Errorf("Count = %d, want %d", got, c.want)
			}
		})
	}
}
