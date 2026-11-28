package push

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// NotStartedPushWait is how long past launch a session must be not started before it pushes; the inbox shows it sooner.
const NotStartedPushWait = 30 * time.Second

// Decided is an event with what Decide made of it. For a Clear, Event is the event that pushed.
type Decided struct {
	Event   Event
	Outcome Outcome
}

// Sink receives each event that Decide did not drop, and a Clear when a key that pushed stops needing the engineer.
// It must not block.
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
	now      func() time.Time

	mu         sync.Mutex
	shipsSeen  bool
	asking     map[string]bool // ship or crew boat -> waiting on a question or approval
	anchored   map[string]bool
	ready      map[string]bool
	polled     map[string]bool // moored ship -> its PRs have been polled since Admiral started
	mooredAt0  map[string]bool // ships already moored in the first snapshot
	fleetOf    map[string]ships.Ship
	waiting    bool // a not-started session is still inside NotStartedPushWait
	inboxSeen  bool
	inboxItems map[string]bool
	// pushed is the event behind each key whose last decided outcome was Push, for the kinds a snapshot can answer.
	pushed map[string]Event
}

// clearable is the kinds that stop needing the engineer when their key leaves a snapshot; the reply and routine kinds never do.
func clearable(k Kind) bool {
	switch k {
	case ShipQuestion, Anchored, ReadyToMerge, InboxQuestion, ChatQuestion, WakeProposal, Findings:
		return true
	}
	return false
}

func shipKind(k Kind) bool { return k == ShipQuestion || k == Anchored || k == ReadyToMerge }

// takeCleared removes and returns the pushed events of the ship kinds (or Inbox kinds) whose key is not in live.
// It is called with w.mu held.
func (w *Watcher) takeCleared(ships bool, live func(Event) bool) []Event {
	var out []Event
	for key, ev := range w.pushed {
		if shipKind(ev.Kind) == ships && !live(ev) {
			out = append(out, ev)
			delete(w.pushed, key)
		}
	}
	return out
}

func (w *Watcher) sendClears(evs []Event) {
	for _, ev := range evs {
		for _, s := range w.sinks {
			deliver(s, Decided{ev, Clear})
		}
	}
}

func NewWatcher(watching func() bool, notify func(fleet string) bool, sinks ...Sink) *Watcher {
	return &Watcher{watching: watching, notify: notify, sinks: sinks, now: time.Now}
}

// WithClock replaces the clock NotStartedPushWait is measured on.
func (w *Watcher) WithClock(now func() time.Time) *Watcher {
	w.now = now
	return w
}

// Waiting is whether a not-started session has yet to reach NotStartedPushWait, so its push needs another look with no event.
func (w *Watcher) Waiting() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.waiting
}

// early is whether a not-started session is still inside NotStartedPushWait.
func (w *Watcher) early(reason, launched string) bool {
	t, err := time.Parse(time.RFC3339, launched)
	return reason == "not_started" && err == nil && w.now().Sub(t) < NotStartedPushWait
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
	if out == Push && clearable(ev.Kind) {
		w.mu.Lock()
		if w.pushed == nil {
			w.pushed = map[string]Event{}
		}
		w.pushed[ev.Key] = ev
		w.mu.Unlock()
	}
	for _, s := range w.sinks {
		deliver(s, Decided{ev, out})
	}
}

// deliver keeps a panicking sink from taking the feed, and so Admiral, down with it.
func deliver(s Sink, d Decided) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("push: a sink panicked: %v", r)
		}
	}()
	s.Deliver(d)
}

// asks is whether a needs-you reason is a question or an approval, as the hooks and the bell write it.
// A finished turn (stop), idle, unknown and the ship-level crew reason are only counted.
func asks(reason string) bool {
	switch {
	case reason == "question", reason == "permission", reason == "bell", reason == "not_started",
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
	if reason == "not_started" {
		return "needs you to finish starting"
	}
	if reason == "bell" {
		return "needs you"
	}
	return "has a question"
}

func (w *Watcher) Ships(list []ships.Ship) {
	asking, anchored, ready, polled := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	fleetOf := make(map[string]ships.Ship, len(list))
	var events []Event
	waiting := false
	w.mu.Lock()
	first := !w.shipsSeen
	if first {
		w.mooredAt0 = map[string]bool{}
		for _, sh := range list {
			if sh.Status == ships.StatusMoored {
				w.mooredAt0[sh.Session] = true
			}
		}
	}
	note := func(seen map[string]bool, now map[string]bool, key string, ev Event) {
		now[key] = true
		if !first && !seen[key] {
			events = append(events, ev)
		}
	}
	for _, sh := range list {
		fleetOf[sh.Session] = sh
		base := Event{Fleet: sh.Fleet, FleetName: sh.FleetName, Name: sh.Name, Target: TargetPeek, Session: sh.Session}
		switch {
		case sh.Status == ships.StatusNeedsYou && asks(sh.Reason) && w.early(sh.Reason, sh.Launched):
			waiting = true
		case sh.Status == ships.StatusNeedsYou && asks(sh.Reason):
			ev := base
			ev.Kind, ev.Key, ev.Text = ShipQuestion, sh.Session, askText(sh.Reason)
			note(w.asking, asking, sh.Session, ev)
		case sh.Status == ships.StatusAnchored:
			ev := base
			ev.Kind, ev.Key, ev.Text, ev.Target = Anchored, sh.Session+":anchored", "stopped with an error", TargetShip
			note(w.anchored, anchored, sh.Session, ev)
		case sh.Status == ships.StatusMoored && sh.PolledAt != "":
			// Until a poll has answered, StackReady is false whatever the PRs say, so for a ship already moored at
			// startup the first answer only seeds; a ship that moors later is new.
			polled[sh.Session] = true
			if sh.StackReady {
				ev := base
				ev.Kind, ev.Key, ev.Text, ev.Merge = ReadyToMerge, sh.Session+":ready", "is ready to merge", true
				ready[sh.Session] = true
				if !first && !w.ready[sh.Session] && (w.polled[sh.Session] || !w.mooredAt0[sh.Session]) {
					events = append(events, ev)
				}
			}
		}
		for _, b := range sh.Crew {
			if b.Status != sessionhost.CrewNeedsYou || !asks(b.Reason) {
				continue
			}
			if w.early(b.Reason, b.Launched) {
				waiting = true
				continue
			}
			ev := base
			ev.Kind, ev.Key, ev.Text, ev.Crew = ShipQuestion, sh.Session+"/"+b.Name, b.Name+" "+askText(b.Reason), b.Name
			note(w.asking, asking, sh.Session+"/"+b.Name, ev)
		}
	}
	w.shipsSeen, w.asking, w.anchored, w.ready, w.polled, w.fleetOf, w.waiting = true, asking, anchored, ready, polled, fleetOf, waiting
	cleared := w.takeCleared(true, func(ev Event) bool {
		switch ev.Kind {
		case Anchored:
			return anchored[ev.Session]
		case ReadyToMerge:
			return ready[ev.Session]
		}
		return asking[ev.Key]
	})
	w.mu.Unlock()
	w.sendClears(cleared)
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
		if ev, ok := w.itemEvent(it, key); ok {
			events = append(events, ev)
		}
	}
	w.inboxSeen, w.inboxItems = true, seen
	cleared := w.takeCleared(false, func(ev Event) bool { return seen[ev.Key] })
	w.mu.Unlock()
	w.sendClears(cleared)
	for _, ev := range events {
		w.Emit(ev)
	}
}

// itemEvent is called with w.mu held.
func (w *Watcher) itemEvent(it inbox.Item, key string) (Event, bool) {
	var kind Kind
	switch it.Kind {
	case inbox.KindQuestion:
		kind = InboxQuestion
		if _, chat := sessionhost.ChatFleetOfSession(it.Session); chat {
			kind = ChatQuestion
		}
	case inbox.KindWakeProposal:
		kind = WakeProposal
		// The item sits in the Admiral session with no fleet, so its fleet is the proposal's ship's.
		if p, ok := it.WakeProposal(); ok {
			if sh, known := w.fleetOf[p.Session]; known {
				it.Fleet, it.FleetName = sh.Fleet, sh.FleetName
			}
		}
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
	// InboxReady is whether the inbox has done its first scan; a change before that is not read.
	InboxReady func() bool
	// Recheck is how often a session still inside NotStartedPushWait is looked at again; a second when zero.
	Recheck time.Duration

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

// ShipsChanged and InboxChanged are the OnChange hooks. The Inbox list is first read only once the inbox has
// scanned, so a chat question that lands first cannot make everything already waiting look new.
func (f *Feed) ShipsChanged() { f.init(); poke(f.ship) }
func (f *Feed) InboxChanged() { f.init(); poke(f.item) }

func (f *Feed) Run(ctx context.Context) {
	f.init()
	// A not-started session's push comes due with no event to announce it.
	every := f.Recheck
	if every <= 0 {
		every = time.Second
	}
	recheck := time.NewTicker(every)
	defer recheck.Stop()
	for {
		select {
		case <-recheck.C:
			if f.Watcher.Waiting() {
				f.Watcher.Ships(f.Ships())
			}
		case <-f.ship:
			f.Watcher.Ships(f.Ships())
		case <-f.item:
			if f.InboxReady != nil && !f.InboxReady() {
				continue
			}
			f.Watcher.Inbox(f.Items())
		case <-ctx.Done():
			return
		}
	}
}
