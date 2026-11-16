package moor

import (
	"fmt"
	"slices"
	"testing"
)

func cpr(n int, head, base string) ChainPR {
	return ChainPR{URL: pr(n), State: "OPEN", Head: head, Base: base, DefaultBranch: "main"}
}

func chainURLs(chains [][]ChainPR) [][]string {
	out := [][]string{}
	for _, c := range chains {
		urls := []string{}
		for _, p := range c {
			urls = append(urls, p.URL)
		}
		out = append(out, urls)
	}
	return out
}

func TestChainsSplitsAViewsPRsIntoChainsBottomToTop(t *testing.T) {
	other := func(n int, head, base string) ChainPR {
		p := cpr(n, head, base)
		p.URL = "https://github.com/acme/gadgets/pull/" + fmt.Sprint(n)
		return p
	}
	closed := cpr(32, "b32", "b31")
	closed.State = "MERGED"
	stacked := cpr(32, "b32", "b31")
	stacked.Stacked = true
	for name, c := range map[string]struct {
		prs  []ChainPR
		want [][]string
	}{
		"three PRs given out of order chain bottom to top": {
			[]ChainPR{cpr(33, "b33", "b32"), cpr(31, "b31", "main"), cpr(32, "b32", "b31")},
			[][]string{{pr(31), pr(32), pr(33)}},
		},
		"two separate chains in one view": {
			[]ChainPR{cpr(31, "b31", "main"), cpr(41, "b41", "main"), cpr(32, "b32", "b31"), cpr(42, "b42", "b41")},
			[][]string{{pr(31), pr(32)}, {pr(41), pr(42)}},
		},
		"a chain next to an unrelated PR on the default branch": {
			[]ChainPR{cpr(31, "b31", "main"), cpr(32, "b32", "b31"), cpr(50, "b50", "main")},
			[][]string{{pr(31), pr(32)}},
		},
		"a fork gives no chain": {
			[]ChainPR{cpr(31, "b31", "main"), cpr(32, "b32", "b31"), cpr(33, "b33", "b31")},
			nil,
		},
		"a PR branching off the middle leaves the longer run a chain": {
			[]ChainPR{cpr(31, "b31", "main"), cpr(32, "b32", "b31"), cpr(33, "b33", "b32"), cpr(34, "b34", "b33"), cpr(40, "b40", "b32")},
			[][]string{{pr(31), pr(32), pr(33), pr(34)}},
		},
		"an even fork above the bottom ends the chain below it": {
			[]ChainPR{cpr(31, "b31", "main"), cpr(32, "b32", "b31"), cpr(33, "b33", "b32"), cpr(34, "b34", "b32")},
			[][]string{{pr(31), pr(32)}},
		},
		"a branch loop ends the chain where it closes": {
			[]ChainPR{cpr(31, "b31", "main"), cpr(32, "b32", "b31"), cpr(33, "b31", "b32")},
			[][]string{{pr(31), pr(32), pr(33)}},
		},
		"a bottom off the default branch gives no chain": {
			[]ChainPR{cpr(31, "b31", "develop"), cpr(32, "b32", "b31")},
			nil,
		},
		"one PR is no chain": {
			[]ChainPR{cpr(31, "b31", "main")},
			nil,
		},
		"the same branch names in two repositories chain nothing": {
			[]ChainPR{cpr(31, "b31", "main"), other(32, "b32", "b31")},
			nil,
		},
		"a PR in a GitHub stack gives no chain": {
			[]ChainPR{cpr(31, "b31", "main"), stacked},
			nil,
		},
		"a merged PR does not chain": {
			[]ChainPR{cpr(31, "b31", "main"), closed},
			nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := chainURLs(Chains(c.prs))
			want := c.want
			if want == nil {
				want = [][]string{}
			}
			if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
				t.Errorf("Chains = %v, want %v", got, want)
			}
		})
	}
}
