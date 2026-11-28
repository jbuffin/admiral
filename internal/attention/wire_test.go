package attention_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/attention"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

// rig serves /ws/v1 with the attention module over lists the test changes.
type rig struct {
	t     *testing.T
	mu    sync.Mutex
	ships []ships.Ship
	items []inbox.Item
	svc   *attention.Service
	url   string
	// joined gets a value once a new client has been through every module's Connected.
	joined chan struct{}
}

func newRig(t *testing.T, sh []ships.Ship, items []inbox.Item) *rig {
	t.Helper()
	r := &rig{t: t, ships: sh, items: items, joined: make(chan struct{}, 16)}
	hub := server.NewHub()
	r.svc = &attention.Service{
		Hub: hub,
		Ships: func() []ships.Ship {
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]ships.Ship(nil), r.ships...)
		},
		Items: func() []inbox.Item {
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]inbox.Item(nil), r.items...)
		},
	}
	srv := httptest.NewServer((&server.Server{Hub: hub, Modules: []server.Module{r.svc, joinProbe{r.joined}}}).Handler())
	t.Cleanup(srv.Close)
	r.url = "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/v1"
	return r
}

type joinProbe struct{ joined chan struct{} }

func (joinProbe) Routes(*http.ServeMux)      {}
func (p joinProbe) Connected(*server.Client) { p.joined <- struct{}{} }

func (r *rig) setShips(sh []ships.Ship) {
	r.mu.Lock()
	r.ships = sh
	r.mu.Unlock()
	r.svc.Changed()
}

func (r *rig) setItems(items []inbox.Item) {
	r.mu.Lock()
	r.items = items
	r.mu.Unlock()
	r.svc.Changed()
}

func (r *rig) dial() *websocket.Conn {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, r.url, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = conn.CloseNow() })
	select {
	case <-r.joined:
	case <-ctx.Done():
		r.t.Fatal("the client never finished connecting")
	}
	return conn
}

// next is the count of the next attention message, skipping other message types.
func next(t *testing.T, conn *websocket.Conn) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("no attention message: %v", err)
		}
		var m struct {
			Type  string `json:"type"`
			Count *int   `json:"count"`
		}
		if json.Unmarshal(data, &m) != nil || m.Type != "attention" {
			continue
		}
		if m.Count == nil {
			t.Fatalf("attention message %s has no count", data)
		}
		return *m.Count
	}
}

func needsYou(session string) ships.Ship {
	return ships.Ship{Session: session, Status: "needs_you", Reason: "idle"}
}

func question(id string) inbox.Item {
	return inbox.Item{Session: "q", ID: id, Kind: inbox.KindQuestion}
}

func TestConnectSendsTheCount(t *testing.T) {
	notice := inbox.Item{Session: inbox.AdmiralSession, ID: "n", Kind: inbox.KindNotice}
	r := newRig(t, []ships.Ship{needsYou("a")}, []inbox.Item{question("1"), notice})
	if got := next(t, r.dial()); got != 2 {
		t.Errorf("count on connect = %d, want 2 (a stop and a question; the notice is FYI)", got)
	}
}

func TestAnsweringOrRemovingAnItemSendsTheLowerCount(t *testing.T) {
	r := newRig(t, []ships.Ship{needsYou("a")}, []inbox.Item{question("1")})
	conn := r.dial()
	if got := next(t, conn); got != 2 {
		t.Fatalf("count on connect = %d, want 2", got)
	}
	r.setItems(nil)
	if got := next(t, conn); got != 1 {
		t.Errorf("count after the item went = %d, want 1", got)
	}
}

func TestViewingAStopSendsNoNewCount(t *testing.T) {
	r := newRig(t, []ships.Ship{needsYou("a")}, []inbox.Item{question("1")})
	conn := r.dial()
	if got := next(t, conn); got != 2 {
		t.Fatalf("count on connect = %d, want 2", got)
	}
	seen := needsYou("a")
	seen.Seen = true
	r.setShips([]ships.Ship{seen})
	// The next change that does move the count must be the next attention message.
	r.setItems(nil)
	if got := next(t, conn); got != 1 {
		t.Errorf("next count = %d, want 1: viewing the stop must send nothing before it", got)
	}
}

func TestAChangeThatLeavesTheCountSendsNothing(t *testing.T) {
	r := newRig(t, []ships.Ship{needsYou("a")}, []inbox.Item{question("1")})
	conn := r.dial()
	if got := next(t, conn); got != 2 {
		t.Fatalf("count on connect = %d, want 2", got)
	}
	// One item swapped for another and a quiet ship added: still 2.
	r.setItems([]inbox.Item{question("2")})
	r.setShips([]ships.Ship{needsYou("a"), {Session: "b", Status: "underway"}})
	r.setShips(nil)
	if got := next(t, conn); got != 1 {
		t.Errorf("next count = %d, want 1: an unchanged count must send nothing before it", got)
	}
}

func TestEveryConnectedClientGetsTheNewCount(t *testing.T) {
	r := newRig(t, nil, nil)
	a, b := r.dial(), r.dial()
	if next(t, a) != 0 || next(t, b) != 0 {
		t.Fatal("want count 0 on connect")
	}
	r.setShips([]ships.Ship{needsYou("x")})
	if got := next(t, a); got != 1 {
		t.Errorf("first client count = %d, want 1", got)
	}
	if got := next(t, b); got != 1 {
		t.Errorf("second client count = %d, want 1", got)
	}
}

func TestTheCountSpansEveryFleet(t *testing.T) {
	one, two := needsYou("a"), needsYou("b")
	one.Fleet, two.Fleet = "alpha", "beta"
	r := newRig(t, []ships.Ship{one, two}, nil)
	if got := next(t, r.dial()); got != 2 {
		t.Errorf("count on connect = %d, want 2 (one stop in each fleet)", got)
	}
}

func TestNoCountIsSentUntilTheListsAreKnown(t *testing.T) {
	r := newRig(t, nil, nil)
	known := false
	r.svc.Ready = func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return known
	}
	conn := r.dial()
	r.setShips([]ships.Ship{needsYou("a")}) // a change before the lists are known sends nothing
	r.mu.Lock()
	known = true
	r.ships = []ships.Ship{needsYou("a"), needsYou("b")}
	r.mu.Unlock()
	r.svc.Changed()
	if got := next(t, conn); got != 2 {
		t.Errorf("first count = %d, want 2 (the count once known, not an earlier one)", got)
	}
}
