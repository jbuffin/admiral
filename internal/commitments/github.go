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
	// PullRequestsOrGone lists the PRs GitHub answered NOT_FOUND for beside the answer; a *moor.Unanswered is a repository it could not look in.
	// A URL in none of the three was not answered.
	PullRequestsOrGone(ctx context.Context, urls []string) (map[string]moor.Status, []string, error)
	// IssueStatesOrGone maps each issue URL to OPEN or CLOSED, with the same gone and *moor.Unanswered rules.
	IssueStatesOrGone(ctx context.Context, urls []string) (map[string]string, []string, error)
}

// ghAnswer is what GitHub said about one subject when a poll started: its state, or that it is gone.
type ghAnswer struct {
	state string // OPEN, CLOSED or MERGED
	gone  bool
	at    time.Time // when the poll that got it started
}

// judges reports whether the answer was asked after the commitment was recorded. A commitment's stamp is cut to the
// second, so a poll that started within that second might have come first and does not count.
func (a ghAnswer) judges(c ledger.Commitment) bool {
	return !a.at.Before(c.TS.Add(time.Second))
}

// same is whether two answers say the same thing, whenever they were asked.
func (a ghAnswer) same(b ghAnswer) bool { return a.state == b.state && a.gone == b.gone }

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
		if kind == ledger.TriggerIssueClosed {
			return verdictDrop, "GitHub has no such issue; if the number is a pull request, record a pull request commitment instead"
		}
		return verdictDrop, "the pull request no longer exists"
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
	start := r.Clock.Now()
	type batch struct{ prs, issues []string }
	recorded := map[string][]ledger.Commitment{}
	subjects := map[hostKey]*batch{}
	var order []hostKey
	seen := map[string]bool{}
	for _, fleet := range r.Fleets() {
		list, err := r.Ledger.Commitments(fleet)
		if err != nil {
			continue
		}
		for _, c := range list {
			if !isGitHubKind(c.Trigger.Kind) || c.Waiting {
				continue
			}
			u, err := url.Parse(c.Trigger.Subject)
			if err != nil || u.Host == "" {
				continue
			}
			recorded[pollKey(fleet, c.Trigger.Subject)] = append(recorded[pollKey(fleet, c.Trigger.Subject)], c)
			if seen[pollKey(fleet, c.Trigger.Subject)] {
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
			got, gone, err := client.PullRequestsOrGone(ctx, b.prs)
			states := make(map[string]string, len(got))
			for u, st := range got {
				states[u] = st.State
			}
			r.collect(k.fleet, b.prs, states, gone, err, start, answers, fail)
		}
		if len(b.issues) > 0 {
			got, gone, err := client.IssueStatesOrGone(ctx, b.issues)
			r.collect(k.fleet, b.issues, got, gone, err, start, answers, fail)
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
		old, ok := r.answers[key]
		if !ok || !old.same(a) {
			changed = true
			continue
		}
		// The same answer, but newly asked after a commitment that the older one could not judge.
		for _, c := range recorded[key] {
			if a.judges(c) && !old.judges(c) {
				changed = true
			}
		}
	}
	r.answers, r.failing = answers, failing
	return changed
}

// collect sorts one call's answer. Only a URL GitHub said is gone counts as gone; one it left out without a word, or in a
// repository it could not look in, was not answered and its commitment waits.
func (r *Runner) collect(fleet string, urls []string, states map[string]string, gone []string, err error, at time.Time, answers map[string]ghAnswer, fail func(fleet, subject string, err error)) {
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
	goneSet := map[string]bool{}
	for _, u := range gone {
		goneSet[u] = true
	}
	for _, u := range urls {
		st, ok := states[u]
		switch {
		case ok:
			answers[pollKey(fleet, u)] = ghAnswer{state: st, at: at}
		case goneSet[u]:
			answers[pollKey(fleet, u)] = ghAnswer{gone: true, at: at}
		case unanswered[u]:
			fail(fleet, u, un)
		default:
			fail(fleet, u, errors.New("GitHub's answer did not include it"))
		}
	}
}
