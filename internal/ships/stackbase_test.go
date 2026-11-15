package ships

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

// fakeStackChecker answers every check with one scripted answer and keeps the requests.
type fakeStackChecker struct {
	mu        sync.Mutex
	conflicts bool
	err       error
	got       []moor.StackCheck
}

func (f *fakeStackChecker) Conflicts(_ context.Context, c moor.StackCheck) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, c)
	return f.conflicts, f.err
}

func (f *fakeStackChecker) set(conflicts bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conflicts, f.err = conflicts, err
}

func (f *fakeStackChecker) requests() []moor.StackCheck {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]moor.StackCheck(nil), f.got...)
}

func layerURL(n int) string { return "https://github.com/acme/widgets/pull/" + strconv.Itoa(n) }

// readyAt is a ready layer whose head is head.
func readyAt(head string) moor.Status {
	st := readyStatus()
	st.Head = head
	return st
}

// stackedDetails is GitHub's answer for layers 7, 9 and 11 as one GitHub stack on main.
func stackedDetails(heads ...string) map[string]moor.Detail {
	st := &moor.DetailStack{Number: 4, Size: len(heads), Base: "main"}
	for i := range heads {
		n := []int{7, 9, 11}[i]
		st.Entries = append(st.Entries, moor.DetailStackEntry{Position: i + 1, Number: n, URL: layerURL(n), State: "OPEN", Head: heads[i]})
	}
	out := map[string]moor.Detail{}
	for i := range heads {
		n := []int{7, 9, 11}[i]
		base := "main"
		if i > 0 {
			base = heads[i-1]
		}
		out[layerURL(n)] = moor.Detail{Number: n, URL: layerURL(n), Head: heads[i], Base: base, DefaultBranch: "main", Stack: st}
	}
	return out
}

// chainedDetails is the same layers chained by branch names, in no GitHub stack.
func chainedDetails(heads ...string) map[string]moor.Detail {
	out := stackedDetails(heads...)
	for u, d := range out {
		d.Stack = nil
		out[u] = d
	}
	return out
}

// independentDetails has every layer based on main.
func independentDetails(heads ...string) map[string]moor.Detail {
	out := chainedDetails(heads...)
	for u, d := range out {
		d.Base = "main"
		out[u] = d
	}
	return out
}

type stackBaseRig struct {
	f  *mooredFixture
	g  *pollGitHub
	ck *fakeStackChecker
}

func newStackBaseRig(t *testing.T, details map[string]moor.Detail, layers ...moor.Status) *stackBaseRig {
	t.Helper()
	g := &pollGitHub{detail: details}
	urls := []string{layerURL(7), layerURL(9), layerURL(11)}[:len(layers)]
	for i, st := range layers {
		g.set(urls[i], st)
	}
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	ck := &fakeStackChecker{conflicts: true}
	f.s.StackChecker = ck
	f.moor(t, "Nelson", "work", urls...)
	return &stackBaseRig{f: f, g: g, ck: ck}
}

func (r *stackBaseRig) poll(t *testing.T) Ship {
	t.Helper()
	r.f.s.PollMoored(context.Background())
	sh, ok := r.f.ship(t, "Nelson")
	if !ok {
		t.Fatal("the ship is not listed")
	}
	return sh
}

func TestAStackThatConflictsWithItsBaseAsAWholeIsNotReadyWhileEachLayerStaysReady(t *testing.T) {
	for name, details := range map[string]map[string]moor.Detail{
		"a GitHub stack":      stackedDetails("h7", "h9", "top-1"),
		"a chain of branches": chainedDetails("h7", "h9", "top-1"),
	} {
		t.Run(name, func(t *testing.T) {
			r := newStackBaseRig(t, details, readyAt("h7"), readyAt("h9"), readyAt("top-1"))

			sh := r.poll(t)

			if sh.StackReady {
				t.Errorf("stack_ready = true, want false: the stack conflicts with main")
			}
			for _, p := range sh.PRs {
				if !p.Ready || p.Conflicts {
					t.Errorf("PR %s = ready %v, conflicts %v, want GitHub's own per-PR answer left as it is", p.URL, p.Ready, p.Conflicts)
				}
			}
			want := moor.StackCheck{Fleet: "work", Host: "github.com", Repo: "acme/widgets", Base: "main", PR: 11, Head: "top-1"}
			if got := r.ck.requests(); len(got) == 0 || got[0] != want {
				t.Errorf("checks = %+v, want the top layer's head asked against main: %+v", got, want)
			}
		})
	}
}

func TestAStackThatMergesCleanlyIntoItsBaseStaysReady(t *testing.T) {
	r := newStackBaseRig(t, stackedDetails("h7", "h9", "top-1"), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
	r.ck.set(false, nil)

	sh := r.poll(t)

	if !sh.StackReady || sh.ReadyUpTo != layerURL(11) {
		t.Errorf("ship = ready %v up to %q, want ready up to the top layer", sh.StackReady, sh.ReadyUpTo)
	}
	if len(r.ck.requests()) == 0 {
		t.Errorf("the base check never ran, so the premise fails")
	}
}

func TestAStackBaseCheckThatCannotRunChangesNothing(t *testing.T) {
	t.Run("the checker fails", func(t *testing.T) {
		r := newStackBaseRig(t, stackedDetails("h7", "h9", "top-1"), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
		r.ck.set(false, errors.New("no gh auth"))

		if sh := r.poll(t); !sh.StackReady {
			t.Errorf("stack_ready = false, want it as today: the check could not run")
		}
		if len(r.ck.requests()) == 0 {
			t.Errorf("the base check never ran, so the premise fails")
		}
	})
	t.Run("the shape cannot be read", func(t *testing.T) {
		r := newStackBaseRig(t, stackedDetails("h7", "h9", "top-1"), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
		r.g.detailErr = errors.New("GitHub is down")

		if sh := r.poll(t); !sh.StackReady {
			t.Errorf("stack_ready = false, want it as today: the shape could not be read")
		}
		if got := r.ck.requests(); len(got) != 0 {
			t.Errorf("checks = %+v, want none without a shape", got)
		}
	})
}

func TestTheStackBaseCheckRunsOnlyForAStackedOrChainedShipWhoseOpenLayersAreAllReady(t *testing.T) {
	pending := readyAt("top-1")
	pending.Checks, pending.MergeState = "PENDING", "BLOCKED"
	for name, c := range map[string]struct {
		details map[string]moor.Detail
		layers  []moor.Status
	}{
		"independent PRs":       {independentDetails("h7", "h9", "top-1"), []moor.Status{readyAt("h7"), readyAt("h9"), readyAt("top-1")}},
		"one PR":                {map[string]moor.Detail{layerURL(7): {Number: 7, URL: layerURL(7), Head: "top-1", Base: "main", DefaultBranch: "main"}}, []moor.Status{readyAt("top-1")}},
		"a layer not ready yet": {stackedDetails("h7", "h9", "top-1"), []moor.Status{readyAt("h7"), readyAt("h9"), pending}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newStackBaseRig(t, c.details, c.layers...)

			sh := r.poll(t)

			if got := r.ck.requests(); len(got) != 0 {
				t.Errorf("checks = %+v, want none", got)
			}
			if want := name != "a layer not ready yet"; sh.StackReady != want {
				t.Errorf("stack_ready = %v, want %v as today", sh.StackReady, want)
			}
		})
	}
}

func TestTheStackShapeIsReadOnceUntilALayersHeadMoves(t *testing.T) {
	r := newStackBaseRig(t, stackedDetails("h7", "h9", "top-1"), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
	r.poll(t)
	first := len(r.g.detailLog())
	if first == 0 {
		t.Fatal("the premise fails: the shape was never read")
	}

	r.poll(t)

	if got := len(r.g.detailLog()); got != first {
		t.Errorf("detail reads = %d after a second poll with the same heads, want still %d", got, first)
	}
	r.g.set(layerURL(11), readyAt("top-2"))
	r.g.mu.Lock()
	r.g.detail = stackedDetails("h7", "h9", "top-2")
	r.g.mu.Unlock()
	r.poll(t)
	if got := len(r.g.detailLog()); got <= first {
		t.Errorf("detail reads = %d after the top head moved, want the shape read again", got)
	}
	got := r.ck.requests()
	if last := got[len(got)-1]; last.Head != "top-2" {
		t.Errorf("last check = %+v, want the new top head", last)
	}
}

func TestMergeComesBackOnTheNextPollOnceTheConflictIsGone(t *testing.T) {
	r := newStackBaseRig(t, stackedDetails("h7", "h9", "top-1"), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
	if sh := r.poll(t); sh.StackReady {
		t.Fatal("the premise fails: the conflicting stack is ready")
	}

	r.ck.set(false, nil) // main moved, or the top was rebased

	if sh := r.poll(t); !sh.StackReady {
		t.Errorf("stack_ready = false on the poll after the conflict went, want true")
	}
}
