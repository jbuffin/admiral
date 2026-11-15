package sources

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGitHubSourceServesAnIssuesParentOnTheRouteAndLeavesItOutOtherwise(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	parent := obj{"number": 188, "title": "Parent title", "url": "https://github.com/jbuffin/admiral/issues/188",
		"repository": obj{"nameWithOwner": "jbuffin/admiral", "url": "https://github.com/jbuffin/admiral"}}
	writeSearch(t, g, "bugs", "label:bug",
		searchNode("issues", 13, obj{"parent": parent}),
		searchNode("issues", 14, obj{"parent": nil}),
		searchNode("pull", 20, obj{"parent": parent}))
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) == 3 })

	code, body := f.do("GET", "", nil)
	if code != 200 {
		t.Fatalf("GET /api/sources = %d: %s", code, body)
	}
	var list []obj
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	orders := map[string]obj{}
	for _, s := range list {
		if s["key"] != "gha" {
			continue
		}
		views, _ := s["views"].([]any)
		for _, v := range views {
			vm, _ := v.(obj)
			if vm["id"] != "bugs" {
				continue
			}
			os, _ := vm["orders"].([]any)
			for _, o := range os {
				om, _ := o.(obj)
				orders[om["id"].(string)] = om
			}
		}
	}
	issue, bare, pr := orders["acme/widgets#13"], orders["acme/widgets#14"], orders["acme/widgets#20"]
	if issue == nil || bare == nil || pr == nil {
		t.Fatalf("served orders = %v, want 13, 14 and 20", orders)
	}
	want := obj{"id": "jbuffin/admiral#188", "title": "Parent title", "url": "https://github.com/jbuffin/admiral/issues/188", "repo": "https://github.com/jbuffin/admiral"}
	if got, _ := json.Marshal(issue["parent"]); string(got) != mustJSON(t, want) {
		t.Errorf("issue 13 parent = %s, want %s", got, mustJSON(t, want))
	}
	absent(t, bare, "the issue without a parent", "parent")
	absent(t, pr, "the pull request", "parent")
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func parseOne(t *testing.T, order obj) obj {
	t.Helper()
	raw, err := json.Marshal([]obj{{"id": "first", "title": "F", "parent": obj{"id": "a/b#9", "title": "N", "url": "https://github.com/a/b/issues/9"}}, order})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseOrders(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("parseOrders = %d orders, %v; a bad parent must not fail the list or drop the order", len(got), err)
	}
	if p, _ := asJSON(t, got[0])["parent"].(obj); p == nil || p["id"] != "a/b#9" {
		t.Fatalf("the neighbour's valid parent = %v, want it kept", p)
	}
	m := asJSON(t, got[1])
	if m["id"] != "child" {
		t.Fatalf("order = %v, want it kept", m)
	}
	return m
}

func TestParseOrdersKeepsAValidParent(t *testing.T) {
	m := parseOne(t, obj{"id": "child", "title": "C", "parent": obj{"id": "jbuffin/admiral#188", "title": "Par\nent", "url": "https://github.com/jbuffin/admiral/issues/188",
		"repo": "https://github.com/jbuffin/admiral"}})
	want := mustJSON(t, obj{"id": "jbuffin/admiral#188", "title": "Par ent", "url": "https://github.com/jbuffin/admiral/issues/188", "repo": "https://github.com/jbuffin/admiral"})
	if got := mustJSON(t, m["parent"]); got != want {
		t.Errorf("parent = %s, want %s (title on one line)", got, want)
	}
}

func TestParseOrdersFallsBackToTheIdWhenTheParentTitleIsEmpty(t *testing.T) {
	m := parseOne(t, obj{"id": "child", "title": "C", "parent": obj{"id": "a/b#1", "title": " ", "url": "https://github.com/a/b/issues/1"}})
	if p, _ := m["parent"].(obj); p == nil || p["title"] != "a/b#1" {
		t.Errorf("parent = %v, want its title to fall back to the id a/b#1", m["parent"])
	}
}

func TestParseOrdersDropsABadParentAndKeepsTheOrder(t *testing.T) {
	url := "https://github.com/a/b/issues/1"
	cases := map[string]any{
		"a string":          "a/b#1",
		"a list":            []any{"a/b#1"},
		"an empty id":       obj{"id": "", "title": "P", "url": url},
		"a control-char id": obj{"id": "a/b#1\n", "title": "P", "url": url},
		"no url":            obj{"id": "a/b#1", "title": "P"},
		"an unsafe url":     obj{"id": "a/b#1", "title": "P", "url": "javascript:alert(1)"},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			absent(t, parseOne(t, obj{"id": "child", "title": "C", "parent": bad}), "the order", "parent")
		})
	}
}

func TestParseOrdersDropsOnlyTheRepoOfAParentWhoseRepoIsBad(t *testing.T) {
	for _, repo := range []any{"not a repo", "javascript:alert(1)", 7, strings.Repeat("x", 300)} {
		m := parseOne(t, obj{"id": "child", "title": "C", "parent": obj{"id": "a/b#1", "title": "P", "url": "https://github.com/a/b/issues/1", "repo": repo}})
		p, _ := m["parent"].(obj)
		if p == nil || p["id"] != "a/b#1" {
			t.Errorf("repo %v: parent = %v, want it kept", repo, m["parent"])
			continue
		}
		absent(t, p, "the parent", "repo")
	}
}

func TestParseOrdersGivesAPullRequestNoParent(t *testing.T) {
	m := parseOne(t, obj{"id": "child", "title": "C", "pr": obj{"state": "OPEN"},
		"parent": obj{"id": "a/b#1", "title": "P", "url": "https://github.com/a/b/issues/1"}})
	absent(t, m, "the order with a pr", "parent")
	if pr, _ := m["pr"].(obj); pr == nil {
		t.Errorf("the order lost its pr: %v", m)
	}
}
