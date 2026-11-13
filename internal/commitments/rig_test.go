package commitments_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ledger"
)

const fleet = "work"

var t0 = time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)

type waiter struct {
	at time.Time
	ch chan time.Time
}

// fakeClock fires a timer only when a test moves the time past it.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := &waiter{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		w.ch <- c.now
		return w.ch
	}
	c.waiters = append(c.waiters, w)
	return w.ch
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	keep := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(t) {
			keep = append(keep, w)
		} else {
			w.ch <- t
		}
	}
	c.waiters = keep
}

func (c *fakeClock) hasDeadline(t time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.waiters {
		if w.at.Equal(t) {
			return true
		}
	}
	return false
}

type launchCall struct {
	ctx context.Context
	req launcher.Request
}

type fakeShips struct {
	mu      sync.Mutex
	calls   []launchCall
	launch  func(n int, req launcher.Request) (commitments.ShipRef, *launcher.Error)
	holders map[string]commitments.ShipRef
}

func (f *fakeShips) Launch(ctx context.Context, req launcher.Request) (commitments.ShipRef, *launcher.Error) {
	f.mu.Lock()
	f.calls = append(f.calls, launchCall{ctx, req})
	n, fn := len(f.calls), f.launch
	f.mu.Unlock()
	if fn != nil {
		return fn(n, req)
	}
	return commitments.ShipRef{Session: "ship-" + strconv.Itoa(n), Name: "Ship " + strconv.Itoa(n)}, nil
}

func (f *fakeShips) Holder(key string) (commitments.ShipRef, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.holders[key]
	return s, ok
}

func (f *fakeShips) hold(key string, s commitments.ShipRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holders == nil {
		f.holders = map[string]commitments.ShipRef{}
	}
	f.holders[key] = s
}

func (f *fakeShips) requests() []launchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]launchCall(nil), f.calls...)
}

// fakeCards keeps cards by ID the way the chat log does: a second post of an ID adds nothing.
type fakeCards struct {
	mu     sync.Mutex
	byID   map[string]commitments.Card
	order  []string
	posts  int
	onPost func(c commitments.Card) error // runs after the card is kept; a non-nil error is the post failing
}

func (f *fakeCards) PostCard(fl string, c commitments.Card) error {
	f.mu.Lock()
	f.posts++
	if f.byID == nil {
		f.byID = map[string]commitments.Card{}
	}
	if _, ok := f.byID[c.ID]; !ok {
		f.byID[c.ID] = c
		f.order = append(f.order, c.ID)
	}
	hook := f.onPost
	f.mu.Unlock()
	if hook != nil {
		return hook(c)
	}
	return nil
}

func (f *fakeCards) cards() []commitments.Card {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []commitments.Card
	for _, id := range f.order {
		out = append(out, f.byID[id])
	}
	return out
}

type rig struct {
	t     *testing.T
	home  string
	clk   *fakeClock
	led   *ledger.Ledger
	ships *fakeShips
	cards *fakeCards
	r     *commitments.Runner
}

func newRig(t *testing.T) *rig {
	t.Helper()
	g := &rig{t: t, home: t.TempDir(), clk: &fakeClock{now: t0}, ships: &fakeShips{}, cards: &fakeCards{}}
	g.led = ledger.New(g.home, g.clk.Now)
	g.restart()
	return g
}

// restart is a new process over the same ledger, outcome log and chat.
func (g *rig) restart() {
	g.r = &commitments.Runner{
		Home:   g.home,
		Ledger: g.led,
		Fleets: func() []string { return []string{fleet} },
		SourceFleet: func(source string) (string, bool) {
			fl, ok := map[string]string{"tracker": "work", "theirs": "home"}[source]
			return fl, ok
		},
		Clock: commitments.Clock{Now: g.clk.Now, After: g.clk.After},
		Ships: g.ships,
		Cards: g.cards,
	}
}

func (g *rig) tick() { g.t.Helper(); g.r.Tick(context.Background()) }

func (g *rig) add(c ledger.Commitment) ledger.Commitment {
	g.t.Helper()
	got, err := g.led.AddCommitment(fleet, c, actor.Via)
	if err != nil {
		g.t.Fatalf("AddCommitment(%+v) = %v", c, err)
	}
	return got
}

func (g *rig) open() []ledger.Commitment {
	g.t.Helper()
	list, err := g.led.Commitments(fleet)
	if err != nil {
		g.t.Fatal(err)
	}
	return list
}

func (g *rig) find(id string) (ledger.Commitment, bool) {
	for _, c := range g.open() {
		if c.ID == id {
			return c, true
		}
	}
	return ledger.Commitment{}, false
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

const launchSpec = `{"fleet":"work","harness":"claude","dir":"/work/repo","skill":{"kind":"none"},"text":"fix the build"}`

func remindAt(when time.Time) ledger.Commitment {
	return ledger.Commitment{Text: "tell me the build is green", Trigger: ledger.Trigger{Kind: ledger.TriggerTime, Subject: stamp(when)}, Action: ledger.ActionRemind}
}

func launchAt(when time.Time) ledger.Commitment {
	return ledger.Commitment{Text: "fix the build", Trigger: ledger.Trigger{Kind: ledger.TriggerTime, Subject: stamp(when)}, Action: ledger.ActionLaunch, Launch: json.RawMessage(launchSpec)}
}

func remindNone() ledger.Commitment {
	return ledger.Commitment{Text: "remember the harbour log", Trigger: ledger.Trigger{Kind: ledger.TriggerNone}, Action: ledger.ActionRemind}
}

// shipTrigger watches ship saltpaw as launched an hour before t0, with a remind as its action.
func shipTrigger(kind string) ledger.Commitment {
	return ledger.Commitment{Text: "tell me when saltpaw is done", Trigger: ledger.Trigger{Kind: kind, Subject: "saltpaw", Launched: stamp(t0.Add(-time.Hour))}, Action: ledger.ActionRemind}
}

// event appends one outcome-log line.
func (g *rig) event(ship, event string, at time.Time, fields map[string]any) {
	g.t.Helper()
	line := map[string]any{"v": 1, "ts": stamp(at), "ship": ship, "event": event}
	for k, v := range fields {
		line[k] = v
	}
	raw, err := json.Marshal(line)
	if err != nil {
		g.t.Fatal(err)
	}
	dir := filepath.Join(g.home, "outcomes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		g.t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, at.UTC().Format("2006-01")+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		g.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		g.t.Fatal(err)
	}
}

func (g *rig) launched(ship string, at time.Time) {
	g.event(ship, "launched", at, map[string]any{"fleet": fleet})
}

func (g *rig) endedClean(ship string, at time.Time) {
	g.event(ship, "ended", at, map[string]any{"exit_code": 0, "signal": nil})
}

func (g *rig) endedError(ship string, at time.Time) {
	g.event(ship, "ended", at, map[string]any{"exit_code": 1, "signal": nil})
}

func (g *rig) stopped(ship string, at time.Time) {
	g.event(ship, "ended", at, map[string]any{"exit_code": nil, "signal": "KILL"})
	g.event(ship, "stopped", at.Add(time.Second), nil)
}

func (g *rig) unmoored(ship, reason string, at time.Time) {
	g.event(ship, "unmoored", at, map[string]any{"reason": reason})
}

func (g *rig) cardFor(id, outcome string) (commitments.Card, bool) {
	want := "commitment-" + id + "-" + outcome
	for _, c := range g.cards.cards() {
		if c.ID == want {
			return c, true
		}
	}
	return commitments.Card{}, false
}

// wantCard fails unless the commitment has a card of the outcome and is closed, and returns the card.
func (g *rig) wantCard(id, outcome string) commitments.Card {
	g.t.Helper()
	c, ok := g.cardFor(id, outcome)
	if !ok {
		g.t.Fatalf("no card commitment-%s-%s among %+v, want one", id, outcome, g.cards.cards())
	}
	if _, still := g.find(id); still {
		g.t.Errorf("commitment %s is still open after its %s card, want it closed", id, outcome)
	}
	if c.Commitment != id {
		g.t.Errorf("card %s names commitment %q, want %q", c.ID, c.Commitment, id)
	}
	return c
}

// wantWaiting fails if the commitment fired, dropped or posted anything.
func (g *rig) wantWaiting(id string) {
	g.t.Helper()
	if _, ok := g.find(id); !ok {
		g.t.Errorf("commitment %s is closed, want it still waiting", id)
	}
	if n := len(g.cards.cards()); n != 0 {
		g.t.Errorf("%d cards %+v, want none while the commitment waits", n, g.cards.cards())
	}
}

// startRun runs the runner until the test ends.
func (g *rig) startRun() {
	g.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.r.Run(ctx); close(done) }()
	g.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			g.t.Error("Run did not return within 2s of its context ending")
		}
	})
}

// waitFor polls for two seconds, so a runner that does nothing fails here rather than hanging.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("within 2s: %s", what)
		}
	}
}

func refusal(status int, msg, wait string) *launcher.Error {
	return &launcher.Error{Status: status, Msg: msg, Wait: wait}
}

var errCrash = errors.New("process died")
