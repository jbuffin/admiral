package attention_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/attention"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

type clientProbe struct{ got chan *server.Client }

func (clientProbe) Routes(*http.ServeMux)        {}
func (p clientProbe) Connected(c *server.Client) { p.got <- c }

func firstType(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m struct{ Type string }
		_ = json.Unmarshal(data, &m)
		if m.Type != "hello" {
			return m.Type
		}
	}
}

func TestAChangeOfTheCountReachesWindowsButNotClientsThatFollowPushEvents(t *testing.T) {
	hub := server.NewHub()
	svc := &attention.Service{Hub: hub, Ships: func() []ships.Ship { return []ships.Ship{needsYou("a")} }, Items: func() []inbox.Item { return nil }}
	probe := clientProbe{make(chan *server.Client, 4)}
	srv := httptest.NewServer((&server.Server{Hub: hub, Modules: []server.Module{svc, probe}}).Handler())
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/v1"

	dial := func() (*websocket.Conn, *server.Client) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		return conn, <-probe.got
	}
	windowConn, _ := dial()
	followerConn, follower := dial()
	// The count a client gets on connecting is today's; it has been sent before the client follows.
	for _, c := range []*websocket.Conn{windowConn, followerConn} {
		if got := firstType(t, c); got != "attention" {
			t.Fatalf("on connect got %q, want attention", got)
		}
	}
	follower.SetFollowing(true)

	svc.Items = func() []inbox.Item { return []inbox.Item{question("1")} }
	svc.Changed()
	if got := firstType(t, windowConn); got != "attention" {
		t.Errorf("the window got %q, want the new count", got)
	}
	// A direct send arrives after anything Changed wrote, so the marker comes first only if the follower got no count.
	hub.SendTo(follower.ID(), map[string]any{"type": "marker"})
	if got := firstType(t, followerConn); got != "marker" {
		t.Errorf("the follower got %q first, want only what the push follow carries", got)
	}
}
