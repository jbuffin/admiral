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
// first appearance of its bottom PR in prs. A chain starts at a PR on its repository's default branch and climbs
// through the PRs based on the head below; where several are, it follows the strictly longest run and leaves the
// rest out, and an even fork ends it. Two or more PRs make a chain; a bottom off the default branch gives none.
func Chains(prs []ChainPR) [][]ChainPR {
	var open []ChainPR
	for _, p := range prs {
		if p.State == "OPEN" && !p.Stacked {
			open = append(open, p)
		}
	}
	above := make([][]int, len(open))
	for i, p := range open {
		for j, q := range open {
			if i != j && q.Base == p.Head && sameRepo(p.URL, q.URL) {
				above[i] = append(above[i], j)
			}
		}
	}
	onPath := make([]bool, len(open))
	var climb func(int) []int
	climb = func(i int) []int {
		onPath[i] = true
		defer func() { onPath[i] = false }()
		var best []int
		tie := false
		for _, j := range above[i] {
			if onPath[j] {
				continue
			}
			run := climb(j)
			switch {
			case len(run) > len(best):
				best, tie = run, false
			case len(run) == len(best):
				tie = true
			}
		}
		if tie {
			best = nil
		}
		return append([]int{i}, best...)
	}
	used := make([]bool, len(open))
	var out [][]ChainPR
	for i, p := range open {
		if p.Base != p.DefaultBranch {
			continue
		}
		run := climb(i)
		if len(run) < 2 || slices.ContainsFunc(run, func(k int) bool { return used[k] }) {
			continue
		}
		chain := make([]ChainPR, len(run))
		for k, j := range run {
			chain[k] = open[j]
			used[j] = true
		}
		out = append(out, chain)
	}
	return out
}

func sameRepo(a, b string) bool {
	ra, oka := RepoOf(a)
	rb, okb := RepoOf(b)
	return oka && okb && strings.EqualFold(ra, rb)
}
