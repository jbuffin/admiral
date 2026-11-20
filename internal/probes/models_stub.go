package probes

import (
	"context"

	"github.com/jbuffin/admiral/internal/config"
)

// Red-phase stub: the model-list work wires the lister into Refresh and Run.

// ModelLister lists one dock's models and stores them; harnessmodels.Service is one.
type ModelLister interface {
	List(ctx context.Context, fleet config.Fleet, harness string) error
}

// SetModelLister makes Refresh and Run list every dock they touch.
func (r *Runner) SetModelLister(ModelLister) {}
