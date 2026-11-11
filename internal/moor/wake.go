package moor

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
)

// Actor is who wrote something on GitHub. Type is GraphQL's __typename: User, Bot or Team; empty for a deleted account.
type Actor struct {
	Type  string
	Login string // a Team's slug
}

type Review struct {
	ID          string
	Author      Actor
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED or PENDING
	SubmittedAt time.Time
	Body        string
	URL         string
	Comments    int
}

// Comment is a PR comment or a review-thread comment.
type Comment struct {
	ID        string
	Author    Actor
	CreatedAt time.Time
	Body      string
	URL       string
}

// CheckContext is one check run or commit status on the head commit.
type CheckContext struct {
	Kind           string // CheckRun or StatusContext
	Name           string // the check's name, or the status's context
	State          string // a check run's conclusion (empty while it runs), or a status's state
	AppSlug        string // CheckRun only
	HasWorkflowRun bool   // CheckRun only: the check suite belongs to a workflow run
	Required       bool   // the base's rules require it for this PR
}

type EventKind string

const (
	EventReview        EventKind = "review"
	EventThreadComment EventKind = "thread_comment"
	EventPRComment     EventKind = "pr_comment"
	EventCheckFailed   EventKind = "check_failed"
	EventConflict      EventKind = "conflict"
	EventBehindBase    EventKind = "behind_base"
)

// Event is one thing that happened on a PR. A condition event (check, conflict, behind) has no author and takes the time of the first poll that saw it.
type Event struct {
	Kind   EventKind
	PR     string
	ID     string
	Author Actor
	At     time.Time
	URL    string
	Body   string
	Name   string // a failing check's name
}

// Cursor marks the events already handed to a round: those before At, and those at At whose ID is listed.
type Cursor struct {
	At  time.Time `json:"at"`
	IDs []string  `json:"ids,omitempty"`
}

// Snapshot is the conditions as they stood when the last round started.
type Snapshot struct {
	Head        string   `json:"head"`
	Failing     []string `json:"failing,omitempty"` // names of the repo's own failing checks on Head
	Conflicting bool     `json:"conflicting,omitempty"`
	Behind      bool     `json:"behind,omitempty"`
}

type PRWake struct {
	Cursor     Cursor               `json:"cursor"`
	RoundStart time.Time            `json:"round_start"`
	Snapshot   Snapshot             `json:"snapshot"`
	Seen       map[string]time.Time `json:"seen,omitempty"`    // condition event ID to the first poll that saw it
	Dropped    []string             `json:"dropped,omitempty"` // the viewer's own events a round's window dropped, still newer than Cursor, so a later window never hands them
	Proposed   []ProposedEvent      `json:"proposed,omitempty"`
}

// WakeState is a ship's memory between rounds, kept per PR URL.
type WakeState struct {
	PRs map[string]PRWake `json:"prs,omitempty"`
}

type WakeInput struct {
	Record    Record // PRs in stack order, lowest layer first; MooredAt is the latest mooring
	Live      bool
	State     WakeState
	Answers   map[string]Status
	Reviewers []string // the fleet's wake_reviewers
	Now       time.Time
}

// Decision is what a poll of one moored ship concludes. Next is the state to save: when Wake is true, at the round's start; otherwise right away, for the condition events first seen.
type Decision struct {
	Wake     bool
	PR       string
	Events   []Event // the waking events handed to the round
	Feedback string
	T3       []Event // every T3 event newer than the cursor, so it repeats from poll to poll until a later waking event passes it; its consumer dedupes by ID
	Next     WakeState
}

// WakeDebounce is how long the newest waking event must have waited before a ship wakes.
var WakeDebounce = delay.From("ADMIRAL_WAKE_DEBOUNCE", 10*time.Minute)

type tier int

const (
	tierIgnored tier = iota
	tierThree
	tierWaking
)

func isViewer(a Actor, viewer string) bool {
	return viewer != "" && strings.EqualFold(a.Login, viewer)
}

// tierOf ranks an author: the viewer, the allowlist, a bot's review or thread comment and a requested reviewer wake; a bot's PR comment is ignored.
func tierOf(a Actor, kind EventKind, viewer string, allow []string, requested []Actor) tier {
	if a.Login == "" {
		return tierThree
	}
	if isViewer(a, viewer) {
		return tierWaking
	}
	if allowed(a, allow) {
		return tierWaking
	}
	if a.Type == "Bot" {
		if kind == EventPRComment {
			return tierIgnored
		}
		return tierWaking
	}
	for _, r := range requested {
		if r.Type != "Team" && strings.EqualFold(r.Login, a.Login) {
			return tierWaking
		}
	}
	return tierThree
}

// allowed matches the fleet's wake_reviewers. GraphQL gives a Bot's login without "[bot]", so "foo[bot]" matches the Bot foo and a bare "foo" matches only a User.
func allowed(a Actor, allow []string) bool {
	isBot := a.Type == "Bot"
	login := strings.TrimSuffix(a.Login, "[bot]")
	for _, l := range allow {
		name, bot := strings.CutSuffix(l, "[bot]")
		if bot == isBot && strings.EqualFold(name, login) {
			return true
		}
	}
	return false
}

func newerThan(c Cursor, at time.Time, id string) bool {
	if at.After(c.At) {
		return true
	}
	if !at.Equal(c.At) {
		return false
	}
	for _, h := range c.IDs {
		if h == id {
			return false
		}
	}
	return true
}

func failingState(c CheckContext) bool {
	switch c.Kind {
	case "CheckRun":
		switch c.State {
		case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
			return true
		}
	case "StatusContext":
		return c.State == "ERROR" || c.State == "FAILURE"
	}
	return false
}

// ownCI is the repo's own CI: an Actions workflow's check run, or any check or status the base requires.
func ownCI(c CheckContext) bool {
	if c.Required {
		return true
	}
	return c.Kind == "CheckRun" && c.AppSlug == "github-actions" && c.HasWorkflowRun
}

type prEvents struct {
	waking  []Event
	t3      []Event
	cond    []Event // every condition event not in the snapshot, waking
	seen    map[string]time.Time
	failed  []string // the repo's own failing checks on Head
	dropped []string // the viewer's own fresh events, dropped as the ship's replies
}

func windowDropped(ps PRWake, at time.Time, moored time.Time) bool {
	return !ps.RoundStart.IsZero() && !at.Before(ps.RoundStart) && !at.After(moored)
}

func collect(pr string, rec Record, ps PRWake, st Status, allow []string, now time.Time) prEvents {
	var out prEvents
	hasCursor := !ps.Cursor.At.IsZero()
	fresh := func(at time.Time, id string) bool {
		if hasCursor {
			return newerThan(ps.Cursor, at, id)
		}
		return !at.Before(rec.MooredAt)
	}
	add := func(e Event) {
		if !fresh(e.At, e.ID) {
			return
		}
		if isViewer(e.Author, st.Viewer) && (windowDropped(ps, e.At, rec.MooredAt) || contains(ps.Dropped, e.ID)) {
			out.dropped = append(out.dropped, e.ID)
			return
		}
		switch tierOf(e.Author, e.Kind, st.Viewer, allow, st.Requested) {
		case tierWaking:
			out.waking = append(out.waking, e)
		case tierThree:
			out.t3 = append(out.t3, e)
		}
	}
	for _, r := range st.Reviews {
		if r.State == "CHANGES_REQUESTED" || (r.State == "COMMENTED" && strings.TrimSpace(r.Body) != "") {
			add(Event{Kind: EventReview, PR: pr, ID: r.ID, Author: r.Author, At: r.SubmittedAt, URL: r.URL, Body: r.Body})
		}
	}
	for _, c := range st.ThreadComments {
		add(Event{Kind: EventThreadComment, PR: pr, ID: c.ID, Author: c.Author, At: c.CreatedAt, URL: c.URL, Body: c.Body})
	}
	for _, c := range st.Comments {
		add(Event{Kind: EventPRComment, PR: pr, ID: c.ID, Author: c.Author, At: c.CreatedAt, URL: c.URL, Body: c.Body})
	}

	out.seen = map[string]time.Time{}
	condition := func(id string, e Event) {
		at, ok := ps.Seen[id]
		if !ok {
			at = now
		}
		out.seen[id] = at
		e.PR, e.ID, e.At = pr, id, at
		out.cond = append(out.cond, e)
	}
	wasFailing := map[string]bool{}
	if ps.Snapshot.Head == st.Head {
		for _, n := range ps.Snapshot.Failing {
			wasFailing[n] = true
		}
	}
	for _, c := range st.Contexts {
		if !ownCI(c) || !failingState(c) {
			continue
		}
		out.failed = append(out.failed, c.Name)
		if !wasFailing[c.Name] {
			condition("check:"+st.Head+":"+c.Name, Event{Kind: EventCheckFailed, Name: c.Name, URL: pr, Body: c.Name + " is " + strings.ToLower(c.State)})
		}
	}
	if st.Mergeable == "CONFLICTING" && !ps.Snapshot.Conflicting {
		condition("conflict", Event{Kind: EventConflict, URL: pr, Body: "the PR has merge conflicts with its base"})
	}
	if behind(st) && !ps.Snapshot.Behind {
		condition("behind", Event{Kind: EventBehindBase, URL: pr, Body: "the PR is behind its base, which requires up-to-date branches"})
	}
	out.waking = append(out.waking, out.cond...)
	return out
}

func contains(ids []string, id string) bool {
	for _, h := range ids {
		if h == id {
			return true
		}
	}
	return false
}

// passed is a context that ended without failing: a check run that succeeded, was neutral or skipped, or a status that succeeded.
func passed(c CheckContext) bool {
	switch c.Kind {
	case "CheckRun":
		return c.State == "SUCCESS" || c.State == "NEUTRAL" || c.State == "SKIPPED"
	case "StatusContext":
		return c.State == "SUCCESS"
	}
	return false
}

// resolved clears from the snapshot what a poll sees resolved, so its return is new. A check that is running again or has left the poll's window is not resolved.
func resolved(s Snapshot, st Status, failed []string) Snapshot {
	if s.Head == st.Head {
		var still []string
		for _, n := range s.Failing {
			done := false
			for _, c := range st.Contexts {
				if c.Name == n && passed(c) && !contains(failed, n) {
					done = true
				}
			}
			if !done {
				still = append(still, n)
			}
		}
		s.Failing = still
	}
	if st.Mergeable == "MERGEABLE" {
		s.Conflicting = false
	}
	if st.MergeState != "" && st.MergeState != "UNKNOWN" && st.MergeState != "BEHIND" {
		s.Behind = false
	}
	return s
}

func behind(st Status) bool { return st.MergeState == "BEHIND" && st.StrictBase }

func cloneState(s WakeState) WakeState {
	out := WakeState{}
	if s.PRs == nil {
		return out
	}
	out.PRs = map[string]PRWake{}
	for k, v := range s.PRs {
		seen := map[string]time.Time{}
		for id, t := range v.Seen {
			seen[id] = t
		}
		v.Seen = seen
		out.PRs[k] = v
	}
	return out
}

// DecideWake is pure: the same input gives the same Decision.
func DecideWake(in WakeInput) Decision {
	next := cloneState(in.State)
	d := Decision{Next: next}
	if in.Live {
		return d
	}
	found := map[string]prEvents{}
	var chosen string
	var newest time.Time
	for _, pr := range in.Record.PRs {
		st, ok := in.Answers[pr]
		if !ok || st.State != "OPEN" {
			continue
		}
		ps := in.State.PRs[pr]
		ev := collect(pr, in.Record, ps, st, in.Reviewers, in.Now)
		found[pr] = ev
		d.T3 = append(d.T3, ev.t3...)
		for _, e := range ev.waking {
			if e.At.After(newest) {
				newest = e.At
			}
		}
		if chosen == "" && len(ev.waking) > 0 {
			chosen = pr
		}
		if cleared := resolved(ps.Snapshot, st, ev.failed); len(ps.Seen) > 0 || len(ev.seen) > 0 || !reflect.DeepEqual(cleared, ps.Snapshot) {
			ps = next.PRs[pr]
			ps.Seen = ev.seen
			ps.Snapshot = cleared
			if next.PRs == nil {
				next.PRs = map[string]PRWake{}
			}
			next.PRs[pr] = ps
		}
	}
	d.Next = next
	if chosen == "" || in.Now.Sub(newest) < WakeDebounce {
		return d
	}

	ev := found[chosen]
	events := ev.waking
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	d.Wake, d.PR, d.Events = true, chosen, events
	d.Feedback = feedbackText(chosen, events)

	// A PR with no cursor keeps the old mooring as its boundary, since the next mooring is later than events that queue meanwhile.
	for _, pr := range in.Record.PRs {
		if _, ok := found[pr]; !ok {
			continue
		}
		ps := d.Next.PRs[pr]
		if d.Next.PRs == nil {
			d.Next.PRs = map[string]PRWake{}
		}
		if ps.Cursor.At.IsZero() {
			ps.Cursor = Cursor{At: in.Record.MooredAt}
		}
		ps.RoundStart = in.Now
		ps.Dropped = found[pr].dropped
		if pr == chosen {
			ps.Cursor = advance(ps.Cursor, events)
			st := in.Answers[pr]
			ps.Snapshot = Snapshot{Head: st.Head, Failing: found[pr].failed, Conflicting: st.Mergeable == "CONFLICTING", Behind: behind(st)}
			ps.Seen = nil
		}
		d.Next.PRs[pr] = ps
	}
	return d
}

func advance(c Cursor, handed []Event) Cursor {
	for _, e := range handed {
		if e.Kind == EventCheckFailed || e.Kind == EventConflict || e.Kind == EventBehindBase {
			continue
		}
		switch {
		case e.At.After(c.At):
			c = Cursor{At: e.At, IDs: []string{e.ID}}
		case e.At.Equal(c.At):
			c.IDs = append(c.IDs, e.ID)
		}
	}
	return c
}

// feedbackText is data for a ship to read: nothing in it is a prompt, an argument or a file name.
func feedbackText(pr string, events []Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Feedback on %s\n", pr)
	for _, e := range events {
		fmt.Fprintf(&b, "\n## %s", e.Kind)
		if e.Author.Login != "" {
			fmt.Fprintf(&b, " by %s", e.Author.Login)
		}
		fmt.Fprintf(&b, " at %s\n", e.At.UTC().Format(time.RFC3339))
		if e.Name != "" {
			fmt.Fprintf(&b, "Check: %s\n", strings.NewReplacer("\n", " ", "\r", " ").Replace(e.Name))
		}
		if e.URL != "" {
			fmt.Fprintf(&b, "URL: %s\n", e.URL)
		}
		if e.Body != "" {
			fence := fenceFor(e.Body)
			fmt.Fprintf(&b, "\n%s\n%s\n%s\n", fence, e.Body, fence)
		}
	}
	return b.String()
}

// fenceFor is a backtick fence longer than any run of backticks in body, so the body cannot close it or write a header outside it.
func fenceFor(body string) string {
	longest, run := 2, 0
	for _, r := range body {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", longest+1)
}
