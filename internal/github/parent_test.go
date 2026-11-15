package github

import (
	"context"
	"os"
	"strings"
	"testing"
)

func parentOf(it Item) (id, title, url, repoURL string, present bool) {
	if it.Parent == nil {
		return "", "", "", "", false
	}
	return it.Parent.ID, it.Parent.Title, it.Parent.URL, it.Parent.RepoURL, true
}

func TestSearchGivesAnIssueItsGitHubParent(t *testing.T) {
	it := onlyItem(t, searchNodes(t, orderNode(t, "issues", j{"parent": j{"number": 188, "title": "Parent title",
		"url": "https://github.com/jbuffin/admiral/issues/188", "repository": j{"nameWithOwner": "jbuffin/admiral", "url": "https://github.com/jbuffin/admiral"}}})))
	id, title, url, repo, ok := parentOf(it)
	if !ok || id != "jbuffin/admiral#188" || title != "Parent title" || url != "https://github.com/jbuffin/admiral/issues/188" || repo != "https://github.com/jbuffin/admiral" {
		t.Errorf("parent = %q %q %q %q (present %v), want jbuffin/admiral#188, Parent title, its url and repo url", id, title, url, repo, ok)
	}
}

func TestSearchGivesNoParentToAnIssueWithoutOneOrWithAPartialOne(t *testing.T) {
	full := func(drop string) j {
		p := j{"number": 188, "title": "P", "url": "https://github.com/jbuffin/admiral/issues/188",
			"repository": j{"nameWithOwner": "jbuffin/admiral", "url": "https://github.com/jbuffin/admiral"}}
		delete(p, drop)
		return p
	}
	cases := map[string]j{
		"no parent member": {},
		"parent null":      {"parent": nil},
		"parent empty":     {"parent": j{}},
		"no number":        {"parent": full("number")},
		"no title":         {"parent": full("title")},
		"no url":           {"parent": full("url")},
		"no repository":    {"parent": full("repository")},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			it := onlyItem(t, searchNodes(t, orderNode(t, "issues", extra)))
			if id, _, _, _, ok := parentOf(it); ok {
				t.Errorf("parent = %q, want none", id)
			}
		})
	}
}

func TestSearchGivesAPullRequestNoParent(t *testing.T) {
	it := onlyItem(t, searchNodes(t, orderNode(t, "pull", j{"parent": j{"number": 188, "title": "P", "url": "https://github.com/jbuffin/admiral/issues/188",
		"repository": j{"nameWithOwner": "jbuffin/admiral", "url": "https://github.com/jbuffin/admiral"}}})))
	if id, _, _, _, ok := parentOf(it); ok {
		t.Errorf("pull request parent = %q, want none", id)
	}
}

func TestSearchQueryAsksForTheParentOnAnIssueOnly(t *testing.T) {
	for name, q := range map[string]string{"searchQuery": searchQuery, "searchQueryWith": searchQueryWith("", "")} {
		issue, pr, _ := strings.Cut(q, "... on PullRequest")
		if !strings.Contains(issue, "parent {") {
			t.Errorf("%s: the Issue fragment does not ask for a parent: %s", name, issue)
		}
		if strings.Contains(pr, "parent {") {
			t.Errorf("%s: the PullRequest fragment asks for a parent: %s", name, pr)
		}
	}
}

func hiddenParentAnswer(errPath string) string {
	return `{"data":{"viewer":{"login":"erin"},"search":{"nodes":[` +
		`{"number":7,"title":"T","url":"https://github.com/acme/widgets/issues/7","repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"},"labels":{"nodes":[]},"parent":null},` +
		`{"number":8,"title":"U","url":"https://github.com/acme/widgets/issues/8","repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"},"labels":{"nodes":[]},` +
		`"parent":{"number":1,"title":"P","url":"https://github.com/acme/widgets/issues/1","repository":{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets"}}}]}},` +
		`"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by personal access token","path":` + errPath + `}]}`
}

func TestSearchKeepsTheRestWhenOnlyAParentIsUnreadable(t *testing.T) {
	f := newFake(t)
	f.answer(t, map[string]string{"json": hiddenParentAnswer(`["search","nodes",0,"parent"]`)})
	items, _, err := NewClient("github.com", "", nil, f.env).SearchViewer(context.Background(), "fake-token-work-2", "is:open")
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %d, err = %v; want 2 items and no error", len(items), err)
	}
	if _, _, _, _, ok := parentOf(items[0]); ok {
		t.Errorf("the issue with the unreadable parent has parent %+v, want none", items[0].Parent)
	}
	if id, _, _, _, ok := parentOf(items[1]); !ok || id != "acme/widgets#1" {
		t.Errorf("the other issue's parent = %q (present %v), want acme/widgets#1", id, ok)
	}
}

func TestSearchKeepsTheRestWhenGhExitsNonZeroForAnUnreadableParent(t *testing.T) {
	bin := scriptGHExit(t, hiddenParentAnswer(`["search","nodes",0,"parent"]`))
	cl := NewClient("github.com", "", nil, os.Environ())
	cl.Gh = bin
	items, _, err := cl.SearchViewer(context.Background(), "fake-token-work-2", "is:open")
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %d, err = %v; want 2 items and no error", len(items), err)
	}
}

func TestSearchStillFailsOnAnErrorOutsideAParent(t *testing.T) {
	for name, path := range map[string]string{"another field": `["search","nodes",0,"labels"]`, "no path": `null`} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.answer(t, map[string]string{"json": hiddenParentAnswer(path)})
			_, _, err := NewClient("github.com", "", nil, f.env).SearchViewer(context.Background(), "fake-token-work-2", "is:open")
			if err == nil || !strings.HasPrefix(err.Error(), "Search failed:") {
				t.Errorf("err = %v, want Search failed", err)
			}
		})
	}
}
