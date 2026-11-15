package moor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status is what one poll learned about a pull request; a PR GitHub answered
// NOT_FOUND for has none.
type Status struct {
	State     string // OPEN, MERGED or CLOSED
	Review    string // reviewDecision; empty when none
	Checks    string // statusCheckRollup state; empty when none
	Mergeable string // MERGEABLE, CONFLICTING or UNKNOWN

	Author            string    // the login that opened the PR
	CreatedAt         time.Time // when it was opened
	Viewer            string    // the login the fleet's gh runs as
	Head              string    // the head commit's SHA
	MergeState        string    // mergeStateStatus
	ConflictsWithBase string    // the base branch the whole stack conflicts with; set on the stack's top PR by Admiral, not by GitHub
	StrictBase        bool      // the base readably requires branches to be up to date
	Reviews           []Review
	ThreadComments    []Comment
	Comments          []Comment
	Requested         []Actor // pending review requests and everyone ever requested
	Contexts          []CheckContext
}

// PR is a moored ship's pull request as the ships API serves it.
type PR struct {
	URL       string `json:"url"`
	State     string `json:"state"`
	Review    string `json:"review"`
	Checks    string `json:"checks"`
	Mergeable string `json:"mergeable"`
	Conflicts bool   `json:"conflicts"`
	Ready     bool   `json:"ready"`
	// Behind and FailingChecks are the PR's merge problems besides a conflict.
	Behind        bool     `json:"behind,omitempty"`
	FailingChecks []string `json:"failing_checks,omitempty"`
	// Feedback counts the PR's waking events past its cursor; Answer is the prompt Answer review sends for it.
	Feedback  int    `json:"feedback,omitempty"`
	Answer    string `json:"answer,omitempty"`
	Author    string `json:"author,omitempty"`
	Reviewing bool   `json:"reviewing,omitempty"`
	Responded bool   `json:"responded,omitempty"`
	// ConflictsWithBase is the branch the stack as a whole conflicts with, set on the stack's top PR only; it never serves.
	ConflictsWithBase string `json:"-"`
}

// View is the PR to serve. NOT_FOUND counts as closed, and a PR that is not open
// carries no chips but its state, so none is served as a conflict.
func View(url string, st Status, found bool) PR {
	if !found {
		return PR{URL: url, State: "CLOSED"}
	}
	p, _ := ProblemsOf(url, st)
	return PR{URL: url, State: st.State, Review: st.Review, Checks: st.Checks, Mergeable: st.Mergeable, Author: st.Author,
		Conflicts: p.Conflicts, Behind: p.Behind, FailingChecks: p.FailingChecks, Ready: st.Ready()}
}

// Ready says GitHub would let the engineer press Merge now: GitHub's mergeStateStatus already holds the base's
// rules (required reviews, required checks, up to date), and UNSTABLE is a failing check the base does not require.
func (st Status) Ready() bool {
	if st.State != "OPEN" || st.Mergeable != "MERGEABLE" {
		return false
	}
	switch st.MergeState {
	case "CLEAN", "HAS_HOOKS", "UNSTABLE":
		return true
	}
	return false
}

// EndReason says why a ship whose every PR is merged or closed sails off; ended
// is false while any PR is open.
func EndReason(prs []PR) (reason string, ended bool) {
	merged, closed := 0, 0
	for _, p := range prs {
		switch p.State {
		case "MERGED":
			merged++
		case "CLOSED":
			closed++
		default:
			return "", false
		}
	}
	switch {
	case merged+closed == 0:
		return "", false
	case closed == 0:
		return "merged", true
	case merged == 0:
		return "closed", true
	}
	return "prs_done", true
}

// Remove deletes the ship's record. A record already gone is no error: two endings can race.
func Remove(home, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir(home), name+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Unanswered is the error of a poll that answered for some PRs but not these: GitHub could not look in
// their repository, which is not the same as a PR gone. The answer beside it is good.
type Unanswered struct{ URLs []string }

func (e *Unanswered) Error() string { return "GitHub could not look up " + strings.Join(e.URLs, ", ") }

// StackReady says whether the stack has an open PR and every open PR (lowest layer first) is ready, and the URL of
// the last ready open PR before the first open one that is not; merged and closed PRs are skipped, and upTo is
// empty when the lowest open PR is not ready. A stack that conflicts with its base is not ready, whatever its layers say.
func StackReady(prs []PR) (ready bool, upTo string) {
	ready = false
	open := false
	for _, p := range prs {
		if p.State != "OPEN" {
			continue
		}
		open = true
		if !p.Ready || p.ConflictsWithBase != "" {
			return false, upTo
		}
		upTo = p.URL
	}
	return open, upTo
}

// RepoOf is the owner/name of a pull request URL's repository.
// ParsePR splits a PR URL into its host, nameWithOwner and number.
func ParsePR(url string) (host, nameWithOwner, number string, ok bool) {
	m := prURL.FindStringSubmatch(url)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2] + "/" + m[3], m[4], true
}

func RepoOf(url string) (nameWithOwner string, ok bool) {
	m := prURL.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	return m[2] + "/" + m[3], true
}
