package sources

import (
	"encoding/json"
	"testing"
)

func TestGitHubSourceServesAPullRequestsStackOnTheRouteAndLeavesItOutOtherwise(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	stacked := obj{"stack": obj{"number": 3, "size": 4}, "stackEntry": obj{"position": 2}}
	writeSearch(t, g, "bugs", "label:bug",
		searchNode("pull", 21, stacked),
		searchNode("pull", 22, obj{"stack": nil, "stackEntry": nil}),
		searchNode("issues", 13, stacked))
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
	pr, bare, issue := orders["acme/widgets#21"], orders["acme/widgets#22"], orders["acme/widgets#13"]
	if pr == nil || bare == nil || issue == nil {
		t.Fatalf("served orders = %v, want 21, 22 and 13", orders)
	}
	want := obj{"number": 3, "url": "https://github.com/acme/widgets/stack/3", "position": 2, "size": 4}
	if got := mustJSON(t, pr["stack"]); got != mustJSON(t, want) {
		t.Errorf("pull request 21 stack = %s, want %s", got, mustJSON(t, want))
	}
	absent(t, bare, "the pull request without a stack", "stack")
	absent(t, issue, "the issue", "stack")
}

const goodStackURL = "https://github.com/a/b/stack/3"

func parseOneStack(t *testing.T, order obj) obj {
	t.Helper()
	raw, err := json.Marshal([]obj{{"id": "first", "title": "F", "pr": obj{"state": "OPEN"},
		"stack": obj{"number": 9, "url": "https://github.com/a/b/stack/9", "position": 1, "size": 2}}, order})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseOrders(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("parseOrders = %d orders, %v; a bad stack must not fail the list or drop the order", len(got), err)
	}
	if s, _ := asJSON(t, got[0])["stack"].(obj); s == nil || s["number"] != float64(9) {
		t.Fatalf("the neighbour's valid stack = %v, want it kept", s)
	}
	m := asJSON(t, got[1])
	if m["id"] != "layer" {
		t.Fatalf("order = %v, want it kept", m)
	}
	return m
}

func TestParseOrdersKeepsAValidStackOnAPullRequest(t *testing.T) {
	m := parseOneStack(t, obj{"id": "layer", "title": "L", "pr": obj{"state": "OPEN"},
		"stack": obj{"number": 3, "url": goodStackURL, "position": 2, "size": 4}})
	want := mustJSON(t, obj{"number": 3, "url": goodStackURL, "position": 2, "size": 4})
	if got := mustJSON(t, m["stack"]); got != want {
		t.Errorf("stack = %s, want %s", got, want)
	}
}

func TestParseOrdersDropsABadStackAndKeepsTheOrder(t *testing.T) {
	cases := map[string]any{
		"a string":          goodStackURL,
		"a list":            []any{3},
		"no number":         obj{"url": goodStackURL, "position": 2, "size": 4},
		"a zero number":     obj{"number": 0, "url": goodStackURL, "position": 2, "size": 4},
		"a fractional one":  obj{"number": 3.5, "url": goodStackURL, "position": 2, "size": 4},
		"a string number":   obj{"number": "3", "url": goodStackURL, "position": 2, "size": 4},
		"no url":            obj{"number": 3, "position": 2, "size": 4},
		"an unsafe url":     obj{"number": 3, "url": "javascript:alert(1)", "position": 2, "size": 4},
		"no position":       obj{"number": 3, "url": goodStackURL, "size": 4},
		"a negative layer":  obj{"number": 3, "url": goodStackURL, "position": -1, "size": 4},
		"no size":           obj{"number": 3, "url": goodStackURL, "position": 2},
		"a zero size":       obj{"number": 3, "url": goodStackURL, "position": 2, "size": 0},
		"a fractional size": obj{"number": 3, "url": goodStackURL, "position": 2, "size": 2.5},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			m := parseOneStack(t, obj{"id": "layer", "title": "L", "pr": obj{"state": "OPEN"}, "stack": bad})
			absent(t, m, "the order", "stack")
			if pr, _ := m["pr"].(obj); pr == nil {
				t.Errorf("the order lost its pr: %v", m)
			}
		})
	}
}

func TestParseOrdersGivesAnIssueNoStack(t *testing.T) {
	m := parseOneStack(t, obj{"id": "layer", "title": "L",
		"stack": obj{"number": 3, "url": goodStackURL, "position": 2, "size": 4}})
	absent(t, m, "the issue order", "stack")
}
