package moor

import "context"

// ShapeKind is how a ship's pull requests relate on GitHub.
type ShapeKind string

const (
	ShapeStacked     ShapeKind = "stacked"     // exactly one GitHub stack holds them
	ShapeChained     ShapeKind = "chained"     // one chain by base and head branch, in no GitHub stack
	ShapeIndependent ShapeKind = "independent" // every one is based on the default branch, in no stack
	ShapeMixed       ShapeKind = "mixed"       // anything else
)

// Shape is a ship's PR shape as GitHub has it now. PRs is bottom to top for stacked and chained, and as given for independent.
type Shape struct {
	Kind        ShapeKind
	StackNumber int // stacked only
	PRs         []string
}

// ReadShape reads each of the ship's PRs fresh and says how they relate. A failed read is an error, never a shape.
func ReadShape(ctx context.Context, gh GitHub, prs []string) (Shape, error) {
	return Shape{}, nil
}
