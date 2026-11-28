package push_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

type followClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *followClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *followClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type followRig struct {
	t        *testing.T
	hub      *server.Hub
	clock    *followClock
	f        *push.Followers
	w        *push.Watcher
	url      string
	ready    atomic.Bool
	watching atomic.Bool
	notifyOn sync.Map // fleet -> false when Notify is off
	joined   chan struct{}
	handled  chan struct{} // one per push_follow the server has finished handling
}

// joinProbe signals each socket the server has registered.
type joinProbe struct{ ch chan struct{} }

func (joinProbe) Routes(*http.ServeMux)      {}
func (p joinProbe) Connected(*server.Client) { p.ch <- struct{}{} }

type followOpts struct {
	hubWatching bool
	queue       int
	send        func(*server.Client, []byte) error
	record      push.FollowRecorder
}

// newFollowRig serves /ws/v1 with an Authorization header meaning bearer, behind a Watcher feeding the Followers and the hub.
func newFollowRig(t *testing.T, o ...followOpts) *followRig {
	t.Helper()
	r := &followRig{t: t, joined: make(chan struct{}, 64), handled: make(chan struct{}, 64), hub: server.NewHub(), clock: &followClock{now: time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)}}
	r.ready.Store(true)
	cfg := push.FollowersConfig{Now: r.clock.Now, Ready: r.ready.Load}
	if len(o) > 0 {
		cfg.Queue, cfg.Send, cfg.Record = o[0].queue, o[0].send, o[0].record
	}
	r.f = push.NewFollowers(cfg)
	watching := r.watching.Load
	if len(o) > 0 && o[0].hubWatching {
		watching = r.hub.Watching
	}
	r.w = push.NewWatcher(watching, func(fleet string) bool {
		v, ok := r.notifyOn.Load(fleet)
		return !ok || v.(bool)
	}, push.NewAlertSink(r.hub), r.f)
	srv := httptest.NewServer((&server.Server{
		Hub:     r.hub,
		Modules: []server.Module{joinProbe{r.joined}},
		Bearer:  func(req *http.Request) bool { return req.Header.Get("Authorization") != "" },
		Device:  func(req *http.Request) string { return req.Header.Get("X-Device") },
		Messages: map[string]server.MessageHandler{
			"push_follow": func(ctx context.Context, c *server.Client, raw json.RawMessage) {
				r.f.Handle(ctx, c, raw)
				select {
				case r.handled <- struct{}{}:
				default:
				}
			},
			"push_unfollow": func(ctx context.Context, c *server.Client, raw json.RawMessage) {
				r.f.HandleUnfollow(ctx, c, raw)
				select {
				case r.handled <- struct{}{}:
				default:
				}
			},
		},
	}).Handler())
	t.Cleanup(srv.Close)
	r.url = "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/v1"
	r.w.Ships([]ships.Ship{working("Dory"), working("Nemo")})
	r.w.Inbox(nil)
	return r
}

func (r *followRig) dial(bearer bool) *websocket.Conn { return r.dialAs("", bearer) }

// dialAs connects as the paired device, with a bearer token or, when bearer is false, only its cookie.
func (r *followRig) dialAs(device string, bearer bool) *websocket.Conn {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{HTTPHeader: map[string][]string{}}
	if bearer {
		opts.HTTPHeader["Authorization"] = []string{"Bearer fake-token-1"}
	}
	if device != "" {
		opts.HTTPHeader["X-Device"] = []string{device}
	}
	conn, _, err := websocket.Dial(ctx, r.url, opts)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = conn.CloseNow() })
	select {
	case <-r.joined:
	case <-ctx.Done():
		r.t.Fatal("the server never registered the socket")
	}
	return conn
}

func (r *followRig) write(conn *websocket.Conn, v map[string]any) {
	r.t.Helper()
	b, _ := json.Marshal(v)
	if err := conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		r.t.Fatal(err)
	}
}

// follow sends push_follow; a nil after is JSON null, and an empty boot leaves the field out.
func (r *followRig) follow(conn *websocket.Conn, after any, boot string) {
	r.t.Helper()
	m := map[string]any{"type": "push_follow", "after": after}
	if boot != "" {
		m["boot"] = boot
	}
	r.write(conn, m)
}

type msg map[string]any

func (m msg) typ() string { s, _ := m["type"].(string); return s }
func (m msg) num(k string) int {
	f, _ := m[k].(float64)
	return int(f)
}

// next is the next message but the greeting, failing when none comes soon.
func (r *followRig) next(conn *websocket.Conn) msg {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			r.t.Fatalf("waiting for a message: %v", err)
		}
		var m msg
		if json.Unmarshal(data, &m) != nil {
			r.t.Fatalf("not JSON: %s", data)
		}
		if m.typ() != "hello" {
			return m
		}
	}
}

func (r *followRig) wantNext(conn *websocket.Conn, typ string) msg {
	r.t.Helper()
	m := r.next(conn)
	if m.typ() != typ {
		r.t.Fatalf("got %v, want a %q message", m, typ)
	}
	return m
}

// quiet fails when a message comes within the window. It ends the connection, so it is a test's last read.
func (r *followRig) quiet(conn *websocket.Conn) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err == nil {
		r.t.Fatalf("got %s, want nothing", data)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		r.t.Fatalf("the socket ended: %v", err)
	}
}

// follower is a bearer socket that has followed with after null and read its first count.
func (r *followRig) follower() *websocket.Conn {
	r.t.Helper()
	conn := r.dial(true)
	r.follow(conn, nil, "")
	r.wantNext(conn, "attention")
	return conn
}

// bootOf asks for a catch-up from a cursor that cannot be inside any ring and reads the boot id off the reset.
func (r *followRig) bootOf() string {
	r.t.Helper()
	conn := r.dial(true)
	r.follow(conn, 0, "no-such-boot")
	m := r.wantNext(conn, "push_reset")
	boot, _ := m["boot"].(string)
	if boot == "" {
		r.t.Fatalf("push_reset %v has no boot", m)
	}
	return boot
}

func (r *followRig) ask(name string) {
	r.w.Ships([]ships.Ship{needsYou(name, "question"), working(otherOf(name))})
}

func otherOf(name string) string {
	if name == "Dory" {
		return "Nemo"
	}
	return "Dory"
}

func askEvent(i int) push.Event {
	key := "s-q" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
	return push.Event{Kind: push.ShipQuestion, Fleet: "work", FleetName: "Work", Key: key, Name: "Q", Text: "has a question", Target: push.TargetPeek, Session: key}
}

func TestFollowRefusedOnASocketWithoutABearerToken(t *testing.T) {
	r := newFollowRig(t)
	conn := r.dial(false)
	r.follow(conn, nil, "")
	m := r.wantNext(conn, "push_refused")
	if m["reason"] == "" || m["reason"] == nil {
		t.Errorf("refusal %v names no reason", m)
	}
	r.ask("Dory")
	r.quiet(conn)
}

func TestFollowWithNullAfterGetsTheCountAndNoReplay(t *testing.T) {
	r := newFollowRig(t)
	r.ask("Dory")
	r.w.Ships([]ships.Ship{needsYou("Dory", "stop"), working("Nemo")}) // moves on: a clear for the question, count 1 for the stop
	conn := r.dial(true)
	r.follow(conn, nil, "")
	m := r.wantNext(conn, "attention")
	if got := m.num("count"); got != 1 {
		t.Errorf("count %d, want 1", got)
	}
	r.quiet(conn)
}

func TestFollowAfterASeqReplaysWhatWasMissedInOrderThenSendsTheCount(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.ask("Dory")
	first := r.wantNext(live, "push")
	r.w.Ships([]ships.Ship{working("Dory"), working("Nemo")})
	clear := r.wantNext(live, "push_clear")
	r.ask("Nemo")
	last := r.wantNext(live, "push")
	boot, _ := first["boot"].(string)
	if boot == "" || clear["boot"] != boot || last["boot"] != boot {
		t.Fatalf("boot ids %v %v %v, want one non-empty id on every event", first["boot"], clear["boot"], last["boot"])
	}
	if first.num("seq") != 1 || clear.num("seq") != 2 || last.num("seq") != 3 {
		t.Fatalf("seqs %d %d %d, want 1 2 3: a push and a clear share one rising number from 1", first.num("seq"), clear.num("seq"), last.num("seq"))
	}

	conn := r.dial(true)
	r.follow(conn, 1, boot)
	if got := r.wantNext(conn, "push_clear"); got.num("seq") != 2 || got["tag"] != "s-Dory" {
		t.Errorf("first replayed %v, want the clear, seq 2", got)
	}
	if got := r.wantNext(conn, "push"); got.num("seq") != 3 {
		t.Errorf("second replayed %v, want seq 3", got)
	}
	if got := r.wantNext(conn, "attention"); got.num("count") != 1 {
		t.Errorf("closing count %v, want 1", got)
	}
	r.quiet(conn)
}

func TestFollowAfterTheLatestSeqReplaysNothingAndSendsTheCount(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.ask("Dory")
	boot, _ := r.wantNext(live, "push")["boot"].(string)
	conn := r.dial(true)
	r.follow(conn, 1, boot)
	r.wantNext(conn, "attention")
	r.quiet(conn)
}

func TestFollowGetsAResetForACursorTheRingCannotServe(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.ask("Dory")
	boot, _ := r.wantNext(live, "push")["boot"].(string)

	cases := []struct {
		name  string
		after any
		boot  string
	}{
		{"a boot from another run", 1, "boot-of-another-run"},
		{"no boot at all", 1, ""},
		{"a seq the boot never reached", 7, boot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := r.dial(true)
			r.follow(conn, tc.after, tc.boot)
			m := r.wantNext(conn, "push_reset")
			if m["boot"] != boot {
				t.Errorf("reset names boot %v, want this run's %q", m["boot"], boot)
			}
			if m.num("attention") != 1 {
				t.Errorf("reset %v, want the count 1 with it", m)
			}
			r.quiet(conn)
		})
	}
}

func TestTheRingKeepsAtMost500EventsSoOlderCursorsResetAndTheRestReplay(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	for i := range 501 {
		r.w.Emit(askEvent(i))
		r.wantNext(live, "push")
	}
	boot := r.bootOf()

	old := r.dial(true)
	r.follow(old, 0, boot)
	r.wantNext(old, "push_reset")

	edge := r.dial(true)
	r.follow(edge, 1, boot)
	for want := 2; want <= 501; want++ {
		if m := r.wantNext(edge, "push"); m.num("seq") != want {
			t.Fatalf("replayed seq %d, want %d", m.num("seq"), want)
		}
	}
	r.wantNext(edge, "attention")
}

func TestTheRingDropsEventsOlderThanADayOnItsOwnClock(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.ask("Dory")
	boot, _ := r.wantNext(live, "push")["boot"].(string)

	r.clock.advance(23 * time.Hour)
	within := r.dial(true)
	r.follow(within, 0, boot)
	r.wantNext(within, "push")

	r.clock.advance(2 * time.Hour)
	past := r.dial(true)
	r.follow(past, 0, boot)
	r.wantNext(past, "push_reset")
}

func TestEachRunHasItsOwnBootAndSeqStartsAtOne(t *testing.T) {
	a, b := newFollowRig(t), newFollowRig(t)
	if a.bootOf() == b.bootOf() {
		t.Fatal("two runs share a boot id")
	}
	live := b.follower()
	b.ask("Dory")
	if m := b.wantNext(live, "push"); m.num("seq") != 1 {
		t.Errorf("first seq of a run is %d, want 1", m.num("seq"))
	}
	// A cursor from the other run is a restart's old cursor.
	conn := b.dial(true)
	b.follow(conn, 1, a.bootOf())
	b.wantNext(conn, "push_reset")
}

func TestASecondPushFollowIsAFreshCatchUp(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.ask("Dory")
	boot, _ := r.wantNext(live, "push")["boot"].(string)
	r.follow(live, 0, boot)
	if m := r.wantNext(live, "push"); m.num("seq") != 1 {
		t.Errorf("replay %v, want seq 1 again", m)
	}
	r.wantNext(live, "attention")
}

func TestAPushCarriesTheWebPushPayloadSeqKindAndCount(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	ev := push.Event{Kind: push.ShipQuestion, Fleet: "work", FleetName: "Work", Key: "s-Dory", Name: "Dory", Text: "has a question",
		Target: push.TargetPeek, Session: "s-Dory", Item: "i-1"}
	r.w.Emit(ev)
	m := r.wantNext(live, "push")
	b, _ := json.Marshal(push.PayloadOf(ev))
	var payload map[string]any
	_ = json.Unmarshal(b, &payload)
	for k, want := range payload {
		if m[k] != want {
			t.Errorf("%s = %v, want %v as the Web Push payload has it", k, m[k], want)
		}
	}
	if m["kind"] != "ship_question" || m.num("seq") != 1 {
		t.Errorf("push %v, want kind ship_question and seq 1", m)
	}
}

func TestAPushHoldsNothingBeyondTheWebPushPayload(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.w.Emit(push.Event{Kind: push.ShipQuestion, Fleet: "work", FleetName: "Work", Key: "s-Dory", Name: "Dory", Target: push.TargetPeek, Session: "s-Dory",
		Text: "has a question\nsecond line with a private path /Users/x/.ssh"})
	m := r.wantNext(live, "push")
	allowed := map[string]bool{"type": true, "boot": true, "seq": true, "kind": true, "attention": true}
	b, _ := json.Marshal(push.PayloadOf(push.Event{}))
	var shape map[string]any
	_ = json.Unmarshal(b, &shape)
	for k := range shape {
		allowed[k] = true
	}
	for _, k := range []string{"fleet", "fleet_name", "line", "tag", "target", "session", "item", "crew", "merge", "routine", "message"} {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			t.Errorf("push carries %q, which the Web Push payload does not", k)
		}
	}
	if strings.Contains(m["line"].(string), "private") {
		t.Errorf("line %q carries text past the first line", m["line"])
	}
}

func TestNoPushReachesAFollowerForAnAlertOrAFleetWithNotifyOff(t *testing.T) {
	t.Run("alert", func(t *testing.T) {
		r := newFollowRig(t)
		live := r.follower()
		r.watching.Store(true)
		r.ask("Dory")
		if m := r.wantNext(live, "attention"); m.num("count") != 1 {
			t.Errorf("got %v, want only the count", m)
		}
		r.quiet(live)
	})
	t.Run("notify off", func(t *testing.T) {
		r := newFollowRig(t)
		live := r.follower()
		r.notifyOn.Store("work", false)
		r.ask("Dory")
		if m := r.wantNext(live, "attention"); m.num("count") != 1 {
			t.Errorf("got %v, want only the count", m)
		}
		r.quiet(live)
	})
}

func TestAFollowerThatSendsPresenceIsWatchingAndHearsTheAlertInsteadOfAPush(t *testing.T) {
	r := newFollowRig(t, followOpts{hubWatching: true})
	live := r.follower()
	r.write(live, map[string]any{"type": "presence", "visible": true, "idle_ms": 0})
	eventually(t, "the hub to count the follower as watching", r.hub.Watching)
	r.ask("Dory")
	got := map[string]bool{r.next(live).typ(): true, r.next(live).typ(): true}
	if !got["alert"] || !got["attention"] || got["push"] {
		t.Errorf("got %v, want an alert and the count, and no push", got)
	}
}

func TestAPushClearCarriesSeqTagAndCountWhenTheKeyIsAnswered(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.ask("Dory")
	r.wantNext(live, "push")
	r.w.Ships([]ships.Ship{working("Dory"), working("Nemo")})
	m := r.wantNext(live, "push_clear")
	if m.num("seq") != 2 || m["tag"] != "s-Dory" || m.num("attention") != 0 {
		t.Errorf("clear %v, want seq 2, tag s-Dory, attention 0", m)
	}
	for k := range m {
		switch k {
		case "type", "boot", "seq", "tag", "attention":
		default:
			t.Errorf("clear carries %q", k)
		}
	}
	r.quiet(live)
}

func TestAPushOnlyEventNeverClears(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.w.Emit(push.ReportReplyEvent(push.Reply{Fleet: "work", FleetName: "Work", Ship: "Dory", Result: "done", Card: "c1", Message: "m1", Text: "All done"}))
	r.wantNext(live, "push")
	r.w.Ships([]ships.Ship{working("Dory"), working("Nemo")})
	r.w.Inbox(nil)
	r.quiet(live)
}

func TestAFollowerGetsOneMessagePerChangeOfTheCount(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()

	r.ask("Dory") // a push carries the count: no attention message beside it
	if m := r.wantNext(live, "push"); m.num("attention") != 1 {
		t.Fatalf("push %v, want attention 1", m)
	}
	// A finished turn counts and never pushes: only the count moves.
	r.w.Ships([]ships.Ship{needsYou("Dory", "question"), needsYou("Nemo", "stop")})
	if m := r.wantNext(live, "attention"); m.num("count") != 2 {
		t.Fatalf("got %v, want the count 2 as the very next message", m)
	}
	// Nothing changed in the count: a snapshot says nothing.
	r.w.Ships([]ships.Ship{needsYou("Dory", "question"), needsYou("Nemo", "stop")})
	// The clear carries the count, and no attention follows it.
	r.w.Ships([]ships.Ship{working("Dory"), needsYou("Nemo", "stop")})
	if m := r.wantNext(live, "push_clear"); m.num("attention") != 1 {
		t.Fatalf("clear %v, want attention 1", m)
	}
	r.w.Ships([]ships.Ship{working("Dory"), working("Nemo")})
	if m := r.wantNext(live, "attention"); m.num("count") != 0 {
		t.Fatalf("got %v, want the count 0 as the very next message", m)
	}
}

func TestANoticeInTheInboxMovesNoCountSoAFollowerHearsNothing(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.w.Inbox([]inbox.Item{item(t, inbox.AdmiralSession, "i-1", "notice", "Heads up")})
	r.quiet(live)
}

func TestBeforeTheShipsAndInboxAreKnownAFollowerGetsNoCountNotEvenAFalseZero(t *testing.T) {
	r := newFollowRig(t)
	r.ready.Store(false)
	conn := r.dial(true)
	// A null follow answers nothing before ready, so a reset is the answer that says the follow is registered.
	r.follow(conn, 5, "no-such-boot")
	r.wantNext(conn, "push_reset")
	r.ask("Dory")
	m := r.wantNext(conn, "push")
	if _, has := m["attention"]; has {
		t.Errorf("push %v carries a count before ready", m)
	}
	r.follow(conn, 5, "no-such-boot")
	reset := r.wantNext(conn, "push_reset")
	if _, has := reset["attention"]; has {
		t.Errorf("reset %v carries a count before ready", reset)
	}
	r.ready.Store(true)
	r.w.Ships([]ships.Ship{needsYou("Dory", "question"), needsYou("Nemo", "stop")})
	if m := r.wantNext(conn, "attention"); m.num("count") != 2 {
		t.Errorf("got %v, want the count 2 once ready", m)
	}
}

func TestEventsEmittedWhileAFollowerCatchesUpAreNeitherLostNorDoubled(t *testing.T) {
	r := newFollowRig(t)
	live := r.follower()
	r.w.Emit(askEvent(0))
	boot, _ := r.wantNext(live, "push")["boot"].(string)

	const total = 200
	var wg sync.WaitGroup
	wg.Add(1)
	conn := r.dial(true)
	go func() {
		defer wg.Done()
		for i := 1; i < total; i++ {
			r.w.Emit(askEvent(i))
		}
	}()
	r.follow(conn, 1, boot)
	for want := 2; want <= total; {
		m := r.next(conn)
		switch m.typ() {
		case "push":
			if m.num("seq") != want {
				t.Fatalf("got seq %d, want %d: an event was lost or doubled", m.num("seq"), want)
			}
			want++
		case "attention":
		default:
			t.Fatalf("unexpected %v", m)
		}
	}
	wg.Wait()
}

func TestAFollowerWhoseWriteFailsIsClosedSoItReconnectsAndCatchesUp(t *testing.T) {
	r := newFollowRig(t, followOpts{send: func(*server.Client, []byte) error { return errors.New("write failed") }})
	conn := r.dial(true)
	r.follow(conn, nil, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("a follower dropped on a write error was left open")
			}
			return
		}
	}
}

func TestANullFollowBeforeReadySendsNothingUntilTheCountIsKnown(t *testing.T) {
	r := newFollowRig(t)
	r.ready.Store(false)
	conn := r.dial(true)
	r.follow(conn, nil, "")
	select {
	case <-r.handled:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never handled the follow")
	}
	// A finished turn moves the count and pushes nothing; before ready it must not reach the follower either.
	r.w.Ships([]ships.Ship{working("Dory"), needsYou("Nemo", "stop")})
	r.ready.Store(true)
	r.w.Ships([]ships.Ship{needsYou("Dory", "stop"), needsYou("Nemo", "stop")})
	if m := r.wantNext(conn, "attention"); m.num("count") != 2 {
		t.Fatalf("got %v, want the count 2 as the first message once ready", m)
	}
}

func TestAFollowerWhoseSocketClosesStopsReceivingAndOthersStillDo(t *testing.T) {
	r := newFollowRig(t)
	gone := r.follower()
	_ = gone.CloseNow()
	live := r.follower()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ask("Dory")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a delivery blocked after a follower's socket closed")
	}
	r.wantNext(live, "push")
}

func TestAFollowerThatCannotKeepUpIsClosedAndTheSinkNeverBlocks(t *testing.T) {
	stall, entered := make(chan struct{}), make(chan struct{}, 1)
	r := newFollowRig(t, followOpts{queue: 2, send: func(*server.Client, []byte) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-stall
		return nil
	}})
	t.Cleanup(func() { close(stall) })
	conn := r.dial(true)
	r.follow(conn, nil, "")
	select {
	case <-entered: // the follow is registered and its writer is stuck on the first message
	case <-time.After(5 * time.Second):
		t.Fatal("the follow never reached its writer")
	}
	emitted := make(chan struct{})
	go func() {
		defer close(emitted)
		for i := range 10 {
			r.w.Emit(askEvent(i))
		}
	}()
	select {
	case <-emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("the sink blocked on a follower that does not read")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the slow follower was left open")
			}
			return
		}
	}
}
