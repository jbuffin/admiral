package github

import (
	"context"
	"os"
	"strings"
	"testing"
)

func stackOf(it Item) (Stack, bool) {
	if it.Stack == nil {
		return Stack{}, false
	}
	return *it.Stack, true
}

func TestSearchGivesAPullRequestItsGitHubStack(t *testing.T) {
	it := onlyItem(t, searchNodes(t, orderNode(t, "pull", j{"stack": j{"number": 3, "size": 4}, "stackEntry": j{"position": 2}})))
	got, ok := stackOf(it)
	want := Stack{Number: 3, URL: "https://github.com/acme/widgets/stack/3", Position: 2, Size: 4}
	if !ok || got != want {
		t.Errorf("stack = %+v (present %v), want %+v", got, ok, want)
	}
}

func TestSearchGivesNoStackToAPullRequestWithoutOne(t *testing.T) {
	cases := map[string]j{
		"no stack member":       {},
		"stack null":            {"stack": nil, "stackEntry": nil},
		"stack without a layer": {"stack": j{"number": 3, "size": 4}, "stackEntry": nil},
		"stack empty":           {"stack": j{}, "stackEntry": j{"position": 1}},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			it := onlyItem(t, searchNodes(t, orderNode(t, "pull", extra)))
			if s, ok := stackOf(it); ok {
				t.Errorf("stack = %+v, want none", s)
			}
		})
	}
}

func TestSearchGivesAnIssueNoStack(t *testing.T) {
	it := onlyItem(t, searchNodes(t, orderNode(t, "issues", j{"stack": j{"number": 3, "size": 4}, "stackEntry": j{"position": 2}})))
	if s, ok := stackOf(it); ok {
		t.Errorf("issue stack = %+v, want none", s)
	}
}

func TestSearchQueryAsksForTheStackOnAPullRequestOnly(t *testing.T) {
	for name, q := range map[string]string{"searchQuery": searchQuery, "searchQueryWith": searchQueryWith("", "")} {
		issue, pr, _ := strings.Cut(q, "... on PullRequest")
		if strings.Contains(issue, "stack") {
			t.Errorf("%s: the Issue fragment asks for a stack: %s", name, issue)
		}
		for _, field := range []string{"stack {", "stackEntry {"} {
			if !strings.Contains(pr, field) {
				t.Errorf("%s: the PullRequest fragment does not ask for %q: %s", name, field, pr)
			}
		}
	}
}

func hiddenStackAnswer(errPath string) string {
	return `{"data":{"viewer":{"login":"erin"},"search":{"nodes":[` +
		`{"number":7,"title":"T","url":"https://github.com/acme/widgets/pull/7","repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"},"labels":{"nodes":[]},` +
		`"state":"OPEN","reviewDecision":null,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","statusCheckRollup":null,"headRefOid":"h1","stack":null,"stackEntry":null},` +
		`{"number":8,"title":"U","url":"https://github.com/acme/widgets/pull/8","repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"},"labels":{"nodes":[]},` +
		`"state":"OPEN","reviewDecision":null,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","statusCheckRollup":null,"headRefOid":"h2","stack":{"number":3,"size":4},"stackEntry":{"position":2}}]}},` +
		`"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by personal access token","path":` + errPath + `}]}`
}

func TestSearchKeepsTheRestWhenOnlyAStackIsUnreadable(t *testing.T) {
	for name, path := range map[string]string{
		"stack":          `["search","nodes",0,"stack"]`,
		"stackEntry":     `["search","nodes",0,"stackEntry"]`,
		"inside a stack": `["search","nodes",0,"stack","size"]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.answer(t, map[string]string{"json": hiddenStackAnswer(path)})
			items, _, err := NewClient("github.com", "", nil, f.env).SearchViewer(context.Background(), "fake-token-work-2", "is:open")
			if err != nil || len(items) != 2 {
				t.Fatalf("items = %d, err = %v; want 2 items and no error", len(items), err)
			}
			if s, ok := stackOf(items[0]); ok {
				t.Errorf("the PR with the unreadable stack has stack %+v, want none", s)
			}
			if s, ok := stackOf(items[1]); !ok || s.Number != 3 || s.Position != 2 || s.Size != 4 {
				t.Errorf("the other PR's stack = %+v (present %v), want number 3, position 2, size 4", s, ok)
			}
		})
	}
}

func TestSearchKeepsTheRestWhenGhExitsNonZeroForAnUnreadableStack(t *testing.T) {
	bin := scriptGHExit(t, hiddenStackAnswer(`["search","nodes",0,"stack"]`))
	cl := NewClient("github.com", "", nil, os.Environ())
	cl.Gh = bin
	items, _, err := cl.SearchViewer(context.Background(), "fake-token-work-2", "is:open")
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %d, err = %v; want 2 items and no error", len(items), err)
	}
}

func TestSearchStillFailsOnAnErrorOutsideAStack(t *testing.T) {
	for name, path := range map[string]string{"another field": `["search","nodes",0,"labels"]`, "no path": `null`} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.answer(t, map[string]string{"json": hiddenStackAnswer(path)})
			_, _, err := NewClient("github.com", "", nil, f.env).SearchViewer(context.Background(), "fake-token-work-2", "is:open")
			if err == nil || !strings.HasPrefix(err.Error(), "Search failed:") {
				t.Errorf("err = %v, want Search failed", err)
			}
		})
	}
}
