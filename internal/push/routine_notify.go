package push

import (
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/routines"
)

// RoutineNotifier turns a routine's trigger records into RoutineNotify events.
type RoutineNotifier struct {
	W         *Watcher
	FleetName func(fleet string) string
}

var _ routines.Notifier = (*RoutineNotifier)(nil)

func (n *RoutineNotifier) Notify(fleet string, r routines.Routine, t routines.Trigger) {
	var text string
	ev := Event{Kind: RoutineNotify, Fleet: fleet, Key: "routine:" + r.ID + ":" + t.ID, Name: r.Name}
	switch t.Outcome {
	case routines.Launched:
		text = "launched"
		if t.Ship != "" {
			ev.Target, ev.Session = TargetShip, t.Ship
		}
	case routines.Proposed:
		text = "wants your go-ahead"
		ev.Target, ev.Session, ev.Item = TargetPeek, inbox.AdmiralSession, "routine-"+t.ID
	case routines.Failed:
		text = "failed to run"
		if t.Reason != "" {
			text += ": " + t.Reason
		}
	case routines.Breaker:
		text = "now asks first after repeated failures"
	default:
		return
	}
	ev.Text = text
	if n.FleetName != nil {
		ev.FleetName = n.FleetName(fleet)
	}
	n.W.Emit(ev)
}
