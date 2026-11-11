// Package probes runs the capability probes that fill the capability store.
//
// UNFINISHED STUB: only the names the tests compile against exist. Every behavior is missing.
package probes

import (
	"context"
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
)

// The guard probe's contract with `admiral guard pretooluse`: the guard denies exactly
// Command(dir, nonce) while NonceEnv names nonce, and records the denial at DeniedPath.
const (
	NonceEnv = "ADMIRAL_PROBE_NONCE"
	DirEnv   = "ADMIRAL_PROBE_DIR"
)

func SentinelPath(dir, nonce string) string { return filepath.Join(dir, "ran-"+nonce) }
func DeniedPath(dir, nonce string) string   { return filepath.Join(dir, "denied-"+nonce) }

// Command is the harmless command the model is asked to run.
func Command(dir, nonce string) string { return "touch " + SentinelPath(dir, nonce) }

// Outcome is one probe's result.
type Outcome struct {
	Passed bool
	Detail string
}

// Target is what a probe runs against.
type Target struct {
	Home    string
	Fleet   config.Fleet
	Harness string
	Version string
}

// Probe proves one capability of one harness.
type Probe struct {
	Harness    string
	Capability capabilities.ID
	Run        func(ctx context.Context, t Target) Outcome
}

// Line is one capability's status after a run.
type Line struct {
	ID     capabilities.ID
	Label  string
	Status capabilities.Status
	Detail string
}

// Report is what Run returns: the detected version and one line per table capability.
type Report struct {
	Version string
	Lines   []Line
}

// Runner holds the registered probes.
type Runner struct {
	// Timeout bounds one Run as a whole, version detection included.
	Timeout time.Duration
}

// New returns a runner with exactly the given probes registered.
func New(home string, ps ...Probe) *Runner { return &Runner{} }

// NewDefault returns a runner with every built-in probe.
func NewDefault(home string) *Runner { return &Runner{} }

// ClaudeGuard is the probe for autopilot.guard_hook on Claude Code.
func ClaudeGuard() Probe { return Probe{} }

// Run detects the harness version, records it, runs the harness's probes and stores
// their results under that version. Concurrent runs for one fleet and harness share one.
func (r *Runner) Run(ctx context.Context, fleet config.Fleet, harness string) (Report, error) {
	return Report{}, nil
}

// Refresh detects every fleet harness's version, records it (clearing it when detection
// fails) and re-runs the probes of a harness whose version differs from the one its last
// results were stored under, in the background.
func (r *Runner) Refresh(ctx context.Context, fleets []config.Fleet) {}

// Wait blocks until the background runs Refresh started have finished.
func (r *Runner) Wait() {}
