// Package promote reads the work a restricted ship left in its clone, through neutral git only.
package promote

import (
	"context"
	"errors"
)

var (
	ErrNothing      = errors.New("nothing to promote")
	ErrAdmiralPaths = errors.New("commits touch .admiral/")
	ErrAlternates   = errors.New("the clone's alternates were changed")
)

// Kind is how the restricted ship left its work.
type Kind string

const (
	Commits Kind = "commits"
	Diff    Kind = "diff"
)

// Work is what Promote starts from: the commits base..tip, or an uncommitted diff against Dir's HEAD.
type Work struct {
	Kind Kind
	// Base is the merge-base of the tip and Dir's HEAD; Tip is empty for a Diff.
	Base, Tip string
	// Range is "<base>..<tip>" or "uncommitted changes".
	Range string
	// Patch is the binary diff of a Diff, with .admiral/ left out.
	Patch string
}

// Read reads the work in clone's branch against dir, refusing as the sentinels say.
func Read(ctx context.Context, dir, clone, branch string) (Work, error) {
	return Work{}, errors.New("promote: not implemented")
}
