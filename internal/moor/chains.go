package moor

import (
	"slices"
	"strings"
)

// ChainPR is one pull request as chain detection reads it.
type ChainPR struct {
	URL           string
	State         string
	Head          string
	Base          string
	DefaultBranch string
	Stacked       bool // in a GitHub stack
}

// lineUp orders prs bottom to top when they are exactly one chain of one repository: the bottom is on the default
// branch, each next PR is based on the head of the one below, and no two share a base. It returns indexes into prs.
func lineUp(prs []ChainPR) ([]int, bool) {
	byBase := map[string]int{}
	for i, p := range prs {
		if _, dup := byBase[p.Base]; dup {
			return nil, false
		}
		byBase[p.Base] = i
	}
	order := make([]int, 0, len(prs))
	base := prs[0].DefaultBranch
	for range prs {
		i, ok := byBase[base]
		if !ok {
			return nil, false
		}
		order = append(order, i)
		base = prs[i].Head
	}
	return order, true
}

// Chains splits the open pull requests outside any GitHub stack into chains, each bottom to top and ordered by the
// first appearance of its bottom PR in prs. A chain is two or more PRs of one repository that lineUp accepts; a fork
// or a bottom off the default branch gives none.
func Chains(prs []ChainPR) [][]ChainPR {
	var open []ChainPR
	for _, p := range prs {
		if p.State == "OPEN" && !p.Stacked {
			open = append(open, p)
		}
	}
	// Union each PR with the one whose head is its base, within a repository.
	parent := make([]int, len(open))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, p := range open {
		for j, q := range open {
			if i != j && p.Base == q.Head && sameRepo(p.URL, q.URL) {
				parent[find(i)] = find(j)
			}
		}
	}
	groups := map[int][]ChainPR{}
	var roots []int
	for i, p := range open {
		r := find(i)
		if _, seen := groups[r]; !seen {
			roots = append(roots, r)
		}
		groups[r] = append(groups[r], p)
	}
	type found struct {
		bottom int
		chain  []ChainPR
	}
	var all []found
	for _, r := range roots {
		g := groups[r]
		if len(g) < 2 {
			continue
		}
		order, ok := lineUp(g)
		if !ok {
			continue
		}
		chain := make([]ChainPR, len(order))
		for k, i := range order {
			chain[k] = g[i]
		}
		bottom := slices.IndexFunc(prs, func(p ChainPR) bool { return p.URL == chain[0].URL })
		all = append(all, found{bottom, chain})
	}
	slices.SortFunc(all, func(a, b found) int { return a.bottom - b.bottom })
	var out [][]ChainPR
	for _, f := range all {
		out = append(out, f.chain)
	}
	return out
}

func sameRepo(a, b string) bool {
	ra, oka := RepoOf(a)
	rb, okb := RepoOf(b)
	return oka && okb && strings.EqualFold(ra, rb)
}
