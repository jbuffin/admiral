package github

import (
	"reflect"
	"strings"
	"testing"
)

func parentOf(t *testing.T, it Item) (id, title, url, repoURL string, present bool) {
	t.Helper()
	f := reflect.ValueOf(it).FieldByName("Parent")
	if !f.IsValid() {
		t.Fatalf("Item has no Parent field: %+v", it)
	}
	if f.IsNil() {
		return "", "", "", "", false
	}
	p := f.Elem()
	s := func(n string) string {
		v := p.FieldByName(n)
		if !v.IsValid() {
			t.Fatalf("Item.Parent has no %s field", n)
		}
		return v.String()
	}
	return s("ID"), s("Title"), s("URL"), s("RepoURL"), true
}

func TestSearchGivesAnIssueItsGitHubParent(t *testing.T) {
	it := onlyItem(t, searchNodes(t, orderNode(t, "issues", j{"parent": j{"number": 188, "title": "Parent title",
		"url": "https://github.com/jbuffin/admiral/issues/188", "repository": j{"nameWithOwner": "jbuffin/admiral", "url": "https://github.com/jbuffin/admiral"}}})))
	id, title, url, repo, ok := parentOf(t, it)
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
			if id, _, _, _, ok := parentOf(t, it); ok {
				t.Errorf("parent = %q, want none", id)
			}
		})
	}
}

func TestSearchGivesAPullRequestNoParent(t *testing.T) {
	it := onlyItem(t, searchNodes(t, orderNode(t, "pull", j{"parent": j{"number": 188, "title": "P", "url": "https://github.com/jbuffin/admiral/issues/188",
		"repository": j{"nameWithOwner": "jbuffin/admiral", "url": "https://github.com/jbuffin/admiral"}}})))
	if id, _, _, _, ok := parentOf(t, it); ok {
		t.Errorf("pull request parent = %q, want none", id)
	}
}

func TestSearchQueryAsksForTheParentOnAnIssueOnly(t *testing.T) {
	const want = "parent { number title url repository { nameWithOwner url } }"
	for name, q := range map[string]string{"searchQuery": searchQuery, "searchQueryWith": searchQueryWith("", "")} {
		issue, pr, _ := strings.Cut(q, "... on PullRequest")
		if !strings.Contains(issue, want) {
			t.Errorf("%s: the Issue fragment does not ask for %q: %s", name, want, issue)
		}
		if strings.Contains(pr, "parent {") {
			t.Errorf("%s: the PullRequest fragment asks for a parent: %s", name, pr)
		}
	}
}
