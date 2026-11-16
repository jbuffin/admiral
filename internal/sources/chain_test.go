package sources

import "testing"

const widgets = "https://github.com/acme/widgets/pull/"

// chainNode is an open pull request with its branches; the repository's default branch is main.
func chainNode(n int, head, base, author string, extra obj) obj {
	node := searchNode("pull", n, obj{"url": widgets + itoa(n), "headRefName": head, "baseRefName": base,
		"author": obj{"login": author}, "repository": obj{"nameWithOwner": "acme/widgets", "url": "https://github.com/acme/widgets",
			"defaultBranchRef": obj{"name": "main"}}})
	for k, v := range extra {
		node[k] = v
	}
	return node
}

func serveChains(t *testing.T, nodes ...obj) map[string]obj {
	t.Helper()
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	writeSearch(t, g, "bugs", "label:bug", nodes...)
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	src := f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) == len(nodes) })
	return servedOrders(t, src, "bugs")
}

func TestGitHubSourceGivesChainedPullRequestsAChainBottomToTop(t *testing.T) {
	orders := serveChains(t,
		chainNode(33, "b33", "b32", "fake-viewer", nil),
		chainNode(31, "b31", "main", "fake-viewer", nil),
		chainNode(32, "b32", "b31", "fake-viewer", nil),
		chainNode(50, "b50", "main", "fake-viewer", nil),
		searchNode("issues", 13, obj{"author": obj{"login": "fake-viewer"}}))

	for n, c := range map[int]obj{
		31: {"id": widgets + "31", "position": 1, "size": 3, "head": "b31", "base": "main", "mine": true},
		32: {"id": widgets + "31", "position": 2, "size": 3, "head": "b32", "base": "b31", "mine": true},
		33: {"id": widgets + "31", "position": 3, "size": 3, "head": "b33", "base": "b32", "mine": true},
	} {
		o := orders["acme/widgets#"+itoa(n)]
		if got, want := mustJSON(t, o["chain"]), mustJSON(t, c); got != want {
			t.Errorf("pull request %d chain = %s, want %s", n, got, want)
		}
	}
	absent(t, orders["acme/widgets#50"], "the PR no other is based on", "chain")
	absent(t, orders["acme/widgets#13"], "the issue", "chain")
}

func TestGitHubSourceMarksAChainMineOnlyWhenEveryPullRequestIsTheViewers(t *testing.T) {
	orders := serveChains(t,
		chainNode(31, "b31", "main", "fake-viewer", nil),
		chainNode(32, "b32", "b31", "someone-else", nil))
	for _, n := range []string{"acme/widgets#31", "acme/widgets#32"} {
		c, _ := orders[n]["chain"].(obj)
		if c == nil || c["mine"] != false || c["size"] != float64(2) {
			t.Errorf("%s chain = %v, want a chain of 2 that is not mine", n, orders[n]["chain"])
		}
	}
}

func TestGitHubSourceKeepsAStackOnPullRequestsGitHubStackedAndGivesThemNoChain(t *testing.T) {
	stack := obj{"stack": obj{"number": 3, "size": 2}}
	orders := serveChains(t,
		chainNode(31, "b31", "main", "fake-viewer", obj{"stack": stack["stack"], "stackEntry": obj{"position": 1}}),
		chainNode(32, "b32", "b31", "fake-viewer", obj{"stack": stack["stack"], "stackEntry": obj{"position": 2}}))
	for _, n := range []string{"acme/widgets#31", "acme/widgets#32"} {
		if orders[n]["stack"] == nil {
			t.Errorf("%s = %v, want its GitHub stack kept", n, orders[n])
		}
		absent(t, orders[n], n, "chain")
	}
}
