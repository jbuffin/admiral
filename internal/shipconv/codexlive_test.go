package shipconv

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jbuffin/admiral/internal/ships"
)

const liveThread = "00000000-0000-4000-8000-0000000000a1"

// liveFake is a Codex app-server on a Unix socket that can be dropped and brought back at the same path.
type liveFake struct {
	t        *testing.T
	dir      string
	sock     string
	mu       sync.Mutex
	accepted int
	conns    []*websocket.Conn
	srv      *http.Server
	closed   int
	resumed  int          // thread/resume answers sent: the observer is ready after the first
	running  []ships.Ship // what the server sees as running ships
}

func newLiveFake(t *testing.T) *liveFake {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cxs") // short: a socket path is capped at 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &liveFake{t: t, dir: dir, sock: filepath.Join(dir, "s.sock")}
	f.up()
	t.Cleanup(f.down)
	return f
}

func (f *liveFake) up() {
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		f.t.Fatal(err)
	}
	f.srv = &http.Server{Handler: http.HandlerFunc(f.serve)}
	go f.srv.Serve(ln)
}

func (f *liveFake) down() {
	f.srv.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		c.CloseNow()
	}
	f.conns = nil
	f.mu.Unlock()
	os.Remove(f.sock)
}

func (f *liveFake) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.accepted++
	f.conns = append(f.conns, c)
	f.mu.Unlock()
	ctx := r.Context()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			f.mu.Lock()
			f.closed++
			f.mu.Unlock()
			return
		}
		var m struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
		}
		json.Unmarshal(data, &m)
		if m.ID == nil {
			continue
		}
		var result string
		switch m.Method {
		case "initialize":
			result = `{"userAgent":"codex/0.0.0","codexHome":"/home/user/.codex","platformFamily":"unix","platformOs":"macos"}`
		case "thread/loaded/list":
			result = `{"data":["` + liveThread + `"],"nextCursor":null}`
		case "thread/resume":
			f.mu.Lock()
			f.resumed++
			f.mu.Unlock()
			result = `{"thread":{"id":"` + liveThread + `","turns":[{"id":"t1","items":[{"type":"userMessage","id":"um-1","content":[{"type":"text","text":"hello from the server"}]}]}]}}`
		}
		c.Write(ctx, websocket.MessageText, []byte(`{"id":`+string(*m.ID)+`,"result":`+result+`}`))
	}
}

func (f *liveFake) push(frame string) {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Write(context.Background(), websocket.MessageText, []byte(frame))
	}
}

func (f *liveFake) resumes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumed
}

func (f *liveFake) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accepted
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("within 10s: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// liveShip makes the fixture's Codex ship "s" a running ship whose pane started on fake's socket.
func (f *fixture) liveShip(fake *liveFake, withRollout bool) {
	f.mu.Lock()
	f.harness["s"] = "codex"
	f.starts["s"] = "'/bin/admiral' 'codex-pane' '--socket' '" + fake.sock + "' '--' '/bin/codex' '--fa-codex'"
	f.mu.Unlock()
	if withRollout {
		path := f.codexRollout("s")
		appendTo(f.t, path, codexLines(1, 1))
		f.setOption("s", "launch", path)
	}
	fake.running = []ships.Ship{{Session: "s", Harness: "codex", Launched: "L1"}}
	f.svc.liveShips = func() []ships.Ship {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.running
	}
	f.svc.liveSocket = func(ships.Ship) (string, string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return fake.sock, f.starts["s"]
	}
	f.svc.liveDir = fake.dir
	f.svc.liveTimeout = 500 * time.Millisecond
	f.svc.liveBackoff = func(int) time.Duration { return 50 * time.Millisecond }
}

// startLive starts the server's observers, once the ship's options are as the test wants them.
func (f *fixture) startLive() {
	ctx, cancel := context.WithCancel(context.Background())
	f.t.Cleanup(cancel)
	f.svc.StartLive(ctx)
}

// serving waits until the observer has read the thread from the server, so a push or a drop cannot beat the handshake.
func (f *fixture) serving(fake *liveFake) {
	f.t.Helper()
	eventually(f.t, "the observer's conversation from thread/resume", func() bool {
		code, p, _ := f.get("s", "")
		return code == 200 && p.State == "ok" && len(p.Items) >= 1 && p.Items[0].Text == "hello from the server" && fake.resumes() >= 1
	})
}

func TestLiveShipWithNoTranscriptServesItsConversationFromTheServer(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	f.startLive()
	f.serving(fake)
}

func TestLiveOneObserverServesEveryView(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	f.startLive()
	f.serving(fake)
	base := serve(t, f)
	a, b := dial(t, base), dial(t, base)
	a.send("conversation_open", "s")
	a.opened("s")
	b.send("conversation_open", "s")
	b.opened("s")

	fake.push(`{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg-1","text":"pushed once","phase":"final_answer"},"threadId":"` + liveThread + `","turnId":"t1"}}`)
	for _, v := range []*socket{a, b} {
		if got := texts(v.next("the pushed item")); len(got) != 1 || got[0] != "pushed once" {
			t.Errorf("view got %v, want [pushed once]", got)
		}
	}
	if n := fake.connections(); n != 1 {
		t.Errorf("%d connections to the app-server, want one observer for both views", n)
	}
}

func TestLivePendingQuestionIsKnownWithNoViewOpen(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	f.startLive()
	f.serving(fake)

	fake.push(`{"method":"item/tool/requestUserInput","id":0,"params":{"threadId":"` + liveThread + `","turnId":"t1","itemId":"call_q1","questions":[{"id":"colour","header":"Colour","question":"Pick a colour","isSecret":false,"options":[{"label":"Red","description":""},{"label":"Blue","description":""}]}]}}`)
	eventually(t, "the pending question", func() bool {
		q := f.svc.PendingQuestions("s", "")
		return len(q) == 1 && q[0].ID == "call_q1#0" && q[0].Question != nil && len(q[0].Question.Options) == 2
	})
	fake.push(`{"method":"serverRequest/resolved","params":{"threadId":"` + liveThread + `","requestId":0}}`)
	eventually(t, "the question to settle", func() bool { return len(f.svc.PendingQuestions("s", "")) == 0 })
}

func TestLiveServerGoingAwayFallsBackToTheRolloutAndComesBackWithReset(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, true)
	f.startLive()
	f.serving(fake)
	base := serve(t, f)
	v := dial(t, base)
	v.send("conversation_open", "s")
	v.opened("s")

	fake.down()
	m := v.next("the rollout, after the server went away")
	if m["reset"] != true || len(texts(m)) != 1 || texts(m)[0] != "message 001" {
		t.Errorf("fallback = %v, want reset: true with the rollout's [message 001]", m)
	}

	fake.up()
	m = v.next("the server's conversation, once it is back")
	if m["reset"] != true || len(texts(m)) != 1 || texts(m)[0] != "hello from the server" {
		t.Errorf("return = %v, want reset: true with the server's [hello from the server]", m)
	}
}

func TestLiveObserverStartsOnlyWhenTheOptionMatchesTheStartCommand(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, true)
	f.mu.Lock()
	f.starts["s"] = "'/bin/admiral' 'codex-pane' '--socket' '/tmp/some-other.sock' '--' '/bin/codex'"
	f.mu.Unlock()
	f.startLive()
	time.Sleep(600 * time.Millisecond)
	if n := fake.connections(); n != 0 {
		t.Errorf("%d connections, want none: the pane's command names another socket", n)
	}
	if code, p, body := f.get("s", ""); code != 200 || p.State != "ok" || len(p.Items) != 1 || p.Items[0].Text != "message 001" {
		t.Errorf("GET = %d %s, want the rollout's conversation", code, body)
	}
}

func TestLiveObserverStopsWhenTheShipGoes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	f.startLive()
	f.serving(fake)

	fake.mu.Lock()
	fake.running = nil
	fake.mu.Unlock()
	eventually(t, "the observer to disconnect", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.closed >= 1
	})
}
