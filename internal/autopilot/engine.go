// Package autopilot launches the orders of panels the engineer has put on
// autopilot (#114). A pass looks at the ships and the listings and launches
// what the caps leave room for. It reads the outcome log for one purpose only:
// counting runs toward retro orders (#117, ADR 0017).
package autopilot

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/resources"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
)

// backoff is how long an order waits after its first and second failed launch;
// the third gives up.
var backoff = []time.Duration{10 * time.Minute, time.Hour}

// Source is one order source as a pass sees it.
type Source struct {
	Key string
	// Listed says the source's runner is ok and every panel has had a successful
	// orders.list since the source started. Only then does the raw listing say
	// which orders are gone.
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
	// Checkout says dir is still a checkout of the fleet, for a resume order's
	// recorded directory (#120).
	Checkout func(fleet config.Fleet, dir string) bool
	// Launch is ships.Service.LaunchOrder.
	Launch func(launcher.Request) (ships.Ship, *launcher.Error)
	// Record writes a record to the outcome log (outcomes.Writer.Enqueue). done,
	// when not nil, gets the error of a failed or dropped write, or nil once the
	// line is in the log.
	Record func(r outcomes.Record, done func(error))
	// Log is the outcome log, outcomes.Read of the home's outcomes directory; nil
	// turns retro orders off.
	Log func() []outcomes.Line
	// Known says the ship list can be trusted (sessionhost.Host.Known). While it
	// is not, the list is empty and the retro step adds nothing; nil means always.
	Known func() bool
	// AddOrder adds an order to a panel (sources.Service.AddOrder); nil turns
	// retro orders off. An error that implements AddKind() inbox.AddKind says
	// whether to try again; one that doesn't is final.
	AddOrder func(ctx context.Context, source, panel, text string) (id string, err error)
	Now      func() time.Time
	Store    *Store
	// OnHot runs when an order becomes hot or stops being; nil does nothing. The
	// sources view shows the mark, so the caller pushes it. It must not block.
	OnHot func()
	// Resources gates the launches of panels that name resources (#118); nil
	// gates nothing.
	Resources *resources.Set
	// OnWait runs when a panel starts or stops waiting for resources, or the
	// resources it waits for change; nil does nothing. The sources view shows
	// it, so the caller pushes it. It must not block.
	OnWait func()
	// Logf takes one line; nil logs nothing.
	Logf func(format string, args ...any)

	pass    sync.Mutex // one pass at a time; also keeps two retro adds for a panel apart
	mu      sync.Mutex // guards skipped
	skipped map[pair]string
	waits   map[pair]*wait       // orders waiting for resources
	waiting map[[2]string]string // panel -> the order its wait shows
	adding  map[[2]string]int    // panel -> resume adds in flight; under mu
	poke    chan struct{}
	pokeMu  sync.Once

	retros retroState
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

// BeginResume is called before a resume order is added to the panel, and the
// func it returns after, with the id the add gave ("" if none). In between,
// Pass holds off on the panel's orders that have no record, for the new order
// may show in a listing before it is recorded. The record says the order
// launches in dir, under the entry of the panel the ship that wrote the report
// counted against (originSource and originPanel, "" for none).
func (e *Engine) BeginResume(source, panel, dir, originSource, originPanel string) (done func(orderID string)) {
	key := [2]string{source, panel}
	e.mu.Lock()
	if e.adding == nil {
		e.adding = map[[2]string]int{}
	}
	e.adding[key]++
	e.mu.Unlock()
	return func(orderID string) {
		if orderID != "" && dir != "" {
			if err := e.Store.addResume(source, orderID, Resume{Dir: dir, OriginSource: originSource, OriginPanel: originPanel}); err != nil {
				e.logf("autopilot: could not save %s: %v", FileName, err)
			}
		}
		e.mu.Lock()
		if e.adding[key]--; e.adding[key] <= 0 {
			delete(e.adding, key)
		}
		e.mu.Unlock()
		e.Poke()
	}
}

func (e *Engine) addingTo(source, panel string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.adding[[2]string{source, panel}] > 0
}

// origin is the entry a resume order launches under: that of the panel the ship
// that wrote the report counted against. It is nil when the ship was not
// launched by autopilot, or its panel has no entry in the config any more.
type origin struct {
	source, panel string
	entry         config.PanelAutopilot
}

func originOf(cfg *config.Config, r Resume) *origin {
	if r.OriginSource == "" || r.OriginPanel == "" {
		return nil
	}
	for _, def := range cfg.SourceList {
		if def.Key == r.OriginSource {
			if entry, ok := def.Autopilot[r.OriginPanel]; ok {
				return &origin{r.OriginSource, r.OriginPanel, entry}
			}
		}
	}
	return nil
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
	// After the launches, however they end: the retro step needs no room under a cap.
	defer e.retroStep(cfg, list, srcs)

	total, perPanel := 0, map[[2]string]int{}
	for _, sh := range list {
		if sh.Status == ships.StatusUnderway || sh.Status == ships.StatusNeedsYou {
			total++
			switch {
			case sh.OriginSource != "" && sh.OriginPanel != "":
				perPanel[[2]string{sh.OriginSource, sh.OriginPanel}]++
			case sh.Source != "" && sh.Panel != "":
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
			panelEntry, ok := def.Autopilot[p.ID]
			if !ok || !panelEntry.Enabled || e.Paused(def.Key, p.ID) {
				e.endPanelWaits(def.Key, p.ID)
				continue
			}
			if _, ok := cfg.Fleet(panelEntry.Fleet); !ok {
				continue
			}
			panelCell := [2]string{def.Key, p.ID}
			for _, o := range p.Orders {
				if total >= cfg.AutopilotMaxShips {
					return
				}
				// A resume order runs under the entry of the panel its ship came
				// from (#120), but with the skill of this panel's: its text starts
				// with the skill already.
				entry, cell := panelEntry, panelCell
				// Read before the record: the add writes it before it releases the
				// hold, so no hold and then no record means it is unrecorded.
				adding := e.addingTo(def.Key, p.ID)
				rec, resume := e.Store.resumeOf(def.Key, o.ID)
				if org := originOf(cfg, rec); resume && org != nil {
					entry, cell = org.entry, [2]string{org.source, org.panel}
					entry.Skill = panelEntry.Skill
					if !entry.Enabled || e.Paused(org.source, org.panel) || perPanel[cell] >= entry.MaxShips {
						continue // it waits for its panel; the others go on
					}
				} else if perPanel[cell] >= entry.MaxShips {
					break
				}
				fleet, ok := cfg.Fleet(entry.Fleet)
				if !ok {
					continue
				}
				key := pair{def.Key, o.ID}
				if hidden[ships.OrderKey{Source: def.Key, OrderID: o.ID}] || e.Store.hasLaunched(def.Key, o.ID) {
					e.endWait(def.Key, p.ID, o.ID)
					continue
				}
				if f := e.Store.failureOf(def.Key, o.ID); f.GaveUp || now.Before(f.NextRetry) {
					continue
				}
				if !resume && adding {
					continue // it may be the order being added, not recorded yet
				}
				var dir, reason string
				if resume {
					dir, reason = rec.Dir, ""
					if e.Checkout == nil || !e.Checkout(fleet, dir) {
						dir, reason = "", "the checkout of the ship that wrote the report is gone or is not a checkout of fleet "+fleet.Name+": "+rec.Dir
					}
				} else {
					dir, reason = e.Dir(fleet, entry.Harness, o)
				}
				if dir == "" {
					e.skip(def.Key, p.ID, o, reason)
					continue
				}
				e.mu.Lock()
				delete(e.skipped, key)
				e.mu.Unlock()
				release := func() {}
				if e.Resources != nil && len(entry.Resources) > 0 {
					rel, waits := e.Resources.Reserve(entry.Resources)
					if waits != nil {
						e.startWait(def.Key, p.ID, o, waits)
						if resume {
							continue // not the panel's other orders: these are the origin's resources
						}
						break // the panel's other orders need the same resources
					}
					release = rel
				}
				e.endWait(def.Key, p.ID, o.ID)
				req := launcher.Request{Fleet: entry.Fleet, Harness: entry.Harness, Dir: dir,
					Skill:  launcher.Skill{Kind: entry.Skill.Kind, Name: entry.Skill.Name},
					Source: def.Key, OrderID: o.ID, Panel: p.ID, OriginSource: cell[0], OriginPanel: cell[1], Autopilot: true, LowStakes: entry.LowStakes, AutoMerge: entry.AutoMerge,
					ExtraArgs: slices.Clone(entry.Args), Resources: slices.Clone(entry.Resources)}
				sh, lerr := e.Launch(req)
				release() // a launched ship is counted from the ship list now
				switch {
				case lerr == nil:
					e.Launched(def.Key, o.ID)
					total++
					perPanel[cell]++
					hidden[ships.OrderKey{Source: def.Key, OrderID: o.ID}] = true
					e.logf("autopilot: launched %s from %s/%s", sh.Session, def.Key, p.ID)
				case lerr.Status == http.StatusConflict, lerr.Status == http.StatusNotFound && strings.HasPrefix(lerr.Msg, "order "):
					// The order has a ship or a launch of it is running, or it
					// left the listing since the pass began: not a failure.
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
	cooled, err := e.Store.prune(src.Key, listed)
	if err != nil {
		e.logf("autopilot: could not save %s: %v", FileName, err)
	}
	if cooled && e.OnHot != nil {
		e.OnHot()
	}
	e.mu.Lock()
	for k := range e.skipped {
		if k.Source == src.Key && !listed[k.OrderID] {
			delete(e.skipped, k)
		}
	}
	var gone []pair
	for k := range e.waits {
		if k.Source == src.Key && !listed[k.OrderID] {
			gone = append(gone, k)
		}
	}
	e.mu.Unlock()
	for _, k := range gone {
		e.endWait(k.Source, e.waitPanel(k), k.OrderID)
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
		"source": source, "panel": panel, "order_id": o.ID, "title": launcher.CleanTitle(o.Title), "reason": reason}}, nil)
}

// failed counts a failed launch: it backs the order off, and the third gives up
// on it.
func (e *Engine) failed(source, panel string, o sources.Order, lerr *launcher.Error, now time.Time) {
	f := e.Store.failureOf(source, o.ID)
	f.Attempts++
	fields := map[string]any{"source": source, "panel": panel, "order_id": o.ID, "title": launcher.CleanTitle(o.Title),
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
	e.Record(outcomes.Record{Event: "launch_failed", Fields: fields}, nil)
	if f.GaveUp && e.OnHot != nil {
		e.OnHot()
	}
}

// wait is an order waiting for resources.
type wait struct {
	panel    string
	names    []string
	recorded bool // the resource_wait record is written
}

func (e *Engine) waitPanel(k pair) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if w := e.waits[k]; w != nil {
		return w.panel
	}
	return ""
}

// WaitingFor is the resources the panel's waiting order waits for, sorted; empty
// when the panel isn't waiting.
func (e *Engine) WaitingFor(source, panel string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.waitingFor(source, panel)
}

func (e *Engine) waitingFor(source, panel string) []string {
	id, ok := e.waiting[[2]string{source, panel}]
	if !ok {
		return []string{}
	}
	return slices.Clone(e.waits[pair{source, id}].names)
}

// startWait notes that the order waits for these resources. It writes the
// order's resource_wait record the first time every check has finished once, so
// at most one per wait however its busy and missing resources change.
func (e *Engine) startWait(source, panel string, o sources.Order, waits []resources.Wait) {
	names, pending := make([]string, len(waits)), false
	for i, w := range waits {
		names[i] = w.Name
		pending = pending || w.Why == resources.Pending
	}
	key := pair{source, o.ID}
	e.mu.Lock()
	before := e.waitingFor(source, panel)
	if e.waits == nil {
		e.waits, e.waiting = map[pair]*wait{}, map[[2]string]string{}
	}
	w := e.waits[key]
	if w == nil {
		w = &wait{panel: panel}
		e.waits[key] = w
	}
	w.names = names
	e.waiting[[2]string{source, panel}] = o.ID
	record := !pending && !w.recorded
	w.recorded = w.recorded || record
	changed := !slices.Equal(before, names)
	e.mu.Unlock()
	if record {
		e.logf("autopilot: %s/%s order %s waits for %s", source, panel, o.ID, strings.Join(names, ", "))
		e.Record(outcomes.Record{Event: "resource_wait", Fields: map[string]any{
			"source": source, "panel": panel, "order_id": o.ID, "title": launcher.CleanTitle(o.Title), "resources": waits}}, nil)
	}
	if changed && e.OnWait != nil {
		e.OnWait()
	}
}

// endWait notes that the order doesn't wait any more.
func (e *Engine) endWait(source, panel, orderID string) {
	key := pair{source, orderID}
	e.mu.Lock()
	if e.waits[key] == nil {
		e.mu.Unlock()
		return
	}
	before := e.waitingFor(source, panel)
	delete(e.waits, key)
	if e.waiting[[2]string{source, panel}] == orderID {
		delete(e.waiting, [2]string{source, panel})
	}
	changed := !slices.Equal(before, e.waitingFor(source, panel))
	e.mu.Unlock()
	if changed && e.OnWait != nil {
		e.OnWait()
	}
}

// endPanelWaits ends every wait of a panel autopilot has stopped looking at.
func (e *Engine) endPanelWaits(source, panel string) {
	e.mu.Lock()
	var ids []string
	for k, w := range e.waits {
		if k.Source == source && w.panel == panel {
			ids = append(ids, k.OrderID)
		}
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.endWait(source, panel, id)
	}
}
