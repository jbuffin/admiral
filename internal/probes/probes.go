// Package probes runs the capability probes that fill the capability store.
package probes

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
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

const (
	defaultTimeout = 2 * time.Minute
	versionTimeout = 15 * time.Second
)

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

	home   string
	probes []Probe
	store  *capabilities.Store
	bg     sync.WaitGroup

	mu      sync.Mutex
	running map[string]*flight
}

// flight is a run in progress that later callers for the same harness join.
type flight struct {
	done chan struct{}
	rep  Report
	err  error
}

// New returns a runner with exactly the given probes registered.
func New(home string, ps ...Probe) *Runner {
	return &Runner{Timeout: defaultTimeout, home: home, probes: ps, store: capabilities.Open(home),
		running: map[string]*flight{}}
}

// NewDefault returns a runner with every built-in probe.
func NewDefault(home string) *Runner { return New(home, ClaudeGuard()) }

// Run detects the harness version, records it, runs the harness's probes and stores
// their results under that version. Concurrent runs for one fleet and harness share one.
func (r *Runner) Run(ctx context.Context, fleet config.Fleet, harness string) (Report, error) {
	key := fleet.Key + "\x00" + harness
	r.mu.Lock()
	if f, ok := r.running[key]; ok {
		r.mu.Unlock()
		select {
		case <-f.done:
			return f.rep, f.err
		case <-ctx.Done():
			return Report{}, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	r.running[key] = f
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	f.rep, f.err = r.run(ctx, fleet, harness)
	r.mu.Lock()
	delete(r.running, key)
	r.mu.Unlock()
	close(f.done)
	return f.rep, f.err
}

func (r *Runner) run(ctx context.Context, fleet config.Fleet, harness string) (Report, error) {
	version, err := capabilities.DetectVersion(ctx, fleet, harness, versionTimeout)
	if err != nil {
		// Fail closed: the old version's passes stop counting.
		_ = r.store.RecordVersion(fleet.Key, harness, "", time.Now())
		return Report{}, err
	}
	if err := r.store.RecordVersion(fleet.Key, harness, version, time.Now()); err != nil {
		return Report{}, err
	}
	target := Target{Home: r.home, Fleet: fleet, Harness: harness, Version: version}
	skipped := map[capabilities.ID]string{}
	for _, p := range r.probes {
		if p.Harness != harness {
			continue
		}
		if !capabilities.ClaimedBy(harness, p.Capability, version) {
			cl, _ := capabilities.Claims(harness, p.Capability)
			skipped[p.Capability] = fmt.Sprintf("version %s is older than %s, the first that claims it", version, cl.From)
			continue
		}
		out := p.Run(ctx, target)
		res := capabilities.Result{Passed: out.Passed, At: time.Now(), Detail: out.Detail}
		if err := r.store.Record(fleet.Key, harness, version, p.Capability, res); err != nil {
			return Report{}, err
		}
	}

	snap, err := r.store.Snapshot(fleet.Key, harness)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Version: version}
	for _, feat := range capabilities.Table() {
		for _, c := range feat.Capabilities {
			st := snap.States[c.ID]
			l := Line{ID: c.ID, Label: c.Label, Status: st.Status, Detail: skipped[c.ID]}
			if st.Result != nil {
				l.Detail = st.Result.Detail
			}
			rep.Lines = append(rep.Lines, l)
		}
	}
	return rep, nil
}

// Refresh detects every fleet harness's version, records it (clearing it when detection
// fails) and re-runs the probes of a harness whose version differs from the one its last
// results were stored under, in the background.
func (r *Runner) Refresh(ctx context.Context, fleets []config.Fleet) {
	for _, fleet := range fleets {
		for _, h := range fleet.Harnesses {
			r.bg.Add(1)
			go func() {
				defer r.bg.Done()
				r.refresh(ctx, fleet, h.Name)
			}()
		}
	}
}

func (r *Runner) refresh(ctx context.Context, fleet config.Fleet, harness string) {
	version, err := capabilities.DetectVersion(ctx, fleet, harness, versionTimeout)
	if err != nil {
		_ = r.store.RecordVersion(fleet.Key, harness, "", time.Now())
		return
	}
	if err := r.store.RecordVersion(fleet.Key, harness, version, time.Now()); err != nil {
		return
	}
	// A harness never probed is left for setup or Check again.
	if probed, _, ok, err := r.store.LastProbed(fleet.Key, harness); err == nil && ok && probed != version {
		_, _ = r.Run(ctx, fleet, harness)
	}
}

// Wait blocks until the background runs Refresh started have finished.
func (r *Runner) Wait() { r.bg.Wait() }
