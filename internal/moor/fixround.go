package moor

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// PRProblems is what keeps one open PR from merging that its ship can fix.
type PRProblems struct {
	PR                string   `json:"pr"`
	Conflicts         bool     `json:"conflicts,omitempty"`
	Behind            bool     `json:"behind,omitempty"`
	FailingChecks     []string `json:"failing_checks,omitempty"`
	ConflictsWithBase string   `json:"conflicts_with_base,omitempty"`
}

// ProblemsOf reports the merge problems of an open PR from its last answer: by state now, whether or not a wake event was seen.
func ProblemsOf(url string, st Status) (PRProblems, bool) {
	if st.State != "OPEN" {
		return PRProblems{}, false
	}
	p := PRProblems{PR: url, Conflicts: st.Mergeable == "CONFLICTING", Behind: behind(st)}
	for _, c := range st.Contexts {
		if ownCI(c) && failingState(c) {
			p.FailingChecks = append(p.FailingChecks, c.Name)
		}
	}
	return p, p.Conflicts || p.Behind || len(p.FailingChecks) > 0
}

// FixRound is the whole-ship feedback round Fix PR and Fix stack start.
type FixRound struct {
	PR       string // the lowest covered PR with a merge problem
	Problems []PRProblems
	Stack    bool // the round covers more than one PR
	Feedback string
	Next     WakeState
}

// DecideFixRound is one hand round for every answered OPEN PR the ship authored, carrying their merge problems and all waiting feedback.
// It reports false when none of them has a merge problem.
func DecideFixRound(in WakeInput) (FixRound, bool) {
	in.Record.PRs = slices.DeleteFunc(slices.Clone(in.Record.PRs), func(u string) bool { return slices.Contains(in.Record.Reviewing, u) })
	found, next, _, _, _ := scan(in)
	var out FixRound
	var covered []string
	var sections []string
	for _, pr := range in.Record.PRs {
		ev, ok := found[pr]
		if !ok {
			continue
		}
		covered = append(covered, pr)
		p, bad := ProblemsOf(pr, in.Answers[pr])
		if bad {
			out.Problems = append(out.Problems, p)
			if out.PR == "" {
				out.PR = pr
			}
		}

		var events []Event
		if bad {
			events = conditionEvents(pr, p, in.Now)
		}
		var waiting []Event
		for _, e := range ev.waking {
			if !e.Kind.isCondition() {
				waiting = append(waiting, e)
			}
		}
		sort.SliceStable(waiting, func(i, j int) bool { return waiting[i].At.Before(waiting[j].At) })
		events = append(events, waiting...)
		if len(events) > 0 {
			sections = append(sections, FeedbackText(pr, events))
		}

		if next.PRs == nil {
			next.PRs = map[string]PRWake{}
		}
		next.PRs[pr] = openWindow(in, pr, next.PRs[pr], ev, waiting, true)
	}
	if out.PR == "" {
		return FixRound{}, false
	}
	out.Stack = len(covered) > 1
	if out.Stack {
		sections = append([]string{"Feedback on the ship's PRs: " + strings.Join(covered, ", ") + "\n"}, sections...)
	}
	out.Feedback = strings.Join(sections, "\n")
	out.Next = next
	return out, true
}

// conditionEvents are a PR's current merge problems as feedback events.
func conditionEvents(pr string, p PRProblems, at time.Time) []Event {
	var out []Event
	if p.Conflicts {
		out = append(out, Event{Kind: EventConflict, PR: pr, At: at, URL: pr, Body: conflictBody})
	}
	if p.Behind {
		out = append(out, Event{Kind: EventBehindBase, PR: pr, At: at, URL: pr, Body: behindBody})
	}
	for _, n := range p.FailingChecks {
		out = append(out, Event{Kind: EventCheckFailed, PR: pr, At: at, URL: pr, Name: n, Body: "the check is failing"})
	}
	return out
}
