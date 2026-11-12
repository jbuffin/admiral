package push

import "github.com/jbuffin/admiral/internal/routines"

// RoutineNotifier turns a routine's trigger records into RoutineNotify events.
type RoutineNotifier struct {
	W         *Watcher
	FleetName func(fleet string) string
}

var _ routines.Notifier = (*RoutineNotifier)(nil)

func (n *RoutineNotifier) Notify(fleet string, r routines.Routine, t routines.Trigger) {}
