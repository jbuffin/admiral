package moor

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// shapeGitHub answers PullRequestDetail from a table and counts the reads.
type shapeGitHub struct {
	GitHub
	mu     sync.Mutex
	detail map[string]Detail
	failOn string
	reads  map[string]int
}

func (g *shapeGitHub) PullRequestDetail(_ context.Context, url string) (Detail, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reads == nil {
		g.reads = map[string]int{}
	}
	g.reads[url]++
	if url == g.failOn {
		return Detail{}, errors.New("pull request lookup failed: boom")
	}
	d, ok := g.detail[url]
	if !ok {
		return Detail{}, errors.New("pull request lookup failed: GitHub has no such pull request")
	}
	return d, nil
}

func pr(n int) string { return fmt.Sprintf("https://github.com/acme/widgets/pull/%d", n) }

// pull is one PR's detail: its branches, and the stack it sits in (nil for none).
func pull(n int, head, base string, stack *DetailStack) Detail {
	return Detail{Number: n, URL: pr(n), State: "OPEN", Head: head, Base: base, DefaultBranch: "main", Stack: stack}
}

// stackOf is GitHub's stack number num holding the PRs, entries in the order given (not by position) at the positions given.
func stackOf(num int, entries ...DetailStackEntry) *DetailStack {
	return &DetailStack{Number: num, Size: len(entries), Base: "main", Entries: entries}
}

func entry(position, n int) DetailStackEntry {
	return DetailStackEntry{Position: position, Number: n, URL: pr(n), State: "OPEN", Head: fmt.Sprintf("b%d", n)}
}

func table(ds ...Detail) map[string]Detail {
	m := map[string]Detail{}
	for _, d := range ds {
		m[d.URL] = d
	}
	return m
}

func TestReadShapeNamesHowAShipsPRsRelateOnGitHub(t *testing.T) {
	shuffled := func() *DetailStack { return stackOf(4, entry(3, 23), entry(1, 21), entry(2, 22)) }
	for name, c := range map[string]struct {
		details []Detail
		prs     []string
		want    Shape
	}{
		"stacked, entries shuffled in GitHub's answer and the ship's PRs out of order": {
			[]Detail{pull(21, "b21", "main", shuffled()), pull(22, "b22", "b21", shuffled()), pull(23, "b23", "b22", shuffled())},
			[]string{pr(22), pr(23), pr(21)},
			Shape{Kind: ShapeStacked, StackNumber: 4, PRs: []string{pr(21), pr(22), pr(23)}},
		},
		"stacked, matched by number and not by the URL string": {
			[]Detail{
				pull(21, "b21", "main", stackOf(9, DetailStackEntry{Position: 1, Number: 21, URL: "https://GITHUB.com/acme/widgets/pull/21"}, DetailStackEntry{Position: 2, Number: 22})),
				pull(22, "b22", "b21", stackOf(9, DetailStackEntry{Position: 1, Number: 21}, DetailStackEntry{Position: 2, Number: 22, URL: "https://GITHUB.com/acme/widgets/pull/22"})),
			},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeStacked, StackNumber: 9, PRs: []string{pr(21), pr(22)}},
		},
		"chained, two": {
			[]Detail{pull(22, "b22", "b21", nil), pull(21, "b21", "main", nil)},
			[]string{pr(22), pr(21)},
			Shape{Kind: ShapeChained, PRs: []string{pr(21), pr(22)}},
		},
		"chained, three, given out of order": {
			[]Detail{pull(23, "b23", "b22", nil), pull(21, "b21", "main", nil), pull(22, "b22", "b21", nil)},
			[]string{pr(23), pr(21), pr(22)},
			Shape{Kind: ShapeChained, PRs: []string{pr(21), pr(22), pr(23)}},
		},
		"chained across two repositories": {
			[]Detail{pull(21, "b21", "main", nil), {Number: 22, URL: "https://github.com/acme/other/pull/22", State: "OPEN", Head: "b22", Base: "b21", DefaultBranch: "main"}},
			[]string{pr(21), "https://github.com/acme/other/pull/22"},
			Shape{Kind: ShapeMixed},
		},
		"chained from a default branch that is not main": {
			[]Detail{
				{Number: 21, URL: pr(21), State: "OPEN", Head: "b21", Base: "trunk", DefaultBranch: "trunk"},
				{Number: 22, URL: pr(22), State: "OPEN", Head: "b22", Base: "b21", DefaultBranch: "trunk"},
			},
			[]string{pr(22), pr(21)},
			Shape{Kind: ShapeChained, PRs: []string{pr(21), pr(22)}},
		},
		"independent": {
			[]Detail{pull(21, "b21", "main", nil), pull(22, "b22", "main", nil)},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeIndependent, PRs: []string{pr(21), pr(22)}},
		},
		"independent on a default branch that is not main": {
			[]Detail{
				{Number: 21, URL: pr(21), State: "OPEN", Head: "b21", Base: "trunk", DefaultBranch: "trunk"},
				{Number: 22, URL: pr(22), State: "OPEN", Head: "b22", Base: "trunk", DefaultBranch: "trunk"},
			},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeIndependent, PRs: []string{pr(21), pr(22)}},
		},
		"mixed: part chained, part independent": {
			[]Detail{pull(21, "b21", "main", nil), pull(22, "b22", "b21", nil), pull(23, "b23", "main", nil)},
			[]string{pr(21), pr(22), pr(23)},
			Shape{Kind: ShapeMixed},
		},
		"mixed: based on a branch that is neither the default nor a sibling's head": {
			[]Detail{pull(21, "b21", "develop", nil), pull(22, "b22", "main", nil)},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeMixed},
		},
		"mixed: a chain whose bottom is not on the default branch": {
			[]Detail{pull(21, "b21", "develop", nil), pull(22, "b22", "b21", nil)},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeMixed},
		},
		"mixed: two PRs on one base, a third on one of them": {
			[]Detail{pull(21, "b21", "main", nil), pull(22, "b22", "b21", nil), pull(23, "b23", "b21", nil)},
			[]string{pr(21), pr(22), pr(23)},
			Shape{Kind: ShapeMixed},
		},
		"mixed: a stack holding one more PR than the ship": {
			[]Detail{
				pull(21, "b21", "main", stackOf(4, entry(1, 21), entry(2, 22), entry(3, 23))),
				pull(22, "b22", "b21", stackOf(4, entry(1, 21), entry(2, 22), entry(3, 23))),
			},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeMixed},
		},
		"mixed: the ship holds one more PR than the stack": {
			[]Detail{
				pull(21, "b21", "main", stackOf(4, entry(1, 21), entry(2, 22))),
				pull(22, "b22", "b21", stackOf(4, entry(1, 21), entry(2, 22))),
				pull(23, "b23", "b22", nil),
			},
			[]string{pr(21), pr(22), pr(23)},
			Shape{Kind: ShapeMixed},
		},
		"mixed: one PR in a stack and the other in none": {
			[]Detail{pull(21, "b21", "main", stackOf(4, entry(1, 21), entry(2, 22))), pull(22, "b22", "b21", nil)},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeMixed},
		},
		"mixed: GitHub's stack is longer than the entries it answered": {
			[]Detail{
				pull(21, "b21", "main", &DetailStack{Number: 4, Size: 3, Entries: []DetailStackEntry{entry(1, 21), entry(2, 22)}}),
				pull(22, "b22", "b21", &DetailStack{Number: 4, Size: 3, Entries: []DetailStackEntry{entry(1, 21), entry(2, 22)}}),
			},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeMixed},
		},
		"mixed: the PRs sit in two different stacks": {
			[]Detail{pull(21, "b21", "main", stackOf(4, entry(1, 21))), pull(22, "b22", "main", stackOf(5, entry(1, 22)))},
			[]string{pr(21), pr(22)},
			Shape{Kind: ShapeMixed},
		},
	} {
		t.Run(name, func(t *testing.T) {
			gh := &shapeGitHub{detail: table(c.details...)}
			got, err := ReadShape(context.Background(), gh, c.prs)
			if err != nil {
				t.Fatalf("ReadShape: %v", err)
			}
			if c.want.Kind == ShapeMixed {
				if got.Kind != ShapeMixed {
					t.Errorf("shape = %+v, want mixed", got)
				}
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("shape = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestReadShapeReadsEveryPRFreshAndOnlyOnce(t *testing.T) {
	gh := &shapeGitHub{detail: table(pull(21, "b21", "main", nil), pull(22, "b22", "main", nil))}
	if _, err := ReadShape(context.Background(), gh, []string{pr(21), pr(22)}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gh.reads, map[string]int{pr(21): 1, pr(22): 1}) {
		t.Errorf("detail reads = %v, want one per PR", gh.reads)
	}
}

func TestReadShapeFailsWhenGitHubDoesNotAnswerForAPR(t *testing.T) {
	for name, gh := range map[string]*shapeGitHub{
		"a failed read": {detail: table(pull(21, "b21", "main", nil), pull(22, "b22", "main", nil)), failOn: pr(22)},
		"no such PR":    {detail: table(pull(21, "b21", "main", nil))},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ReadShape(context.Background(), gh, []string{pr(21), pr(22)})
			if err == nil {
				t.Errorf("shape = %+v with no error, want the failure and not a guess", got)
			}
		})
	}
}

func TestReadShapeOfNoPRsIsMixedAndReadsNothing(t *testing.T) {
	gh := &shapeGitHub{}
	got, err := ReadShape(context.Background(), gh, nil)
	if err != nil || got.Kind != ShapeMixed || len(gh.reads) != 0 {
		t.Errorf("shape = %+v, err = %v, reads = %v; want mixed, no error, no reads", got, err, gh.reads)
	}
}
