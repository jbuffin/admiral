package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"
)

// EventKind is one kind of event a source can raise; Panel is set when the kind belongs to one of its panels.
type EventKind struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Panel string `json:"panel,omitempty"`
}

// Event is one outside event as a source sent it. A source's own tier is never read.
type Event struct {
	Kind     string
	Panel    string
	Ref      string
	Actor    string
	Author   string
	Viewer   string // the engineer's login, filled by the built-in GitHub source only
	Verified bool   // true only for events the built-in GitHub source saw itself
}

// maxEventsPerMinute bounds one source's delivered events in a fixed one-minute window.
const maxEventsPerMinute = 60

// maxEventField is the longest ref, actor or author a source may send.
const maxEventField = 512

// Events is the source's event catalog; ok is false when it has none (not asked, or it answered method-not-found).
func (r *runner) Events() (kinds []EventKind, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.catalog), r.hasCatalog
}

// pullCatalog asks the source for events.catalog; any failure leaves it with no catalog.
func (r *runner) pullCatalog(ctx context.Context, p conn) {
	res, err := p.call(ctx, "events.catalog", map[string]any{}, callTimeout)
	var kinds []EventKind
	if err == nil {
		err = decodeInto(res, &kinds)
	}
	if err == nil {
		for _, k := range kinds {
			if k.Kind == "" || hasControl(k.Kind) || hasControl(k.Panel) {
				err = fmt.Errorf("events.catalog: a kind %q is not usable", k.Kind)
				break
			}
		}
	}
	var rpc *rpcError
	if err != nil && !(errors.As(err, &rpc) && rpc.Code == codeMethodNotFound) {
		r.logf("events.catalog failed: %v", err)
	}
	r.mu.Lock()
	if err != nil {
		kinds = nil
	}
	r.catalog, r.hasCatalog = kinds, err == nil
	r.mu.Unlock()
}

// handleEvent validates one `event` notification against the catalog and delivers it to onEvent.
func (r *runner) handleEvent(params json.RawMessage) {
	var p struct {
		Kind   string `json:"kind"`
		Panel  string `json:"panel"`
		Ref    string `json:"ref"`
		Actor  string `json:"actor"`
		Author string `json:"author"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		r.logf("protocol error: an event with an unreadable shape")
		return
	}
	for _, f := range []string{p.Kind, p.Panel, p.Ref, p.Actor, p.Author} {
		if len(f) > maxEventField || hasControl(f) {
			r.logf("protocol error: an event field is too long or holds a control character")
			return
		}
	}
	if u, err := url.Parse(p.Ref); err != nil || p.Ref == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		r.logf("protocol error: an event whose ref is not a URL")
		return
	}
	r.mu.Lock()
	listed := false
	for _, k := range r.catalog {
		if r.hasCatalog && k.Kind == p.Kind && k.Panel == p.Panel {
			listed = true
		}
	}
	deliver := r.onEvent
	if listed {
		listed = r.spendBound(time.Now())
	}
	r.mu.Unlock()
	if !listed {
		r.logf("an event %q was dropped: not in the catalog, or the source is over %d a minute", p.Kind, maxEventsPerMinute)
		return
	}
	if deliver != nil {
		deliver(Event{Kind: p.Kind, Panel: p.Panel, Ref: p.Ref, Actor: p.Actor, Author: p.Author})
	}
}

// spendBound counts one delivery against the current minute, which starts at its first event; false when it is spent.
// r.mu must be held.
func (r *runner) spendBound(now time.Time) bool {
	if now.Sub(r.evWindow) >= time.Minute {
		r.evWindow, r.evCount = now, 0
	}
	if r.evCount >= maxEventsPerMinute {
		return false
	}
	r.evCount++
	return true
}

// emitFrom is what the built-in source delivers its own events through, trusted but bounded like a plugin's; nil
// when nothing listens.
func (r *runner) emitFrom(sid int) func(Event) {
	if r.onEvent == nil {
		return nil
	}
	return func(ev Event) {
		r.mu.Lock()
		ok := sid == r.sid && r.spendBound(time.Now())
		r.mu.Unlock()
		if ok {
			r.onEvent(ev)
		}
	}
}
