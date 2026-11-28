package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/server"
)

// clientProbe hands the test each Client as it connects.
type clientProbe struct{ got chan *server.Client }

func (clientProbe) Routes(*http.ServeMux)        {}
func (p clientProbe) Connected(c *server.Client) { p.got <- c }

type followServer struct {
	t     *testing.T
	hub   *server.Hub
	url   string
	probe clientProbe
}

func newFollowServer(t *testing.T) *followServer {
	t.Helper()
	f := &followServer{t: t, hub: server.NewHub(), probe: clientProbe{make(chan *server.Client, 8)}}
	srv := httptest.NewServer((&server.Server{
		Hub:     f.hub,
		Bearer:  func(r *http.Request) bool { return r.Header.Get("Authorization") != "" },
		Modules: []server.Module{f.probe},
	}).Handler())
	t.Cleanup(srv.Close)
	f.url = "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/v1"
	return f
}

func (f *followServer) dial(bearer bool) (*websocket.Conn, *server.Client) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{}
	if bearer {
		opts.HTTPHeader = http.Header{"Authorization": {"Bearer fake-token-1"}}
	}
	conn, _, err := websocket.Dial(ctx, f.url, opts)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = conn.CloseNow() })
	select {
	case c := <-f.probe.got:
		return conn, c
	case <-ctx.Done():
		f.t.Fatal("the server never registered the client")
		return nil, nil
	}
}

func read(t *testing.T, conn *websocket.Conn, wait time.Duration) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return "", err
		}
		var m struct{ Type string }
		_ = json.Unmarshal(data, &m)
		if m.Type != "hello" {
			return m.Type, nil
		}
	}
}

func TestClientKnowsWhetherItsSocketCameInWithABearerToken(t *testing.T) {
	f := newFollowServer(t)
	_, withToken := f.dial(true)
	_, without := f.dial(false)
	if !withToken.Bearer() {
		t.Error("a socket the server's Bearer check accepted is not marked bearer")
	}
	if without.Bearer() {
		t.Error("a socket without a bearer token is marked bearer")
	}
}

func TestAFollowingClientGetsNoWindowBroadcast(t *testing.T) {
	f := newFollowServer(t)
	followerConn, follower := f.dial(true)
	windowConn, _ := f.dial(false)
	follower.SetFollowing(true)

	f.hub.BroadcastOnceWindows("attention", func() any { return map[string]any{"type": "attention", "count": 1} })
	if got, err := read(t, windowConn, 2*time.Second); err != nil || got != "attention" {
		t.Fatalf("the window got %q, %v, want attention", got, err)
	}
	if got, err := read(t, followerConn, 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the follower got %q, %v, want nothing", got, err)
	}
}

func TestFollowingDoesNotMakeASocketWatchingButPresenceStillDoes(t *testing.T) {
	f := newFollowServer(t)
	conn, c := f.dial(true)
	c.SetFollowing(true)
	if f.hub.Watching() {
		t.Fatal("a socket that only follows counts as watching")
	}
	b, _ := json.Marshal(map[string]any{"type": "presence", "visible": true, "idle_ms": 0})
	if err := conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !f.hub.Watching() {
		if time.Now().After(deadline) {
			t.Fatal("a following bearer socket that sent presence never counted as watching")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.hub.SendWatching(map[string]any{"type": "alert"})
	if got, err := read(t, conn, 2*time.Second); err != nil || got != "alert" {
		t.Errorf("got %q, %v, want alert", got, err)
	}
}

func TestClosingAClientEndsItsSocket(t *testing.T) {
	f := newFollowServer(t)
	conn, c := f.dial(true)
	c.Close()
	if _, err := read(t, conn, 2*time.Second); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the socket stayed open, read gave %v", err)
	}
}
