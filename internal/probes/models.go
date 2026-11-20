package probes

import (
	"context"

	"github.com/jbuffin/admiral/internal/config"
)

// ModelLister lists one dock's models and stores them; harnessmodels.Service is one.
type ModelLister interface {
	List(ctx context.Context, fleet config.Fleet, harness string) error
}

// SetModelLister makes Refresh and Run list every dock they touch. Call it before either.
func (r *Runner) SetModelLister(l ModelLister) { r.lister = l }

// listModels lists the dock in the background: an account's models change without a version change,
// and a slow or failing harness must not hold up a version check or the server's startup.
func (r *Runner) listModels(fleet config.Fleet, harness string) {
	if r.lister == nil {
		return
	}
	r.bg.Add(1)
	go func() {
		defer r.bg.Done()
		ctx, cancel := context.WithCancel(r.life)
		defer cancel()
		_ = r.lister.List(ctx, fleet, harness)
	}()
}
