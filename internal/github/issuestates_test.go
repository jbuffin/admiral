package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

// recordingGH writes every gh call's argv to a file as NUL-separated words, two NULs ending a call.
func recordingGH(t *testing.T) (bin, rec string) {
	t.Helper()
	dir := t.TempDir()
	rec = filepath.Join(dir, "argv")
	bin = filepath.Join(dir, "gh")
	script := `#!/bin/sh
if [ "$1" = auth ]; then echo fake-token-work-2; exit 0; fi
for a in "$@"; do printf '%s\0' "$a" >> "$REC"; done
printf '\0\0' >> "$REC"
echo '{"data":{"viewer":{"login":"fake-viewer"}}}'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, rec
}

func recordedCalls(t *testing.T, rec string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(rec)
	if err != nil {
		return nil
	}
	var calls [][]string
	// A call's last word ends in a NUL and the script adds two more, so a call's separator leaves one NUL on the next.
	for _, c := range strings.Split(string(raw), "\x00\x00") {
		if c = strings.TrimPrefix(c, "\x00"); c != "" {
			calls = append(calls, strings.Split(strings.TrimSuffix(c, "\x00"), "\x00"))
		}
	}
	return calls
}

func TestIssueStatesNeverPutsAnOwnerOrRepoWhereGhReadsAFlag(t *testing.T) {
	bin, rec := recordingGH(t)
	cl := NewClient("github.com", "", nil, append(os.Environ(), "REC="+rec))
	cl.Gh = bin
	urls := []string{
		"https://github.com/-x/widgets/issues/3",
		"https://github.com/acme/--web/issues/4",
	}
	_, _ = cl.IssueStates(context.Background(), urls)

	calls := recordedCalls(t, rec)
	if len(calls) == 0 {
		t.Fatalf("IssueStates ran no gh call for %v", urls)
	}
	for _, argv := range calls {
		for i, a := range argv {
			for _, bad := range []string{"-x", "--web"} {
				if a == bad || strings.HasPrefix(a, bad+"=") {
					t.Errorf("argv %q has %q as a word of its own: gh would read it as a flag", argv, a)
				}
				if strings.Contains(a, bad) && !strings.HasPrefix(a, "query=") && !(i > 0 && (argv[i-1] == "-f" || argv[i-1] == "-F")) && !strings.HasPrefix(a, "https://") {
					t.Errorf("argv %q carries %q in %q, outside a query, a -f value or a URL", argv, bad, a)
				}
			}
		}
	}
	joined := strings.Join(calls[0], " ")
	if !strings.Contains(joined, "-x") || !strings.Contains(joined, "--web") {
		t.Errorf("argv %q never names the issues' owner or repo: the lookup asks about nothing", calls[0])
	}
}

func TestIssueStatesAsksOnceForEveryURL(t *testing.T) {
	bin, rec := recordingGH(t)
	cl := NewClient("github.com", "", nil, append(os.Environ(), "REC="+rec))
	cl.Gh = bin
	_, _ = cl.IssueStates(context.Background(), []string{"https://github.com/acme/widgets/issues/3", "https://github.com/acme/widgets/issues/5", "https://github.com/globex/app/issues/1"})
	calls := recordedCalls(t, rec)
	if len(calls) != 1 {
		t.Fatalf("%d gh calls (%q), want one batched call", len(calls), calls)
	}
	q := strings.Join(calls[0], " ")
	for _, want := range []string{"widgets", "globex", "3", "5", "1"} {
		if !strings.Contains(q, want) {
			t.Errorf("the one call %q does not mention %q", q, want)
		}
	}
}

// cannedGH answers every graphql call with stdout and exit code.
func cannedGH(t *testing.T, stdout string, code int, stderr string) *Client {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	body := filepath.Join(dir, "out")
	if err := os.WriteFile(body, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = auth ]; then echo fake-token-work-2; exit 0; fi\ncat " + body + "\necho '" + stderr + "' >&2\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cl := NewClient("github.com", "", nil, os.Environ())
	cl.Gh = bin
	return cl
}

func TestIssueStatesOrGoneCountsOnlyAnItemLevelNotFoundAsGone(t *testing.T) {
	const (
		open  = "https://github.com/acme/widgets/issues/1"
		gone  = "https://github.com/acme/widgets/issues/2"
		blank = "https://github.com/acme/widgets/issues/3"
		repo  = "https://github.com/acme/hidden/issues/4"
	)
	out := `{"data":{"i0":{"issue":{"state":"OPEN"}},"i1":{"issue":null},"i2":{"issue":{"state":""}},"i3":null},"errors":[` +
		`{"type":"NOT_FOUND","path":["i1","issue"],"message":"no issue"},` +
		`{"type":"NOT_FOUND","path":["i3"],"message":"no repository"}]}`
	got, goneURLs, err := cannedGH(t, out, 1, "").IssueStatesOrGone(context.Background(), []string{open, gone, blank, repo})
	var un *moor.Unanswered
	if !errors.As(err, &un) || len(un.URLs) != 1 || un.URLs[0] != repo {
		t.Fatalf("error = %v, want unanswered for only %s", err, repo)
	}
	if len(got) != 1 || got[open] != "OPEN" {
		t.Errorf("answered = %v, want only %s OPEN: an empty state is not an answer", got, open)
	}
	if len(goneURLs) != 1 || goneURLs[0] != gone {
		t.Errorf("gone = %v, want only %s: a URL left out for any other reason is unanswered", goneURLs, gone)
	}
}

func TestPullRequestsOrGoneNamesTheItemGoneAndNotTheUnanswered(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "", "", "MERGEABLE")
	f.write(t, "pr_notfound/"+keyOf.Replace(prB), "x")
	f.write(t, "repo_notfound/globex_app", "x")
	got, gone, err := cl.PullRequestsOrGone(context.Background(), []string{prA, prB, prC})
	var un *moor.Unanswered
	if !errors.As(err, &un) || len(un.URLs) != 1 || un.URLs[0] != prC {
		t.Fatalf("error = %v, want unanswered for only %s", err, prC)
	}
	if got[prA].State != "OPEN" || len(gone) != 1 || gone[0] != prB {
		t.Errorf("answered %v and gone %v, want %s answered and only %s gone", got, gone, prA, prB)
	}
}

func TestAFailedIssueLookupSaysIssueAndAFailedPullRequestLookupSaysPullRequest(t *testing.T) {
	_, err := cannedGH(t, "", 1, "gh: HTTP 502").IssueStates(context.Background(), []string{"https://github.com/acme/widgets/issues/1"})
	if err == nil || !strings.Contains(err.Error(), "issue lookup failed") || strings.Contains(err.Error(), "pull request") {
		t.Errorf("issue error = %v, want it to say the issue lookup failed", err)
	}
	_, err = cannedGH(t, "", 1, "gh: HTTP 502").PullRequests(context.Background(), []string{"https://github.com/acme/widgets/pull/1"})
	if err == nil || !strings.Contains(err.Error(), "pull request lookup failed") {
		t.Errorf("pull request error = %v, want the existing wording", err)
	}
}
