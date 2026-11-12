package push

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// Decided is an event with what Decide made of it.
type Decided struct {
	Event   Event
	Outcome Outcome
}

// Sink receives each event that Decide did not drop. It must not block.
type Sink interface {
	Deliver(Decided)
}

// Recorder is a Sink that keeps what it was given.
type Recorder struct {
	mu     sync.Mutex
	events []Decided
}

func (r *Recorder) Deliver(d Decided) {
	r.mu.Lock()
	r.events = append(r.events, d)
	r.mu.Unlock()
}

func (r *Recorder) Events() []Decided {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Decided(nil), r.events...)
}

// Watcher turns ship and Inbox snapshots into events, once per new occurrence. The first snapshot of each kind is
// what already waits at startup: seen, not new.
type Watcher struct {
	watching func() bool
	notify   func(fleet string) bool
	sinks    []Sink

	mu         sync.Mutex
	shipsSeen  bool
	asking     map[string]bool // ship or crew boat -> waiting on a question or approval
	anchored   map[string]bool
	ready      map[string]bool
	inboxSeen  bool
	inboxItems map[string]bool
}

func NewWatcher(watching func() bool, notify func(fleet string) bool, sinks ...Sink) *Watcher {
	return &Watcher{watching: watching, notify: notify, sinks: sinks}
}

// Emit is the way in for events that no detector produces yet.
func (w *Watcher) Emit(ev Event) {
	notify := true
	if ev.Fleet != "" {
		notify = w.notify(ev.Fleet)
	}
	out := Decide(ev, w.watching(), notify)
	if out == Nothing {
		return
	}
	for _, s := range w.sinks {
		s.Deliver(Decided{ev, out})
	}
}

// asks is whether a needs-you reason is a question or an approval, as the hooks and the bell write it.
// A finished turn (stop), idle, unknown and the ship-level crew reason are only counted.
func asks(reason string) bool {
	switch {
	case reason == "question", reason == "permission", reason == "bell",
		strings.HasPrefix(reason, "permission:"),
		reason == "notification:permission_prompt", reason == "notification:elicitation_dialog":
		return true
	}
	return false
}

func askText(reason string) string {
	if strings.HasPrefix(reason, "permission") || strings.HasSuffix(reason, "permission_prompt") {
		return "needs your approval"
	}
	if reason == "bell" {
		return "needs you"
	}
	return "has a question"
}

func (w *Watcher) Ships(list []ships.Ship) {
	asking, anchored, ready := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var events []Event
	w.mu.Lock()
	first := !w.shipsSeen
	note := func(seen map[string]bool, now map[string]bool, key string, ev Event) {
		now[key] = true
		if !first && !seen[key] {
			events = append(events, ev)
		}
	}
	for _, sh := range list {
		base := Event{Fleet: sh.Fleet, FleetName: sh.FleetName, Name: sh.Name, Target: TargetPeek, Session: sh.Session}
		switch {
		case sh.Status == ships.StatusNeedsYou && asks(sh.Reason):
			ev := base
			ev.Kind, ev.Key, ev.Text = ShipQuestion, sh.Session, askText(sh.Reason)
			note(w.asking, asking, sh.Session, ev)
		case sh.Status == ships.StatusAnchored:
			ev := base
			ev.Kind, ev.Key, ev.Text, ev.Target = Anchored, sh.Session+":anchored", "stopped with an error", TargetShip
			note(w.anchored, anchored, sh.Session, ev)
		case sh.Status == ships.StatusMoored && sh.StackReady:
			ev := base
			ev.Kind, ev.Key, ev.Text, ev.Merge = ReadyToMerge, sh.Session+":ready", "is ready to merge", true
			note(w.ready, ready, sh.Session, ev)
		}
		for _, b := range sh.Crew {
			if b.Status != sessionhost.CrewNeedsYou || !asks(b.Reason) {
				continue
			}
			ev := base
			ev.Kind, ev.Key, ev.Text, ev.Crew = ShipQuestion, sh.Session+"/"+b.Name, b.Name+" "+askText(b.Reason), b.Name
			note(w.asking, asking, sh.Session+"/"+b.Name, ev)
		}
	}
	w.shipsSeen, w.asking, w.anchored, w.ready = true, asking, anchored, ready
	w.mu.Unlock()
	for _, ev := range events {
		w.Emit(ev)
	}
}

func (w *Watcher) Inbox(items []inbox.Item) {
	seen := make(map[string]bool, len(items))
	var events []Event
	w.mu.Lock()
	first := !w.inboxSeen
	for _, it := range items {
		key := it.Session + "/" + it.ID
		seen[key] = true
		if first || w.inboxItems[key] {
			continue
		}
		if ev, ok := itemEvent(it, key); ok {
			events = append(events, ev)
		}
	}
	w.inboxSeen, w.inboxItems = true, seen
	w.mu.Unlock()
	for _, ev := range events {
		w.Emit(ev)
	}
}

func itemEvent(it inbox.Item, key string) (Event, bool) {
	var kind Kind
	switch it.Kind {
	case inbox.KindQuestion:
		kind = InboxQuestion
		if _, chat := sessionhost.ChatFleetOfSession(it.Session); chat {
			kind = ChatQuestion
		}
	case inbox.KindWakeProposal:
		kind = WakeProposal
	case inbox.KindFindings:
		kind = Findings
	default:
		return Event{}, false
	}
	var title string
	_ = json.Unmarshal(it.Field("title"), &title)
	name := it.Name
	if name == "" {
		name = it.FleetName
	}
	if name == "" {
		name = "Admiral"
	}
	return Event{Kind: kind, Fleet: it.Fleet, FleetName: it.FleetName, Key: key, Name: name, Text: title, Target: TargetPeek, Session: it.Session, Item: it.ID}, true
}

// Feed runs a Watcher on change signals: the signals never block, and a burst of them is read once.
type Feed struct {
	Watcher *Watcher
	Ships   func() []ships.Ship
	Items   func() []inbox.Item

	ship, item chan struct{}
	once       sync.Once
}

func (f *Feed) init() {
	f.once.Do(func() { f.ship, f.item = make(chan struct{}, 1), make(chan struct{}, 1) })
}

func poke(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// ShipsChanged and InboxChanged are the OnChange hooks. The Inbox list is first read only after its own first
// change, so a startup scan that has not run yet cannot make everything already waiting look new.
func (f *Feed) ShipsChanged() { f.init(); poke(f.ship) }
func (f *Feed) InboxChanged() { f.init(); poke(f.item) }

func (f *Feed) Run(ctx context.Context) {
	f.init()
	for {
		select {
		case <-f.ship:
			f.Watcher.Ships(f.Ships())
		case <-f.item:
			f.Watcher.Inbox(f.Items())
		case <-ctx.Done():
			return
		}
	}
}
