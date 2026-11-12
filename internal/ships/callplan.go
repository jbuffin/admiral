package ships

import (
	"context"
	"time"

	"github.com/jbuffin/admiral/internal/keymap"
)

// paneIO is the pane a plan drives: what it shows and a way to type into it.
type paneIO struct {
	capture func(ctx context.Context) (string, error)
	send    func(ctx context.Context, keys string) error
}

// stepWait is how long a menu has to show what a step should have done before the answer stops.
var stepWait = 3 * time.Second

const stepPoll = 50 * time.Millisecond

// waitFor polls the pane until ok holds or the wait runs out, and says whether it held.
func (p paneIO) waitFor(ctx context.Context, ok func(string) bool, wait time.Duration) (bool, error) {
	deadline := time.Now().Add(wait)
	for {
		screen, err := p.capture(ctx)
		if err != nil {
			return false, err
		}
		if ok(screen) {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(stepPoll):
		}
	}
}

// runPlan answers a call on the pane one step at a time. why is set, with nothing sent after the point it names, when the
// menu is not where the plan starts or stops following it; err is a tmux failure.
func runPlan(ctx context.Context, plan keymap.Plan, p paneIO) (why string, err error) {
	if ok, err := p.waitFor(ctx, plan.Fresh, time.Second); err != nil || !ok {
		return "the menu is not on its first question with nothing chosen: answer in the terminal", err
	}
	for _, st := range plan.Steps {
		if err := p.send(ctx, st.Keys); err != nil {
			return "", err
		}
		if st.Done == nil {
			continue
		}
		if ok, err := p.waitFor(ctx, st.Done, stepWait); err != nil || !ok {
			return "the menu did not show " + st.Want + ": finish the answer in the terminal", err
		}
	}
	if plan.Review == nil {
		return "", nil
	}
	if ok, err := p.waitFor(ctx, plan.Review, stepWait); err != nil || !ok {
		return "the review screen does not list your answers: check it in the terminal", err
	}
	if err := p.send(ctx, plan.Submit.Keys); err != nil {
		return "", err
	}
	if ok, err := p.waitFor(ctx, plan.Submit.Done, stepWait); err != nil || !ok {
		return "the menu did not take the submit: check it in the terminal", err
	}
	return "", nil
}
