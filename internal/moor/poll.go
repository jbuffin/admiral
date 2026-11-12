package moor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Status is what one poll learned about a pull request; a PR GitHub answered
// NOT_FOUND for has none.
type Status struct {
	State     string // OPEN, MERGED or CLOSED
	Review    string // reviewDecision; empty when none
	Checks    string // statusCheckRollup state; empty when none
	Mergeable string // MERGEABLE, CONFLICTING or UNKNOWN

	Viewer         string // the login the fleet's gh runs as
	Head           string // the head commit's SHA
	MergeState     string // mergeStateStatus
	StrictBase     bool   // the base readably requires branches to be up to date
	Reviews        []Review
	ThreadComments []Comment
	Comments       []Comment
	Requested      []Actor // pending review requests and everyone ever requested
	Contexts       []CheckContext
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
}

// View is the PR to serve. NOT_FOUND counts as closed, and a PR that is not open
// carries no chips but its state, so none is served as a conflict.
func View(url string, st Status, found bool) PR {
	if !found {
		return PR{URL: url, State: "CLOSED"}
	}
	return PR{URL: url, State: st.State, Review: st.Review, Checks: st.Checks, Mergeable: st.Mergeable,
		Conflicts: st.State == "OPEN" && st.Mergeable == "CONFLICTING"}
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

// StackReady says whether every PR of the stack (lowest layer first) is ready, and the URL of the highest layer
// up to which every layer is ready; upTo is empty when the lowest layer is not ready.
func StackReady(prs []PR) (ready bool, upTo string) {
	return false, "" // not implemented
}
