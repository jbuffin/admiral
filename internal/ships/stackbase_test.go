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

// stackedDetails is GitHub's answer for the first n of layers 7, 9 and 11 as one GitHub stack on main; their head branches are layer-N, never a SHA.
func stackedDetails(n int) map[string]moor.Detail {
	nums := []int{7, 9, 11}[:n]
	st := &moor.DetailStack{Number: 4, Size: n, Base: "main"}
	for i, num := range nums {
		st.Entries = append(st.Entries, moor.DetailStackEntry{Position: i + 1, Number: num, URL: layerURL(num), State: "OPEN", Head: "layer-" + strconv.Itoa(num)})
	}
	out := map[string]moor.Detail{}
	for i, num := range nums {
		base := "main"
		if i > 0 {
			base = "layer-" + strconv.Itoa(nums[i-1])
		}
		out[layerURL(num)] = moor.Detail{Number: num, URL: layerURL(num), Head: "layer-" + strconv.Itoa(num), Base: base, DefaultBranch: "main", Stack: st}
	}
	return out
}

// chainedDetails is the same layers chained by branch names, in no GitHub stack.
func chainedDetails(n int) map[string]moor.Detail {
	out := stackedDetails(n)
	for u, d := range out {
		d.Stack = nil
		out[u] = d
	}
	return out
}

// independentDetails has every layer based on main.
func independentDetails(n int) map[string]moor.Detail {
	out := chainedDetails(n)
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
		"a GitHub stack":      stackedDetails(3),
		"a chain of branches": chainedDetails(3),
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
	r := newStackBaseRig(t, stackedDetails(3), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
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
		r := newStackBaseRig(t, stackedDetails(3), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
		r.ck.set(false, errors.New("no gh auth"))

		if sh := r.poll(t); !sh.StackReady {
			t.Errorf("stack_ready = false, want it as today: the check could not run")
		}
		if len(r.ck.requests()) == 0 {
			t.Errorf("the base check never ran, so the premise fails")
		}
	})
	t.Run("the shape cannot be read", func(t *testing.T) {
		r := newStackBaseRig(t, stackedDetails(3), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
		r.g.detailErr = errors.New("GitHub is down")

		if sh := r.poll(t); !sh.StackReady {
			t.Errorf("stack_ready = false, want it as today: the shape could not be read")
		}
		if got := r.ck.requests(); len(got) != 0 {
			t.Errorf("checks = %+v, want none without a shape", got)
		}
	})
}

func TestTheStackBaseCheckRunsOnlyForAStackedOrChainedShipWhoseOpenLayersAreAllMergeable(t *testing.T) {
	blockedOnReview := readyAt("top-1")
	blockedOnReview.Review, blockedOnReview.MergeState = "REVIEW_REQUIRED", "BLOCKED"
	behind := readyAt("h9")
	behind.MergeState = "BEHIND"
	conflicting := readyAt("h9")
	conflicting.Mergeable, conflicting.MergeState = "CONFLICTING", "DIRTY"
	unknown := readyAt("h9")
	unknown.Mergeable, unknown.MergeState = "UNKNOWN", "UNKNOWN"
	one := map[string]moor.Detail{layerURL(7): {Number: 7, URL: layerURL(7), Head: "layer-7", Base: "main", DefaultBranch: "main"}}
	for name, c := range map[string]struct {
		details map[string]moor.Detail
		layers  []moor.Status
		runs    bool
		ready   bool
	}{
		"independent PRs": {independentDetails(3), []moor.Status{readyAt("h7"), readyAt("h9"), readyAt("top-1")}, false, true},
		"one PR":          {one, []moor.Status{readyAt("top-1")}, false, true},
		"the top mergeable but blocked on review": {stackedDetails(3), []moor.Status{readyAt("h7"), readyAt("h9"), blockedOnReview}, true, false},
		"a mergeable layer that is behind":        {stackedDetails(3), []moor.Status{readyAt("h7"), behind, readyAt("top-1")}, true, false},
		"a layer that conflicts on its own":       {stackedDetails(3), []moor.Status{readyAt("h7"), conflicting, readyAt("top-1")}, false, false},
		"a layer whose mergeability is unknown":   {stackedDetails(3), []moor.Status{readyAt("h7"), unknown, readyAt("top-1")}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newStackBaseRig(t, c.details, c.layers...)

			sh := r.poll(t)

			if got := r.ck.requests(); (len(got) > 0) != c.runs {
				t.Errorf("checks = %+v, want the check to run = %v", got, c.runs)
			}
			if sh.StackReady != c.ready {
				t.Errorf("stack_ready = %v, want %v", sh.StackReady, c.ready)
			}
		})
	}
}

func TestTheStackShapeIsReadOnceUntilALayersHeadMoves(t *testing.T) {
	r := newStackBaseRig(t, stackedDetails(3), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
	r.poll(t)
	first := len(r.g.detailLog())
	if first == 0 {
		t.Fatal("the premise fails: the shape was never read")
	}

	r.poll(t)

	if got := len(r.g.detailLog()); got != first {
		t.Errorf("detail reads = %d after a second poll with the same heads, want still %d", got, first)
	}
	r.g.set(layerURL(11), readyAt("top-2")) // a push moves the head SHA; the branch name stays
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
	r := newStackBaseRig(t, stackedDetails(3), readyAt("h7"), readyAt("h9"), readyAt("top-1"))
	if sh := r.poll(t); sh.StackReady {
		t.Fatal("the premise fails: the conflicting stack is ready")
	}

	r.ck.set(false, nil) // main moved, or the top was rebased

	if sh := r.poll(t); !sh.StackReady {
		t.Errorf("stack_ready = false on the poll after the conflict went, want true")
	}
}

func TestAStackWhoseLowerLayerMergedIsCheckedFromItsOpenLayers(t *testing.T) {
	merged := readyAt("h7")
	merged.State = "MERGED"
	details := stackedDetails(3)
	retargeted := details[layerURL(9)]
	retargeted.Base = "main" // GitHub retargets the next layer when the one below merges
	details[layerURL(9)] = retargeted
	r := newStackBaseRig(t, details, merged, readyAt("h9"), readyAt("top-1"))

	sh := r.poll(t)

	want := moor.StackCheck{Fleet: "work", Host: "github.com", Repo: "acme/widgets", Base: "main", PR: 11, Head: "top-1"}
	if got := r.ck.requests(); len(got) != 1 || got[0] != want {
		t.Errorf("checks = %+v, want one of %+v: the top open layer against the lowest open layer's base", got, want)
	}
	if sh.StackReady {
		t.Errorf("stack_ready = true, want false: the open layers conflict with main")
	}
}

func TestOneOpenLayerOnTopOfMergedOnesIsLeftToGitHub(t *testing.T) {
	merged := readyAt("h7")
	merged.State = "MERGED"
	r := newStackBaseRig(t, stackedDetails(3), merged, merged, readyAt("top-1"))

	sh := r.poll(t)

	if got := r.ck.requests(); len(got) != 0 {
		t.Errorf("checks = %+v, want none for a lone open PR", got)
	}
	if !sh.StackReady {
		t.Errorf("stack_ready = false, want true: GitHub's answer for the lone PR stands")
	}
}

func TestAReviewedShipsStackIsNotChecked(t *testing.T) {
	g := &pollGitHub{detail: stackedDetails(2)}
	g.set(layerURL(7), readyAt("h7"))
	g.set(layerURL(9), readyAt("top-1"))
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	ck := &fakeStackChecker{conflicts: true}
	f.s.StackChecker = ck
	f.moorReviewing(t, "Nelson", "work", []string{layerURL(9)}, layerURL(7), layerURL(9))

	f.s.PollMoored(context.Background())

	if got := ck.requests(); len(got) != 0 {
		t.Errorf("checks = %+v, want none: Admiral never offers Merge or Fix for a reviewed PR", got)
	}
}
