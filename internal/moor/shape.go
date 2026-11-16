package moor

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

// ShapeKind is how a ship's pull requests relate on GitHub.
type ShapeKind string

const (
	ShapeStacked     ShapeKind = "stacked"     // exactly one GitHub stack holds them
	ShapeChained     ShapeKind = "chained"     // one chain by base and head branch, in no GitHub stack
	ShapeIndependent ShapeKind = "independent" // every one is based on the default branch, in no stack
	ShapeMixed       ShapeKind = "mixed"       // anything else
)

// Shape is a ship's PR shape as GitHub has it now. PRs is bottom to top for stacked and chained, and as given for independent.
type Shape struct {
	Kind        ShapeKind
	StackNumber int    // stacked only
	Base        string // chained only: the bottom PR's base, the default branch
	PRs         []string
}

// ReadShape reads each of the ship's PRs fresh and says how they relate. A failed read is an error, never a shape.
func ReadShape(ctx context.Context, gh GitHub, prs []string) (Shape, error) {
	if len(prs) == 0 {
		return Shape{Kind: ShapeMixed}, nil
	}
	details := make([]Detail, len(prs))
	for i, u := range prs {
		d, err := gh.PullRequestDetail(ctx, u)
		if err != nil {
			return Shape{}, err
		}
		details[i] = d
	}
	return ShapeOf(prs, details), nil
}

// ShapeOf says how prs relate given each one's detail, read fresh by the caller; details[i] is prs[i]'s.
func ShapeOf(prs []string, details []Detail) Shape {
	mixed := Shape{Kind: ShapeMixed}
	if s, ok := stackedShape(prs, details); ok {
		return s
	}
	anyStack := false
	for _, d := range details {
		anyStack = anyStack || d.Stack != nil
	}
	if anyStack {
		return mixed
	}

	independent := true
	for _, d := range details {
		independent = independent && d.Base == d.DefaultBranch
	}
	if independent {
		return Shape{Kind: ShapeIndependent, PRs: slices.Clone(prs)}
	}
	if !oneRepo(prs) {
		return mixed // branch names chain nothing across repositories
	}

	chain := make([]ChainPR, len(details))
	for i, d := range details {
		chain[i] = ChainPR{URL: prs[i], Head: d.Head, Base: d.Base, DefaultBranch: d.DefaultBranch}
	}
	order, ok := lineUp(chain)
	if !ok {
		return mixed
	}
	ordered := make([]string, len(order))
	for k, i := range order {
		ordered[k] = prs[i]
	}
	return Shape{Kind: ShapeChained, Base: details[0].DefaultBranch, PRs: ordered}
}

// stackedShape is the shape when every PR sits in one GitHub stack whose entries are exactly the ship's PRs.
func stackedShape(prs []string, details []Detail) (Shape, bool) {
	st := details[0].Stack
	if st == nil || len(st.Entries) != len(prs) || st.Size != len(prs) {
		return Shape{}, false
	}
	for _, d := range details {
		if d.Stack == nil || d.Stack.Number != st.Number {
			return Shape{}, false
		}
	}
	// Match by number within the ship's repository, not by URL text, which GitHub may case differently.
	_, repo, _, _ := ParsePR(prs[0])
	byNumber := map[int]string{}
	for _, u := range prs {
		r, ok := RepoOf(u)
		_, _, n, _ := ParsePR(u)
		num, err := strconv.Atoi(n)
		if !ok || !strings.EqualFold(r, repo) || err != nil {
			return Shape{}, false
		}
		byNumber[num] = u
	}
	entries := slices.Clone(st.Entries)
	slices.SortFunc(entries, func(a, b DetailStackEntry) int { return a.Position - b.Position })
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		u, ok := byNumber[e.Number]
		if !ok {
			return Shape{}, false
		}
		delete(byNumber, e.Number)
		out = append(out, u)
	}
	return Shape{Kind: ShapeStacked, StackNumber: st.Number, PRs: out}, true
}

// oneRepo says every URL is a PR of the same repository, compared case-insensitively.
func oneRepo(prs []string) bool {
	var first string
	for i, u := range prs {
		r, ok := RepoOf(u)
		if !ok || (i > 0 && !strings.EqualFold(r, first)) {
			return false
		}
		first = r
	}
	return true
}
