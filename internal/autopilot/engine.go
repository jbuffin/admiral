// Package autopilot launches the orders of panels the engineer has put on
// autopilot (#114). A pass looks at the ships and the listings and launches
// what the caps leave room for; it never reads a ship's outcome back from the
// log (ADR 0015).
package autopilot

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
)

// backoff is how long an order waits after its first and second failed launch;
// the third gives up.
var backoff = []time.Duration{10 * time.Minute, time.Hour}

// Source is one order source as a pass sees it.
type Source struct {
	Key string
	// Listed says the source's runner is ok and has listed its panels since
	// Admiral started. Only then does the raw listing say which orders are gone.
	Listed bool
	// Panels is the raw listing: the orders a ship hides are still in it.
	Panels []sources.Panel
}

// Engine is autopilot. The func fields are its seams, in the idiom of
// sources.Service.
type Engine struct {
	Config  func() *config.Config
	Ships   func() []ships.Ship
	Sources func() []Source // in any order; the engine applies the sidebar's
	Paused  func(source, panel string) bool
	// Dir is the checkout to launch the order in, or "" and why not.
	Dir func(fleet config.Fleet, harness string, o sources.Order) (dir, reason string)
	// Launch is ships.Service.LaunchOrder.
	Launch func(launcher.Request) (ships.Ship, *launcher.Error)
	// Record writes a record to the outcome log.
	Record func(outcomes.Record)
	Now    func() time.Time
	Store  *Store
	// Logf takes one line; nil logs nothing.
	Logf func(format string, args ...any)

	pass    sync.Mutex // one pass at a time
	mu      sync.Mutex // guards skipped
	skipped map[pair]string
	poke    chan struct{}
	pokeMu  sync.Once
}

func (e *Engine) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Poke asks Run for a pass. It never blocks.
func (e *Engine) Poke() {
	e.pokeMu.Do(func() { e.poke = make(chan struct{}, 1) })
	select {
	case e.poke <- struct{}{}:
	default:
	}
}

// Run makes a pass after every poke and once a minute, until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	e.Poke()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-e.poke:
		case <-tick.C:
		case <-ctx.Done():
			return
		}
		e.Pass()
	}
}

// Hot says the engine has given up on the order after three failed launches.
func (e *Engine) Hot(source, orderID string) bool {
	return e.Store.failureOf(source, orderID).GaveUp
}

// Launched adds an order launched by hand to the launched set, so autopilot
// does not run it again while it stays listed.
func (e *Engine) Launched(source, orderID string) {
	if err := e.Store.markLaunched(source, orderID); err != nil {
		e.logf("autopilot: could not save %s: %v", FileName, err)
	}
}

// Pass makes one pass: for each eligible panel in sidebar order, it launches
// the panel's visible orders that autopilot has not launched, until a cap is full.
func (e *Engine) Pass() {
	e.pass.Lock()
	defer e.pass.Unlock()
	cfg := e.Config()
	list := e.Ships()
	srcs := map[string]Source{}
	for _, src := range e.Sources() {
		srcs[src.Key] = src
		e.forget(src)
	}

	total, perPanel := 0, map[[2]string]int{}
	for _, sh := range list {
		if sh.Status == ships.StatusUnderway || sh.Status == ships.StatusNeedsYou {
			total++
			if sh.Source != "" && sh.Panel != "" {
				perPanel[[2]string{sh.Source, sh.Panel}]++
			}
		}
	}
	hidden := ships.HiddenOrders(list)
	now := e.Now()

	for _, def := range cfg.SourceList {
		src, ok := srcs[def.Key]
		if !ok {
			continue
		}
		panels := slices.Clone(src.Panels)
		sources.SortPanels(panels, def.PanelOrder)
		for _, p := range panels {
			entry, ok := def.Autopilot[p.ID]
			if !ok || !entry.Enabled || e.Paused(def.Key, p.ID) {
				continue
			}
			fleet, ok := cfg.Fleet(entry.Fleet)
			if !ok {
				continue
			}
			cell := [2]string{def.Key, p.ID}
			for _, o := range p.Orders {
				if total >= cfg.AutopilotMaxShips {
					return
				}
				if perPanel[cell] >= entry.MaxShips {
					break
				}
				key := pair{def.Key, o.ID}
				if hidden[ships.OrderKey{Source: def.Key, OrderID: o.ID}] || e.Store.hasLaunched(def.Key, o.ID) {
					continue
				}
				if f := e.Store.failureOf(def.Key, o.ID); f.GaveUp || now.Before(f.NextRetry) {
					continue
				}
				dir, reason := e.Dir(fleet, entry.Harness, o)
				if dir == "" {
					e.skip(def.Key, p.ID, o, reason)
					continue
				}
				e.mu.Lock()
				delete(e.skipped, key)
				e.mu.Unlock()
				req := launcher.Request{Fleet: entry.Fleet, Harness: entry.Harness, Dir: dir,
					Skill:  launcher.Skill{Kind: entry.Skill.Kind, Name: entry.Skill.Name},
					Source: def.Key, OrderID: o.ID, Panel: p.ID, Autopilot: true, LowStakes: entry.LowStakes,
					ExtraArgs: slices.Clone(entry.Args)}
				switch sh, lerr := e.Launch(req); {
				case lerr == nil:
					e.Launched(def.Key, o.ID)
					total++
					perPanel[cell]++
					hidden[ships.OrderKey{Source: def.Key, OrderID: o.ID}] = true
					e.logf("autopilot: launched %s from %s/%s", sh.Session, def.Key, p.ID)
				case lerr.Status == http.StatusConflict:
					// The order has a ship or a launch of it is running: not a failure.
				case lerr.Status == http.StatusServiceUnavailable && lerr.Msg == ships.NotKnownMsg:
					return // no ship list to count against; the next change tries again
				default:
					e.failed(def.Key, p.ID, o, lerr, now)
				}
			}
		}
	}
}

// forget drops what a source that has listed no longer lists.
func (e *Engine) forget(src Source) {
	if !src.Listed {
		return
	}
	listed := map[string]bool{}
	for _, p := range src.Panels {
		for _, o := range p.Orders {
			listed[o.ID] = true
		}
	}
	if err := e.Store.prune(src.Key, listed); err != nil {
		e.logf("autopilot: could not save %s: %v", FileName, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for k := range e.skipped {
		if k.Source == src.Key && !listed[k.OrderID] {
			delete(e.skipped, k)
		}
	}
}

// skip writes the order's skipped record, once until its reason changes or it
// leaves the listing.
func (e *Engine) skip(source, panel string, o sources.Order, reason string) {
	e.mu.Lock()
	key := pair{source, o.ID}
	if e.skipped == nil {
		e.skipped = map[pair]string{}
	}
	again := e.skipped[key] == reason
	if !again {
		e.skipped[key] = reason
	}
	e.mu.Unlock()
	if again {
		return
	}
	e.logf("autopilot: skipped %s/%s order %s: %s", source, panel, o.ID, reason)
	e.Record(outcomes.Record{Event: "skipped", Fields: map[string]any{
		"source": source, "panel": panel, "order_id": o.ID, "title": o.Title, "reason": reason}})
}

// failed counts a failed launch: it backs the order off, and the third gives up
// on it.
func (e *Engine) failed(source, panel string, o sources.Order, lerr *launcher.Error, now time.Time) {
	f := e.Store.failureOf(source, o.ID)
	f.Attempts++
	fields := map[string]any{"source": source, "panel": panel, "order_id": o.ID, "title": o.Title,
		"reason": lerr.Msg, "attempt": f.Attempts}
	if f.Attempts <= len(backoff) {
		f.NextRetry = now.Add(backoff[f.Attempts-1])
		fields["next_retry"] = f.NextRetry.UTC().Format(time.RFC3339)
	} else {
		f.GaveUp = true
	}
	if err := e.Store.setFailure(source, o.ID, f); err != nil {
		e.logf("autopilot: could not save %s: %v", FileName, err)
	}
	e.logf("autopilot: launch of %s/%s order %s failed (attempt %d): %s", source, panel, o.ID, f.Attempts, lerr.Msg)
	e.Record(outcomes.Record{Event: "launch_failed", Fields: fields})
}
