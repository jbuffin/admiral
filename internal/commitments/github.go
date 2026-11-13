package commitments

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
	"github.com/jbuffin/admiral/internal/ledger"
	"github.com/jbuffin/admiral/internal/moor"
)

// GHPoll is how often the runner asks GitHub about the PRs and issues its commitments wait on.
var GHPoll = delay.From("ADMIRAL_COMMITMENT_GH_POLL", 2*time.Minute)

// GitHub is the part of *github.Client the runner polls with: one batched call per host.
type GitHub interface {
	// PullRequests leaves out a PR GitHub answers NOT_FOUND for; a *moor.Unanswered beside the answer is a repository it could not look in.
	PullRequests(ctx context.Context, urls []string) (map[string]moor.Status, error)
	// IssueStates maps each issue URL to OPEN or CLOSED, leaving out an issue GitHub answers NOT_FOUND for, with the same *moor.Unanswered rule.
	IssueStates(ctx context.Context, urls []string) (map[string]string, error)
}

// ghAnswer is what GitHub last said about one subject: its state, or that it is gone.
type ghAnswer struct {
	state string // OPEN, CLOSED or MERGED
	gone  bool
}

// The GitHub rows of the trigger-and-drop table, beside the ship rows in shiplog.go (every row has a test):
//
//	answer                 pr_merged                          pr_closed   issue_closed
//	OPEN                   waits                              waits       waits
//	MERGED                 fires                              fires       -
//	CLOSED                 drops: closed without merging      fires       fires
//	gone (item NOT_FOUND)  drops                              drops       drops
//
// No answer (a failed call, a repository GitHub could not look in, no client) never reaches this table: the commitment waits.
func ghVerdict(kind string, a ghAnswer) (verdictKind, string) {
	if a.gone {
		return verdictDrop, "the " + noun(kind) + " no longer exists"
	}
	switch {
	case kind == ledger.TriggerPRMerged && a.state == "MERGED",
		kind == ledger.TriggerPRClosed && (a.state == "MERGED" || a.state == "CLOSED"),
		kind == ledger.TriggerIssueClosed && a.state == "CLOSED":
		return verdictFire, ""
	case kind == ledger.TriggerPRMerged && a.state == "CLOSED":
		return verdictDrop, "the pull request was closed without merging"
	}
	return verdictWait, ""
}

func noun(kind string) string {
	if kind == ledger.TriggerIssueClosed {
		return "issue"
	}
	return "pull request"
}

func isGitHubKind(kind string) bool {
	return kind == ledger.TriggerPRMerged || kind == ledger.TriggerPRClosed || kind == ledger.TriggerIssueClosed
}

// pollKey names a subject within a fleet: two fleets may watch one URL through different clients.
func pollKey(fleet, subject string) string { return fleet + "\x00" + subject }

type hostKey struct{ fleet, host string }

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// answerOf is the last answer for a subject, if GitHub gave one.
func (r *Runner) answerOf(fleet, subject string) (ghAnswer, bool) {
	r.gmu.Lock()
	defer r.gmu.Unlock()
	a, ok := r.answers[pollKey(fleet, subject)]
	return a, ok
}

// pollLoop asks GitHub on its own timer so a slow `gh` never holds the tick; a changed answer wakes the runner.
func (r *Runner) pollLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if r.pollGitHub(ctx) {
			r.Changed()
		}
		select {
		case <-ctx.Done():
			return
		case <-r.Clock.After(GHPoll):
		}
	}
}

// pollGitHub makes one batched call per kind per host and reports whether any answer changed.
func (r *Runner) pollGitHub(ctx context.Context) bool {
	type batch struct{ prs, issues []string }
	subjects := map[hostKey]*batch{}
	var order []hostKey
	seen := map[string]bool{}
	for _, fleet := range r.Fleets() {
		list, err := r.Ledger.Commitments(fleet)
		if err != nil {
			continue
		}
		for _, c := range list {
			if !isGitHubKind(c.Trigger.Kind) || c.Waiting || seen[pollKey(fleet, c.Trigger.Subject)] {
				continue
			}
			u, err := url.Parse(c.Trigger.Subject)
			if err != nil || u.Host == "" {
				continue
			}
			seen[pollKey(fleet, c.Trigger.Subject)] = true
			k := hostKey{fleet, strings.ToLower(u.Host)}
			b := subjects[k]
			if b == nil {
				b = &batch{}
				subjects[k] = b
				order = append(order, k)
			}
			if c.Trigger.Kind == ledger.TriggerIssueClosed {
				b.issues = append(b.issues, c.Trigger.Subject)
			} else {
				b.prs = append(b.prs, c.Trigger.Subject)
			}
		}
	}

	answers := map[string]ghAnswer{}
	failing := map[string]bool{}
	fail := func(fleet, subject string, err error) {
		key := pollKey(fleet, subject)
		failing[key] = true
		r.gmu.Lock()
		again := r.failing[key]
		r.gmu.Unlock()
		if !again {
			r.logf("admiral: commitment trigger %s: GitHub did not answer: %v", subject, err)
		}
	}
	for _, k := range order {
		b := subjects[k]
		var client GitHub
		if r.GitHubFor != nil {
			client = r.GitHubFor(k.fleet, k.host)
		}
		all := append(append([]string(nil), b.prs...), b.issues...)
		if client == nil {
			for _, u := range all {
				fail(k.fleet, u, errors.New("no GitHub source of this fleet uses "+k.host))
			}
			continue
		}
		if len(b.prs) > 0 {
			got, err := client.PullRequests(ctx, b.prs)
			states := make(map[string]string, len(got))
			for u, st := range got {
				states[u] = st.State
			}
			r.collect(k.fleet, b.prs, states, err, answers, fail)
		}
		if len(b.issues) > 0 {
			got, err := client.IssueStates(ctx, b.issues)
			r.collect(k.fleet, b.issues, got, err, answers, fail)
		}
		if ctx.Err() != nil {
			return false
		}
	}

	r.gmu.Lock()
	defer r.gmu.Unlock()
	// A failed subject keeps what it last answered: the commitment stays as it was.
	changed := false
	for key, a := range r.answers {
		if _, again := answers[key]; !again && failing[key] {
			answers[key] = a
		}
	}
	for key, a := range answers {
		if old, ok := r.answers[key]; !ok || old != a {
			changed = true
		}
	}
	r.answers, r.failing = answers, failing
	return changed
}

// collect sorts one call's answer: a URL it left out of a call that otherwise answered is gone, unless it is in a repository GitHub could not look in.
func (r *Runner) collect(fleet string, urls []string, states map[string]string, err error, answers map[string]ghAnswer, fail func(fleet, subject string, err error)) {
	unanswered := map[string]bool{}
	var un *moor.Unanswered
	switch {
	case errors.As(err, &un):
		for _, u := range un.URLs {
			unanswered[u] = true
		}
	case err != nil:
		for _, u := range urls {
			fail(fleet, u, err)
		}
		return
	}
	for _, u := range urls {
		st, ok := states[u]
		switch {
		case ok:
			answers[pollKey(fleet, u)] = ghAnswer{state: st}
		case unanswered[u]:
			fail(fleet, u, un)
		default:
			answers[pollKey(fleet, u)] = ghAnswer{gone: true}
		}
	}
}
