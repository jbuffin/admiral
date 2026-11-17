package sources

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// scriptedChecker answers by the top PR's number: conflicting, failing, or clean when unlisted.
type scriptedChecker struct {
	mu        sync.Mutex
	conflicts map[int]bool
	failing   map[int]bool
	asked     []moor.StackCheck
}

func (c *scriptedChecker) Conflicts(_ context.Context, r moor.StackCheck) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, r)
	if c.failing[r.PR] {
		return false, errors.New("the repository cannot be reached")
	}
	return c.conflicts[r.PR], nil
}

func (c *scriptedChecker) requestsFor(pr int) []moor.StackCheck {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []moor.StackCheck
	for _, r := range c.asked {
		if r.PR == pr {
			out = append(out, r)
		}
	}
	return out
}

// sha gives each pull request its own head commit, so a check that asks for another PR's head is caught.
func sha(n int) string {
	d := itoa(n)
	return d + strings.Repeat("a", 40-len(d))
}

func withHead(n int, extra obj) obj {
	out := obj{"headRefOid": sha(n)}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// chainOf is three PRs, each based on the one below, the first on main.
func chainOf(first int, mergeable func(n int) string) []obj {
	var out []obj
	for i := 0; i < 3; i++ {
		n := first + i
		base := "main"
		if i > 0 {
			base = "b" + itoa(n-1)
		}
		extra := withHead(n, nil)
		if m := mergeable(n); m != "" {
			extra["mergeable"] = m
		}
		out = append(out, chainNode(n, "b"+itoa(n), base, "fake-viewer", extra))
	}
	return out
}

func allMergeable(int) string { return "" }

func stackOf(first, number int) []obj {
	var out []obj
	for i := 0; i < 3; i++ {
		n := first + i
		base := "main"
		if i > 0 {
			base = "b" + itoa(n-1)
		}
		out = append(out, chainNode(n, "b"+itoa(n), base, "fake-viewer", withHead(n, obj{
			"stack": obj{"number": number, "size": 3}, "stackEntry": obj{"position": i + 1}})))
	}
	return out
}

// serveWithChecker serves the nodes with the checker in place and returns what the orders answer shows once ready(orders) holds.
func serveWithChecker(t *testing.T, ck moor.StackChecker, ready func(map[string]obj) bool, nodes ...obj) map[string]obj {
	t.Helper()
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	writeSearch(t, g, "bugs", "label:bug", nodes...)
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.Svc.Sup.StackChecker = ck
	f.startSources()
	var got map[string]obj
	deadline := time.Now().Add(5 * time.Second)
	for {
		list := f.Svc.View()
		for _, s := range list {
			if s.Key == "gha" && len(s.orders("bugs")) == len(nodes) {
				got = servedOrders(t, s, "bugs")
				if ready(got) {
					return got
				}
			}
		}
		if time.Now().After(deadline) {
			seen := map[string]any{}
			for id, o := range got {
				seen[id] = []any{conflictOf(o, "chain"), conflictOf(o, "stack")}
			}
			t.Fatalf("within 5s the awaited conflicts_with_base never showed; last answer's [chain, stack] values: %s", mustJSON(t, seen))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func conflictOf(o obj, kind string) any {
	g, _ := o[kind].(obj)
	return g["conflicts_with_base"]
}

func hasConflict(kind string, numbers ...int) func(map[string]obj) bool {
	return func(orders map[string]obj) bool {
		for _, n := range numbers {
			if conflictOf(orders["acme/widgets#"+itoa(n)], kind) != "main" {
				return false
			}
		}
		return true
	}
}

func TestAChainWhoseTopConflictsWithItsBaseIsServedWithTheBaseOnEveryPR(t *testing.T) {
	ck := &scriptedChecker{conflicts: map[int]bool{33: true}}
	orders := serveWithChecker(t, ck, hasConflict("chain", 31, 32, 33), chainOf(31, allMergeable)...)

	for _, n := range []int{31, 32, 33} {
		if got := conflictOf(orders["acme/widgets#"+itoa(n)], "chain"); got != "main" {
			t.Errorf("PR %d chain.conflicts_with_base = %v, want main", n, got)
		}
	}
	// The whole chain is asked once, as its top PR's head against the bottom's base.
	want := moor.StackCheck{Fleet: "alpha", Host: "github.com", Repo: "acme/widgets", Base: "main", PR: 33, Head: sha(33)}
	if got := ck.requestsFor(33); len(got) == 0 || !slices.ContainsFunc(got, func(r moor.StackCheck) bool { return r == want }) {
		t.Errorf("checks for the top = %+v, want one like %+v", got, want)
	}
}

func TestAGitHubStackWhoseTopConflictsWithItsBaseIsServedWithTheBaseOnEveryPR(t *testing.T) {
	ck := &scriptedChecker{conflicts: map[int]bool{53: true}}
	orders := serveWithChecker(t, ck, hasConflict("stack", 51, 52, 53), stackOf(51, 5)...)

	for _, n := range []int{51, 52, 53} {
		if got := conflictOf(orders["acme/widgets#"+itoa(n)], "stack"); got != "main" {
			t.Errorf("PR %d stack.conflicts_with_base = %v, want main", n, got)
		}
	}
}

// The next four put a conflicting chain beside the chain under test, so the awaited answer proves a refresh ran with the checker in place.
func TestAChainThatMergesCleanlyCarriesNoConflictAndIsAskedAboutItsTop(t *testing.T) {
	ck := &scriptedChecker{conflicts: map[int]bool{33: true}}
	nodes := append(chainOf(31, allMergeable), chainOf(41, allMergeable)...)
	orders := serveWithChecker(t, ck, hasConflict("chain", 31, 32, 33), nodes...)

	for _, n := range []int{41, 42, 43} {
		if got := conflictOf(orders["acme/widgets#"+itoa(n)], "chain"); got != nil {
			t.Errorf("PR %d chain.conflicts_with_base = %v, want none for a chain that merges cleanly", n, got)
		}
	}
	want := moor.StackCheck{Fleet: "alpha", Host: "github.com", Repo: "acme/widgets", Base: "main", PR: 43, Head: sha(43)}
	if got := ck.requestsFor(43); !slices.ContainsFunc(got, func(r moor.StackCheck) bool { return r == want }) {
		t.Errorf("checks for the clean chain's top = %+v, want one like %+v: a clean answer is only meaningful if the check ran", got, want)
	}
}

func TestAChainWithALayerThatIsNotMergeableIsNotChecked(t *testing.T) {
	ck := &scriptedChecker{conflicts: map[int]bool{33: true, 43: true}}
	nodes := append(chainOf(31, allMergeable), chainOf(41, func(n int) string {
		if n == 42 {
			return "CONFLICTING"
		}
		return ""
	})...)
	orders := serveWithChecker(t, ck, hasConflict("chain", 31, 32, 33), nodes...)

	if got := ck.requestsFor(43); len(got) != 0 {
		t.Errorf("checks for the chain with a conflicting layer = %+v, want none: that layer says so on its own", got)
	}
	for _, n := range []int{41, 42, 43} {
		if got := conflictOf(orders["acme/widgets#"+itoa(n)], "chain"); got != nil {
			t.Errorf("PR %d chain.conflicts_with_base = %v, want none", n, got)
		}
	}
}

func TestACheckThatFailsLeavesTheChainsOrdersAsTheyWere(t *testing.T) {
	ck := &scriptedChecker{conflicts: map[int]bool{33: true}, failing: map[int]bool{43: true}}
	nodes := append(chainOf(31, allMergeable), chainOf(41, allMergeable)...)
	orders := serveWithChecker(t, ck, hasConflict("chain", 31, 32, 33), nodes...)

	if len(ck.requestsFor(43)) == 0 {
		t.Fatal("the failing check was never asked, so the test proves nothing")
	}
	for n, want := range map[int]obj{
		41: {"id": widgets + "41", "position": 1, "size": 3, "head": "b41", "base": "main", "mine": true},
		43: {"id": widgets + "41", "position": 3, "size": 3, "head": "b43", "base": "b42", "mine": true},
	} {
		if got, want := mustJSON(t, orders["acme/widgets#"+itoa(n)]["chain"]), mustJSON(t, want); got != want {
			t.Errorf("PR %d chain = %s, want %s", n, got, want)
		}
	}
}

func TestAStackWhoseMiddleLayerIsNotInTheViewIsNotChecked(t *testing.T) {
	ck := &scriptedChecker{conflicts: map[int]bool{33: true, 63: true}}
	partial := stackOf(61, 6)
	nodes := append(chainOf(31, allMergeable), partial[0], partial[2])
	orders := serveWithChecker(t, ck, hasConflict("chain", 31, 32, 33), nodes...)

	if got := ck.requestsFor(63); len(got) != 0 {
		t.Errorf("checks for a stack with a layer missing from the view = %+v, want none", got)
	}
	for _, n := range []int{61, 63} {
		if got := conflictOf(orders["acme/widgets#"+itoa(n)], "stack"); got != nil {
			t.Errorf("PR %d stack.conflicts_with_base = %v, want none", n, got)
		}
	}
}
