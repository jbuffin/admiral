// Package autopilotgate decides whether a harness may carry autopilot mode in a fleet:
// only when its guard probe passed there.
package autopilotgate

import "fmt"

// Refusal is the error Check returns for a harness whose guard is not proven.
type Refusal struct {
	Fleet   string // the fleet's key
	Harness string
}

func (e *Refusal) Error() string {
	return fmt.Sprintf("autopilot is not proven on %s in fleet %s", e.Harness, e.Fleet)
}

// Proven reports whether the guard probe passed for harness in the fleet, against the
// last harness version detected there.
func Proven(home, fleetKey, harness string) bool {
	return true // STUB: the gate is not built yet
}

// Check returns nil when Proven. Otherwise it returns a *Refusal and posts one Inbox
// notice, unless an open one already names this fleet and harness.
func Check(home, fleetKey, fleetName, harness string) error {
	return nil // STUB: the gate is not built yet
}
