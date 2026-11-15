package moor

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// PRProblems is what keeps one open PR from merging that its ship can fix.
type PRProblems struct {
	PR            string   `json:"pr"`
	Conflicts     bool     `json:"conflicts,omitempty"`
	Behind        bool     `json:"behind,omitempty"`
	FailingChecks []string `json:"failing_checks,omitempty"`
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
			if e.Kind != EventCheckFailed && e.Kind != EventConflict && e.Kind != EventBehindBase {
				waiting = append(waiting, e)
			}
		}
		sort.SliceStable(waiting, func(i, j int) bool { return waiting[i].At.Before(waiting[j].At) })
		events = append(events, waiting...)
		if len(events) > 0 {
			sections = append(sections, FeedbackText(pr, events))
		}

		ps := next.PRs[pr]
		if next.PRs == nil {
			next.PRs = map[string]PRWake{}
		}
		if ps.Cursor.At.IsZero() {
			ps.Cursor = Cursor{At: in.Record.MooredAt}
		}
		ps.RoundStart = in.Now
		ps.Dropped = ev.dropped
		ps.Cursor = advance(ps.Cursor, waiting)
		st := in.Answers[pr]
		ps.Snapshot = Snapshot{Head: st.Head, Failing: ev.failed, Conflicting: st.Mergeable == "CONFLICTING", Behind: behind(st)}
		ps.Seen = nil
		next.PRs[pr] = ps
	}
	if out.PR == "" {
		return FixRound{}, false
	}
	if len(covered) > 1 {
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
		out = append(out, Event{Kind: EventConflict, PR: pr, At: at, URL: pr, Body: "the PR has merge conflicts with its base"})
	}
	if p.Behind {
		out = append(out, Event{Kind: EventBehindBase, PR: pr, At: at, URL: pr, Body: "the PR is behind its base, which requires up-to-date branches"})
	}
	for _, n := range p.FailingChecks {
		out = append(out, Event{Kind: EventCheckFailed, PR: pr, At: at, URL: pr, Name: n, Body: "the check is failing"})
	}
	return out
}
