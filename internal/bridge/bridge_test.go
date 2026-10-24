package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

type oneShip struct{}

func (oneShip) Get(session string) (ships.Ship, bool) {
	return ships.Ship{Session: session, Status: ships.StatusUnderway}, session == "ship"
}
func (oneShip) InputSeen(string) (ships.Ship, error)   { return ships.Ship{}, nil }
func (oneShip) Crew(string) ([]sessionhost.Crew, bool) { return nil, false }
func (oneShip) CrewInputSeen(string, string) (sessionhost.Crew, error) {
	return sessionhost.Crew{}, ships.ErrNotFound
}

// A control client that fails to attach must not leave a linger timer behind to
// close a client that was never there: the server has to outlive the idle time.
func TestFailedAttachDoesNotCrashAfterTheIdleLinger(t *testing.T) {
	s := New("/nonexistent/tmux", t.TempDir()+"/sock", oneShip{})
	s.Idle = 50 * time.Millisecond
	mux := http.NewServeMux()
	s.Routes(mux)
	mux.HandleFunc("GET /ping", func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusNoContent) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):]+"/ws/bridge/ship", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"join","cols":80,"rows":24}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("a ship whose control client could not attach sent a message instead of closing")
	}

	time.Sleep(300 * time.Millisecond) // well past the linger
	res, err := http.Get(srv.URL + "/ping")
	if err != nil {
		t.Fatalf("the server stopped answering after a failed attach: %v", err)
	}
	res.Body.Close()
	s.mu.Lock()
	n := len(s.conns)
	s.mu.Unlock()
	if n != 0 {
		t.Errorf("%d control clients remain after a failed attach, want 0", n)
	}
}
