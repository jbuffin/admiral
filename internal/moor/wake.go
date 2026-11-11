package moor

import "time"

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
	At  time.Time
	IDs []string
}

// Snapshot is the conditions as they stood when the last round started.
type Snapshot struct {
	Head        string
	Failing     []string // names of the repo's own failing checks on Head
	Conflicting bool
	Behind      bool
}

type PRWake struct {
	Cursor     Cursor
	RoundStart time.Time
	Snapshot   Snapshot
	Seen       map[string]time.Time // condition event ID to the first poll that saw it
}

// WakeState is a ship's memory between rounds, kept per PR URL.
type WakeState struct {
	PRs map[string]PRWake
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
	T3       []Event
	Next     WakeState
}

// DecideWake is pure: the same input gives the same Decision.
func DecideWake(in WakeInput) Decision { return Decision{} }
