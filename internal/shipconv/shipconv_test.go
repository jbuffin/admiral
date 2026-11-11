package shipconv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

const sid = "0f8fad5b-d9cb-469f-a165-70867728950e"

type itemJSON struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type pageJSON struct {
	State string     `json:"state"`
	Items []itemJSON `json:"items"`
	More  bool       `json:"more"`
}

func line(i int) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "uuid": fmt.Sprintf("u-%03d", i), "timestamp": "2026-01-02T03:04:05.000Z",
		"message": map[string]any{"role": "user", "content": fmt.Sprintf("message %03d", i)}})
	return string(b) + "\n"
}

func lines(from, to int) string {
	var sb strings.Builder
	for i := from; i <= to; i++ {
		sb.WriteString(line(i))
	}
	return sb.String()
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// fixture is a service whose ships are "claude" ships anchored at transcripts under a temporary config dir.
type fixture struct {
	t       *testing.T
	cfg     string
	svc     *Service
	options map[string]string
	harness map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cfg, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg, "projects", "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, cfg: cfg, options: map[string]string{}, harness: map[string]string{}}
	f.svc = &Service{
		ship: func(session string) (ships.Ship, bool) {
			h, ok := f.harness[session]
			return ships.Ship{Session: session, Harness: h, Launched: "L1"}, ok
		},
		option: func(sh ships.Ship) (string, string, bool) {
			raw, ok := f.options[sh.Session]
			return raw, cfg, ok
		},
	}
	return f
}

func (f *fixture) path(dir string) string { return filepath.Join(f.cfg, "projects", dir, sid+".jsonl") }

// ship adds a claude ship whose option records path as written by its launch.
func (f *fixture) ship(name, path string) {
	f.harness[name] = "claude"
	f.options[name] = "1700000000000000000 launch " + path
}

func (f *fixture) get(name, query string) (int, pageJSON, string) {
	f.t.Helper()
	mux := http.NewServeMux()
	f.svc.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships/"+name+"/conversation"+query, nil))
	body, _ := io.ReadAll(rec.Result().Body)
	var p pageJSON
	_ = json.Unmarshal(body, &p)
	return rec.Code, p, string(body)
}

func (f *fixture) wantState(name, state string) {
	f.t.Helper()
	if code, p, body := f.get(name, ""); code != 200 || p.State != state {
		f.t.Errorf("GET conversation of %s = %d %s, want 200 with state %q", name, code, body, state)
	}
}

func TestConversationEndpoint_States(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	if code, _, body := f.get("nope", ""); code != 404 {
		t.Errorf("an unknown ship = %d %s, want 404", code, body)
	}

	f.harness["codex-ship"] = "codex"
	f.options["codex-ship"] = "1700000000000000000 launch " + f.path("zz")
	f.wantState("codex-ship", "none")

	f.harness["bare"] = "claude"
	f.options["bare"] = ""
	f.wantState("bare", "none")

	f.harness["hook"] = "claude"
	f.options["hook"] = "1700000000000000000 hook " + f.path("zz")
	f.wantState("hook", "none") // only a path the launch recorded is ours

	f.ship("fresh", f.path("zz"))
	f.wantState("fresh", "none") // not written yet, never seen

	appendTo(t, f.path("zz"), `{"type":"summary","summary":"x"}`+"\n")
	if code, p, body := f.get("fresh", ""); code != 200 || p.State != "ok" || len(p.Items) != 0 || p.More {
		t.Errorf("a metadata-only file = %d %s, want ok with no items", code, body)
	}

	if err := os.Remove(f.path("zz")); err != nil {
		t.Fatal(err)
	}
	f.wantState("fresh", "gone") // it was read before

	other := "/x"
	f.ship("badpath", other)
	f.wantState("badpath", "none")
}

func TestConversationEndpoint_RefusesWhatIsNotARegularFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), sid+".jsonl")
	if err := os.WriteFile(outside, []byte(line(1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, f.path("zz")); err != nil {
		t.Fatal(err)
	}
	f.ship("linkfile", f.path("zz"))
	f.wantState("linkfile", "unreadable")

	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(f.cfg, "projects", "linkdir")); err != nil {
		t.Fatal(err)
	}
	f.ship("linkdir", f.path("linkdir"))
	f.wantState("linkdir", "unreadable")

	if err := os.Remove(f.path("zz")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.path("zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.wantState("linkfile", "unreadable") // a directory where the file should be

	f.ship("outside", outside)
	f.wantState("outside", "unreadable") // a real file, but not under projects/
}

func TestConversationEndpoint_FindsTheFileInAnotherProjectDirectory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.cfg, "projects", "shortened"), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, f.path("shortened"), line(1))
	f.ship("moved", f.path("computed")) // the directory Admiral computed does not exist
	if code, p, body := f.get("moved", ""); code != 200 || p.State != "ok" || len(p.Items) != 1 || p.Items[0].Text != "message 001" {
		t.Errorf("GET = %d %s, want ok with message 001", code, body)
	}
}

func TestConversationEndpoint_PagesAndRejectsBadBefore(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	appendTo(t, f.path("zz"), lines(1, 250))
	f.ship("big", f.path("zz"))

	code, last, body := f.get("big", "")
	if code != 200 || last.State != "ok" || len(last.Items) != 200 || !last.More ||
		last.Items[0].Text != "message 051" || last.Items[199].Text != "message 250" {
		t.Fatalf("newest page = %d %.200s, want 200 items message 051..250 with more", code, body)
	}
	_, older, body := f.get("big", "?before="+url.QueryEscape(last.Items[0].ID))
	if older.State != "ok" || len(older.Items) != 50 || older.More || older.Items[0].Text != "message 001" || older.Items[49].Text != "message 050" {
		t.Errorf("page before message 051 = %.200s, want 50 items message 001..050 and more=false", body)
	}
	for name, q := range map[string]string{"empty": "?before=", "unknown id": "?before=nope", "too long": "?before=" + strings.Repeat("a", 300)} {
		if code, _, body := f.get("big", q); code != 400 {
			t.Errorf("before %s = %d %s, want 400", name, code, body)
		}
	}
	if code, _, _ := f.get("nope", "?before="); code != 404 {
		t.Errorf("an unknown ship with a bad before = %d, want 404", code)
	}
}

// socket is a client of a real server.Server that serves svc.
type socket struct {
	t    *testing.T
	c    *websocket.Conn
	in   chan map[string]any
	done context.CancelFunc
}

func dial(t *testing.T, base string) *socket {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &socket{t: t, c: c, in: make(chan map[string]any, 64), done: cancel}
	go func() {
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(data, &m) == nil && m["type"] == "conversation" {
				s.in <- m
			}
		}
	}()
	t.Cleanup(func() { cancel(); c.CloseNow() })
	return s
}

func (s *socket) send(typ, ship string) {
	s.t.Helper()
	b, _ := json.Marshal(map[string]string{"type": typ, "ship": ship})
	if err := s.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		s.t.Fatal(err)
	}
}

func (s *socket) next(what string) map[string]any {
	s.t.Helper()
	select {
	case m := <-s.in:
		return m
	case <-time.After(10 * time.Second):
		s.t.Fatalf("within 10s: %s never arrived", what)
		return nil
	}
}

func (s *socket) quiet(what string) {
	s.t.Helper()
	select {
	case m := <-s.in:
		s.t.Fatalf("%s: got %v", what, m)
	case <-time.After(1500 * time.Millisecond):
	}
}

func serve(t *testing.T, f *fixture) string {
	t.Helper()
	srv := httptest.NewServer((&server.Server{Hub: server.NewHub(), Messages: f.svc.Messages(), Modules: []server.Module{f.svc}}).Handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func texts(m map[string]any) []string {
	var out []string
	items, _ := m["items"].([]any)
	for _, it := range items {
		out = append(out, it.(map[string]any)["text"].(string))
	}
	return out
}

func TestConversationSocket_DeliversAppendsToSubscribersOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	appendTo(t, f.path("zz"), lines(1, 3))
	f.ship("s", f.path("zz"))
	base := serve(t, f)
	sub, other := dial(t, base), dial(t, base)

	sub.send("conversation_open", "s")
	sub.quiet("opening a ship sent what was already there")
	appendTo(t, f.path("zz"), lines(4, 4))
	m := sub.next("the appended item")
	if got := texts(m); len(got) != 1 || got[0] != "message 004" || m["ship"] != "s" {
		t.Errorf("pushed %v for ship %v, want [message 004] for s", got, m["ship"])
	}
	other.quiet("a client that never opened the ship")

	// Messages from one client are handled in order, so a push for "t" shows the close of "s" was handled.
	f.ship("t", f.path("zz2"))
	if err := os.MkdirAll(filepath.Join(f.cfg, "projects", "zz2"), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, f.path("zz2"), line(1))
	sub.send("conversation_close", "s")
	sub.send("conversation_open", "t")
	appendTo(t, f.path("zz"), lines(5, 5))
	appendTo(t, f.path("zz2"), line(2))
	if m := sub.next("an append to t"); m["ship"] != "t" {
		t.Errorf("pushed for %v, want t", m["ship"])
	}
	sub.quiet("a closed subscription")
}

func TestConversationSocket_IgnoresShipsWithNoConversation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.harness["codex-ship"] = "codex"
	f.options["codex-ship"] = ""
	base := serve(t, f)
	c := dial(t, base)
	c.send("conversation_open", "codex-ship")
	c.send("conversation_open", "unknown")
	c.quiet("an open for a ship with no conversation")
}

func TestConversationSocket_AClientHoldsSixteenShipsOpen(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for i := 1; i <= MaxOpen+1; i++ {
		dir := fmt.Sprintf("d%02d", i)
		if err := os.MkdirAll(filepath.Join(f.cfg, "projects", dir), 0o755); err != nil {
			t.Fatal(err)
		}
		appendTo(t, f.path(dir), line(i))
		f.ship(fmt.Sprintf("ship%02d", i), f.path(dir))
	}
	base := serve(t, f)
	c := dial(t, base)
	for i := 1; i <= MaxOpen+1; i++ {
		c.send("conversation_open", fmt.Sprintf("ship%02d", i))
	}
	// Opens are handled in order, so a push for the last ship shows all seventeen were.
	appendTo(t, f.path("d01"), line(101))
	appendTo(t, f.path("d17"), line(117))
	if m := c.next("an append to the last ship"); m["ship"] != "ship17" {
		t.Errorf("first push was for %v, want ship17", m["ship"])
	}
	c.quiet("an append to the first ship, which the seventeenth open closed")
	appendTo(t, f.path("d02"), line(102))
	if m := c.next("an append to the second ship"); m["ship"] != "ship02" {
		t.Errorf("push was for %v, want ship02", m["ship"])
	}
}
