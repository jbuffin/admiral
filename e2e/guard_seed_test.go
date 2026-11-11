package e2e

import (
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
)

// fakeVersion is what the fake harnesses print for --version when a test sets nothing else.
const fakeVersion = "2.1.300 (fake)"

// defaultFleets are the fleets of world.config that have a claude dock.
var defaultFleets = []string{"work", "home", "claudeonly", "nosearch"}

// seedGuard stores a guard probe result for the harness's detected version in the
// world's home, the way the probe runner leaves it. Call it before w.start().
func (w *world) seedGuard(fleet, harness string, passed bool) {
	w.t.Helper()
	store, now := capabilities.Open(w.home), time.Now()
	if err := store.RecordVersion(fleet, harness, fakeVersion, now); err != nil {
		w.t.Fatal(err)
	}
	res := capabilities.Result{Passed: passed, At: now, Detail: "seeded by the test"}
	if err := store.Record(fleet, harness, fakeVersion, capabilities.AutopilotGuard, res); err != nil {
		w.t.Fatal(err)
	}
}

// proveGuard seeds a passed guard probe for claude in each named fleet, or in every
// default fleet when none is named: the state an autopilot launch needs.
func (w *world) proveGuard(fleets ...string) {
	w.t.Helper()
	if len(fleets) == 0 {
		fleets = defaultFleets
	}
	for _, f := range fleets {
		w.seedGuard(f, "claude", true)
	}
}
