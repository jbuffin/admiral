// Package stackmerge checks in an Admiral-owned bare repository whether a stack's top merges into its base.
package stackmerge

import (
	"context"
	"errors"

	"github.com/jbuffin/admiral/internal/moor"
)

type Git struct{}

// New keeps its bare repositories under home; remote is where a check fetches from.
func New(home string, remote func(moor.StackCheck) string) *Git { return &Git{} }

func (g *Git) Conflicts(ctx context.Context, c moor.StackCheck) (bool, error) {
	return false, errors.New("not implemented")
}
