package moor

import "context"

// StackCheck asks whether a stack's top PR merges into its base as a whole.
type StackCheck struct {
	Fleet string
	Host  string
	Repo  string // owner/name
	Base  string // the stack's base branch
	PR    int    // the top PR's number
	Head  string // the top PR's head SHA
}

// StackChecker answers StackCheck; an error means it could not tell.
type StackChecker interface {
	Conflicts(ctx context.Context, c StackCheck) (bool, error)
}
