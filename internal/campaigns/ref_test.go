package campaigns

import (
	"strings"
	"testing"
)

func TestParseIssueRefAcceptsTheTwoPasteForms(t *testing.T) {
	cases := []struct {
		in   string
		want IssueRef
	}{
		{"https://github.com/acme/campaign/issues/286", IssueRef{Host: "github.com", Owner: "acme", Repo: "campaign", Number: 286}},
		{"  https://acme.ghe.com/Acme-Co/my.repo_2/issues/7 \n", IssueRef{Host: "acme.ghe.com", Owner: "Acme-Co", Repo: "my.repo_2", Number: 7}},
		{"acme/campaign#12", IssueRef{Owner: "acme", Repo: "campaign", Number: 12}},
	}
	for _, tc := range cases {
		got, err := ParseIssueRef(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseIssueRef(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
	}
}

func TestParseIssueRefRefusesWithAMessageToShow(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a pull request URL", "https://github.com/acme/campaign/pull/9", "pull request"},
		{"extra path", "https://github.com/acme/campaign/issues/9/files", "issue"},
		{"empty", "", "issue"},
		{"no number", "https://github.com/acme/campaign/issues/", "issue"},
		{"a non-numeric number", "https://github.com/acme/campaign/issues/abc", "issue"},
		{"number with a query string", "https://github.com/acme/campaign/issues/9?x=1", "issue"},
		{"a bad character in the repo", "https://github.com/acme/camp aign/issues/9", "issue"},
		{"shell syntax in the owner", "acme;rm/campaign#9", "issue"},
		{"a repo that is not owner/repo", "campaign#9", "issue"},
		{"not http", "ftp://github.com/acme/campaign/issues/9", "issue"},
		{"zero", "acme/campaign#0", "issue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseIssueRef(tc.in)
			if err == nil {
				t.Fatalf("ParseIssueRef(%q) = %+v, want an error", tc.in, got)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("message %q does not say %q", err, tc.want)
			}
		})
	}
}

func TestAGitHubURLWithAFlagOrDotPathOwnerOrRepoIsRefused(t *testing.T) {
	for _, u := range []string{
		"https://github.com/-x/widget/pull/1",
		"https://github.com/acme/-x/pull/1",
		"https://github.com/./widget/pull/1",
		"https://github.com/acme/../pull/1",
	} {
		if _, err := ParseGitHubURL(u, []string{"github.com"}, true); err == nil {
			t.Errorf("ParseGitHubURL(%q) accepted it, want a refusal", u)
		}
	}
}
