// Package probes runs the capability probes that fill the capability store.
package probes

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
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

// SeenPath is written by the guard for every Bash call it is asked about while the probe's
// env is live, so the probe can tell a hook that never ran from a model that ran something else.
func SeenPath(dir, nonce string) string { return filepath.Join(dir, "seen-"+nonce) }

// The scout guard probe's files, beside the autopilot probe's: the guard writes ScoutSeenPath for every Write
// call it is asked about while the probe's env is live, and ScoutDeniedPath when it refuses the probe's file,
// which is ScoutFilePath inside the checkout the run's ADMIRAL_SCOUT names.
func ScoutSeenPath(dir, nonce string) string   { return filepath.Join(dir, "scout-seen-"+nonce) }
func ScoutDeniedPath(dir, nonce string) string { return filepath.Join(dir, "scout-denied-"+nonce) }
func ScoutFilePath(checkout, nonce string) string {
	return filepath.Join(checkout, "scout-"+nonce)
}

var nonceFormat = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ValidEnv is what the guard requires of the probe's two variables before it acts on them,
// so a stray value can never name a file outside the probe's directory.
func ValidEnv(dir, nonce string) bool { return nonceFormat.MatchString(nonce) && filepath.IsAbs(dir) }

// Command is the harmless command the model is asked to run.
func Command(dir, nonce string) string { return "touch " + SentinelPath(dir, nonce) }

const (
	defaultTimeout = 32 * time.Minute // worst case, in order: the guard and fence (2 minutes each), the sandbox probe's three runs each tried three times (9 x 2), the network run (2) and four judge calls of up to 90s each (6), with a little slack
	versionTimeout = 15 * time.Second
)

// Outcome is one probe's result.
type Outcome struct {
	Passed bool
	// Inconclusive is a probe that could not run every command and showed no failure on the ones it ran: it is not recorded, so a stored pass stays and a version never proven stays not checked.
	Inconclusive bool
	Detail       string
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
	// Inconclusive is a probe of this run that could not conclude and stored nothing, so Status and the stored result are an earlier run's.
	Inconclusive bool
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
	life   context.Context
	stop   context.CancelFunc

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
	life, stop := context.WithCancel(context.Background())
	return &Runner{Timeout: defaultTimeout, home: home, probes: ps, store: capabilities.Open(home),
		life: life, stop: stop, running: map[string]*flight{}}
}

// Close stops every run in progress; a stopped run stores nothing and leaves the version alone.
func (r *Runner) Close() { r.stop() }

// NewDefault returns a runner with every built-in probe.
func NewDefault(home string) *Runner { return New(home, defaultProbes()...) }

func defaultProbes() []Probe {
	ps := append([]Probe{ClaudeGuard(), ClaudeScoutGuard(), ClaudeMCPFence(), CodexMCPFence(), CodexConfigHome(), CopilotMCPFence(), CopilotConfigHome()}, judgeProbes()...)
	// Last, so a slow restricted run cannot starve the judge calls of the run's time.
	return append(ps, ClaudeRestrictedSandbox(), ClaudeRestrictedNetwork())
}

// GuardHarnesses lists the harnesses with a registered guard probe: a stored pass for
// any other harness proves nothing.
func GuardHarnesses() []string {
	var out []string
	for _, p := range defaultProbes() {
		if p.Capability == capabilities.AutopilotGuard {
			out = append(out, p.Harness)
		}
	}
	return out
}

// Run detects the harness version, records it, runs the harness's probes and stores
// their results under that version. Concurrent runs for one fleet and harness share one.
// The run does not end when ctx does: only Close, or the run's own time limit, ends it.
func (r *Runner) Run(ctx context.Context, fleet config.Fleet, harness string) (Report, error) {
	return r.runShared(ctx, fleet, harness, "")
}

func (r *Runner) runShared(ctx context.Context, fleet config.Fleet, harness, known string) (Report, error) {
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

	// Deferred, so a probe that panics still frees the callers waiting on it.
	defer func() {
		r.mu.Lock()
		delete(r.running, key)
		r.mu.Unlock()
		close(f.done)
	}()
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.Timeout)
	defer cancel()
	defer context.AfterFunc(r.life, cancel)()
	f.rep, f.err = r.run(runCtx, fleet, harness, known)
	return f.rep, f.err
}

// cancelled is a run ended from outside, which says nothing about the harness.
func cancelled(ctx context.Context) bool { return errors.Is(ctx.Err(), context.Canceled) }

func (r *Runner) run(ctx context.Context, fleet config.Fleet, harness, version string) (Report, error) {
	if version == "" {
		var err error
		if version, err = capabilities.DetectVersion(ctx, fleet, harness, versionTimeout); err != nil {
			if cancelled(ctx) {
				return Report{}, ctx.Err()
			}
			// Fail closed: the old version's passes stop counting.
			_ = r.store.RecordVersionFailure(fleet.Key, harness, err.Error(), time.Now())
			return Report{}, err
		}
	}
	if err := r.store.RecordVersion(fleet.Key, harness, version, time.Now()); err != nil {
		return Report{}, err
	}
	target := Target{Home: r.home, Fleet: fleet, Harness: harness, Version: version}
	skipped := map[capabilities.ID]string{}
	unrecorded := map[capabilities.ID]string{}
	// The probes of a harness use their own directories and run side by side, so one
	// more probe does not add its model call to the wait.
	var due []Probe
	for _, p := range r.probes {
		if p.Harness != harness {
			continue
		}
		if !capabilities.ClaimedBy(harness, p.Capability, version) {
			cl, _ := capabilities.Claims(harness, p.Capability)
			skipped[p.Capability] = fmt.Sprintf("version %s is older than %s, the first that claims it", version, cl.From)
			continue
		}
		due = append(due, p)
	}
	outs := make([]Outcome, len(due))
	panics := make([]any, len(due))
	var wg sync.WaitGroup
	for i, p := range due {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { panics[i] = recover() }()
			outs[i] = p.Run(ctx, target)
		}()
	}
	wg.Wait()
	// Run's caller recovers a panicking probe, so it must surface on this goroutine.
	for _, v := range panics {
		if v != nil {
			panic(v)
		}
	}
	if cancelled(ctx) {
		return Report{}, ctx.Err()
	}
	for i, p := range due {
		out := outs[i]
		if out.Inconclusive {
			unrecorded[p.Capability] = out.Detail
			continue
		}
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
			if d, ok := unrecorded[c.ID]; ok {
				l.Inconclusive = true
				if st.Status == capabilities.Failed && l.Detail != "" {
					d += "; the stored failure: " + l.Detail
				}
				l.Detail = d
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(r.life, cancel)()
	version, err := capabilities.DetectVersion(ctx, fleet, harness, versionTimeout)
	if err != nil {
		if !cancelled(ctx) {
			_ = r.store.RecordVersionFailure(fleet.Key, harness, err.Error(), time.Now())
		}
		return
	}
	if err := r.store.RecordVersion(fleet.Key, harness, version, time.Now()); err != nil {
		return
	}
	// A harness never probed is left for setup or Check again.
	if probed, _, ok, err := r.store.LastProbed(fleet.Key, harness); err == nil && ok && probed != version {
		_, _ = r.runShared(ctx, fleet, harness, version)
	}
}

// Wait blocks until the background runs Refresh started have finished.
func (r *Runner) Wait() { r.bg.Wait() }

// ScoutGuardHarnesses lists the harnesses with a registered scout guard probe.
func ScoutGuardHarnesses() []string {
	var out []string
	for _, p := range defaultProbes() {
		if p.Capability == capabilities.ScoutGuard {
			out = append(out, p.Harness)
		}
	}
	return out
}
