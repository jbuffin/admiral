package shipconv

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	loaded   []string     // the threads the server has loaded
	resumeOf []string     // the thread of each thread/resume, in order
}

func newLiveFake(t *testing.T) *liveFake {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cxs") // short: a socket path is capped at 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return newLiveFakeIn(t, dir, "s.sock")
}

// newLiveFakeIn is another server in a directory that already holds one, as Codex keeps every socket in one.
func newLiveFakeIn(t *testing.T, dir, name string) *liveFake {
	t.Helper()
	f := &liveFake{t: t, dir: dir, sock: filepath.Join(dir, name), loaded: []string{liveThread}}
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
			Params struct {
				ThreadID string `json:"threadId"`
			} `json:"params"`
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
			f.mu.Lock()
			ids, _ := json.Marshal(f.loaded)
			f.mu.Unlock()
			result = `{"data":` + string(ids) + `,"nextCursor":null}`
		case "thread/list":
			f.mu.Lock()
			var rows []string
			for i, id := range f.loaded {
				rows = append(rows, fmt.Sprintf(`{"id":%q,"updatedAt":%d}`, id, 100+i))
			}
			f.mu.Unlock()
			result = `{"data":[` + strings.Join(rows, ",") + `],"nextCursor":null}`
		case "thread/resume":
			id := m.Params.ThreadID
			f.mu.Lock()
			f.resumed++
			f.resumeOf = append(f.resumeOf, id)
			f.mu.Unlock()
			text := "hello from the server"
			if id != liveThread {
				text = "hello from " + id
			}
			result = `{"thread":{"id":"` + id + `","turns":[{"id":"t1","items":[{"type":"userMessage","id":"um-1","content":[{"type":"text","text":"` + text + `"}]}]}]}}`
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

func (f *liveFake) setLoaded(ids ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loaded = ids
}

func (f *liveFake) resumedThreads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resumeOf...)
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
	f.svc.liveReadEvery = 100 * time.Millisecond
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

const otherThread = "00000000-0000-4000-8000-0000000000b2"

func startedFrame(id, extra string) string {
	return `{"method":"thread/started","params":{"thread":{"id":"` + id + `","ephemeral":false,"source":"cli"` + extra + `}}}`
}

func TestLiveFollowsTheThreadTheTUIStartsOnTheSameServer(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	f.startLive()
	f.serving(fake)
	base := serve(t, f)
	v := dial(t, base)
	v.send("conversation_open", "s")
	v.opened("s")

	// A sub-agent's thread and an ephemeral one run beside the TUI's: the view stays.
	fake.push(startedFrame("00000000-0000-4000-8000-0000000000d1", `,"source":{"subAgent":"review"}`))
	fake.push(`{"method":"thread/started","params":{"thread":{"id":"00000000-0000-4000-8000-0000000000d2","ephemeral":true,"source":"cli"}}}`)
	v.quiet("a sub-agent's or an ephemeral thread")
	if n := fake.connections(); n != 1 {
		t.Errorf("%d connections, want still the one: no switch for those", n)
	}

	// /new: the server loads another thread and says so.
	fake.setLoaded(liveThread, otherThread)
	fake.push(startedFrame(otherThread, ""))
	m := v.next("the new thread's conversation")
	if m["reset"] != true || len(texts(m)) != 1 || texts(m)[0] != "hello from "+otherThread {
		t.Errorf("after /new = %v, want reset: true with the new thread's history", m)
	}
	if got := fake.resumedThreads(); len(got) != 2 || got[1] != otherThread {
		t.Errorf("resumed %v, want the first thread, then the new one", got)
	}
	fake.push(`{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg-n","text":"on the new thread","phase":"final_answer"},"threadId":"` + otherThread + `","turnId":"t2"}}`)
	if got := texts(v.next("the new thread's message")); len(got) != 1 || got[0] != "on the new thread" {
		t.Errorf("got %v, want the new thread's message pushed", got)
	}
	eventually(t, "GET to answer from the new thread", func() bool {
		code, p, _ := f.get("s", "")
		return code == 200 && len(p.Items) >= 1 && p.Items[0].Text == "hello from "+otherThread
	})
}

var rolloutID = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

func TestLiveFollowsTheRolloutTheShipRecordsNext(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, true) // a hook-less server picks the only loaded thread; this ship's hooks record a rollout
	f.startLive()
	f.serving(fake)
	base := serve(t, f)
	v := dial(t, base)
	v.send("conversation_open", "s")
	v.opened("s")

	next := f.codexRollout("after /new")
	appendTo(t, next, codexLines(1, 1))
	id := rolloutID.FindStringSubmatch(next)[1]
	fake.setLoaded(liveThread, id)
	f.setOption("s", "SessionStart", next)
	m := v.next("the new rollout's thread")
	if m["reset"] != true || len(texts(m)) != 1 || texts(m)[0] != "hello from "+id {
		t.Errorf("after the recorded rollout changed = %v, want reset: true with that thread's history", m)
	}
}

func TestLiveRestartsOnAnotherSocketWhenTheShipsAnswerChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	second := newLiveFakeIn(t, fake.dir, "s2.sock")
	f.startLive()
	f.serving(fake)

	f.mu.Lock()
	f.starts["s"] = "'/bin/admiral' 'codex-pane' '--socket' '" + second.sock + "' '--' '/bin/codex' '--fa-codex'"
	f.mu.Unlock()
	f.svc.liveSocket = func(ships.Ship) (string, string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return second.sock, f.starts["s"]
	}
	eventually(t, "the old connection to close and the new socket to be dialled", func() bool {
		fake.mu.Lock()
		closed := fake.closed
		fake.mu.Unlock()
		return closed >= 1 && second.connections() >= 1 && second.resumes() >= 1
	})
}

func TestLiveServerGoingAwayWithNoRolloutEmptiesTheView(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	f.startLive()
	f.serving(fake)
	base := serve(t, f)
	v := dial(t, base)
	v.send("conversation_open", "s")
	v.opened("s")

	fake.down()
	m := v.next("the view, after the server went away")
	if items, _ := m["items"].([]any); m["reset"] != true || len(items) != 0 {
		t.Errorf("fallback = %v, want reset: true with no items", m)
	}
}

func TestLiveQuestionPendingOnTheServerTellsTheShipItAsksAndSettles(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	var mu sync.Mutex
	var told []bool
	f.svc.Asking = func(session string, asking bool) {
		mu.Lock()
		defer mu.Unlock()
		told = append(told, asking)
	}
	f.startLive()
	f.serving(fake)
	got := func() []bool {
		mu.Lock()
		defer mu.Unlock()
		return append([]bool(nil), told...)
	}

	// The TUI rings no bell for this question: the pending request is all there is.
	fake.push(`{"method":"item/tool/requestUserInput","id":0,"params":{"threadId":"` + liveThread + `","turnId":"t1","itemId":"call_q1","questions":[{"id":"colour","header":"Colour","question":"Pick a colour","isSecret":false,"options":[{"label":"Red","description":""},{"label":"Blue","description":""}]}]}}`)
	eventually(t, "the ship to be told it asks", func() bool { g := got(); return len(g) == 1 && g[0] })
	fake.push(`{"method":"serverRequest/resolved","params":{"threadId":"` + liveThread + `","requestId":0}}`)
	eventually(t, "the ship to be told it does not", func() bool { g := got(); return len(g) == 2 && !g[1] })
}

// Every frame an observer gets also reaches the live usage sink, whoever is watching.
func TestLiveFramesReachTheUsageSink(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake := newLiveFake(t)
	f.liveShip(fake, false)
	var mu sync.Mutex
	got := map[string][]string{}
	f.svc.LiveFrame = func(session string, frame []byte) {
		mu.Lock()
		defer mu.Unlock()
		got[session] = append(got[session], string(frame))
	}
	f.startLive()
	f.serving(fake)

	usageFrame := `{"method":"thread/tokenUsage/updated","params":{"threadId":"` + liveThread + `","turnId":"t1","tokenUsage":{"total":{"totalTokens":10,"inputTokens":8,"cachedInputTokens":0,"outputTokens":2,"reasoningOutputTokens":0},"modelContextWindow":1000}}}`
	limitsFrame := `{"method":"account/rateLimits/updated","params":{"rateLimits":{"primary":{"usedPercent":1,"windowDurationMins":300,"resetsAt":1900000000},"secondary":null}}}`
	fake.push(usageFrame)
	fake.push(limitsFrame)
	eventually(t, "both frames at the sink, under the ship's name", func() bool {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, fr := range got["s"] {
			if fr == usageFrame || fr == limitsFrame {
				n++
			}
		}
		return n == 2
	})
}
