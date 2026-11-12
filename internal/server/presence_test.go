package server_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/server"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type presenceRig struct {
	t     *testing.T
	hub   *server.Hub
	clock *fakeClock
	url   string
}

func newPresenceRig(t *testing.T) *presenceRig {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	hub := server.NewHub()
	hub.Now = clock.Now
	srv := httptest.NewServer((&server.Server{Hub: hub}).Handler())
	t.Cleanup(srv.Close)
	return &presenceRig{t: t, hub: hub, clock: clock, url: "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/v1"}
}

func (r *presenceRig) dial() *websocket.Conn {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, r.url, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// say sends a presence message and waits until the hub has seen it: messages from one client are handled in order,
// so the echo of a later hello-less ping is not available; the hub's own answer is.
func (r *presenceRig) say(conn *websocket.Conn, visible bool, idleMS int) {
	r.t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "presence", "visible": visible, "idle_ms": idleMS})
	if err := conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		r.t.Fatal(err)
	}
}

func (r *presenceRig) waitWatching(want bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.hub.Watching() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("hub.Watching() = %v after 5s, want %v", r.hub.Watching(), want)
}

func (r *presenceRig) holdWatching(want bool) {
	r.t.Helper()
	for range 20 {
		if r.hub.Watching() != want {
			r.t.Fatalf("hub.Watching() = %v, want it to stay %v", r.hub.Watching(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPresence_AVisibleRecentlyUsedWindowIsWatching(t *testing.T) {
	r := newPresenceRig(t)
	if r.hub.Watching() {
		t.Fatal("a hub with no window is watching")
	}
	conn := r.dial()
	r.holdWatching(false) // connected, but it has said nothing
	r.say(conn, true, 500)
	r.waitWatching(true)
}

func TestPresence_AHiddenWindowIsNotWatching(t *testing.T) {
	r := newPresenceRig(t)
	conn := r.dial()
	r.say(conn, true, 0)
	r.waitWatching(true)
	r.say(conn, false, 0)
	r.waitWatching(false)
}

func TestPresence_AWindowIdleForTwoMinutesStopsWatchingUntilItIsUsedAgain(t *testing.T) {
	r := newPresenceRig(t)
	conn := r.dial()
	r.say(conn, true, 0)
	r.waitWatching(true)

	r.clock.advance(100 * time.Second)
	if !r.hub.Watching() {
		t.Error("a window used 100 s ago stopped watching, want it to count until about 2 minutes")
	}
	r.clock.advance(30 * time.Second)
	if r.hub.Watching() {
		t.Error("a window idle for 130 s still counts as watching")
	}

	r.say(conn, true, 0) // the heartbeat, after the person touched the window
	r.waitWatching(true)
}

func TestPresence_AnIdleWindowReportedAtTheStartIsNotWatching(t *testing.T) {
	r := newPresenceRig(t)
	conn := r.dial()
	r.say(conn, true, int((5 * time.Minute).Milliseconds()))
	r.holdWatching(false)
}

func TestPresence_AnAbsurdIdleTimeIsIdleNotRecent(t *testing.T) {
	r := newPresenceRig(t)
	conn := r.dial()
	r.say(conn, true, 9_000_000_000_000_000)
	r.holdWatching(false)
}

func TestPresence_AClosedSocketStopsCountingAtOnce(t *testing.T) {
	r := newPresenceRig(t)
	conn := r.dial()
	r.say(conn, true, 0)
	r.waitWatching(true)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	r.waitWatching(false)
}

func TestPresence_OneWatchingWindowIsEnoughWhateverTheOthersSay(t *testing.T) {
	r := newPresenceRig(t)
	away, here := r.dial(), r.dial()
	r.say(away, false, 0)
	r.say(here, true, 0)
	r.waitWatching(true)
	_ = here.Close(websocket.StatusNormalClosure, "")
	r.waitWatching(false)
}
