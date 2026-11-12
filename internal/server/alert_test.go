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

type alertClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *alertClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type alertRig struct {
	t   *testing.T
	hub *server.Hub
	url string
}

func newAlertRig(t *testing.T) *alertRig {
	t.Helper()
	clock := &alertClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	hub := server.NewHub()
	hub.Now = clock.Now
	srv := httptest.NewServer((&server.Server{Hub: hub}).Handler())
	t.Cleanup(srv.Close)
	return &alertRig{t: t, hub: hub, url: "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/v1"}
}

func (r *alertRig) dial() *websocket.Conn {
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

func (r *alertRig) report(conn *websocket.Conn, visible bool, idle time.Duration) {
	r.t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "presence", "visible": visible, "idle_ms": idle.Milliseconds()})
	if err := conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		r.t.Fatal(err)
	}
}

// settle waits until the hub has handled the report: the hub counts the window as watching or not, as asked.
func (r *alertRig) settle(want bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.hub.Watching() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("hub.Watching() did not become %v", want)
}

// next is the type of the next message the client receives.
func next(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ Type string }
	_ = json.Unmarshal(data, &m)
	return m.Type
}

func TestSendWatching_ReachesAWatchedWindowAndNoOtherClient(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *alertRig, conn *websocket.Conn)
		want  string
	}{
		{"visible and recently used", func(r *alertRig, c *websocket.Conn) { r.report(c, true, time.Second); r.settle(true) }, "alert"},
		{"hidden", func(r *alertRig, c *websocket.Conn) {
			r.report(c, true, 0)
			r.settle(true)
			r.report(c, false, 0)
			r.settle(false)
		}, "after"},
		{"idle for minutes", func(r *alertRig, c *websocket.Conn) {
			r.report(c, true, 0)
			r.settle(true)
			r.report(c, true, 5*time.Minute)
			r.settle(false)
		}, "after"},
		{"never sent presence", func(r *alertRig, c *websocket.Conn) {}, "after"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newAlertRig(t)
			conn := r.dial()
			tc.setup(r, conn)
			r.hub.SendWatching(map[string]any{"type": "alert", "name": "Dory"})
			// Messages to one client arrive in order, so an alert that was sent comes before this one.
			r.hub.Broadcast(map[string]any{"type": "after"})
			if got := next(t, conn); got != tc.want {
				t.Errorf("first message = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSendWatching_OnlyTheWatchedOfTwoWindowsHearsIt(t *testing.T) {
	r := newAlertRig(t)
	away, here := r.dial(), r.dial()
	r.report(away, false, 0)
	r.report(here, true, 0)
	r.settle(true)
	r.hub.SendWatching(map[string]any{"type": "alert"})
	r.hub.Broadcast(map[string]any{"type": "after"})
	if got := next(t, here); got != "alert" {
		t.Errorf("the watched window got %q first, want alert", got)
	}
	if got := next(t, away); got != "after" {
		t.Errorf("the hidden window got %q first, want only the later message", got)
	}
}
