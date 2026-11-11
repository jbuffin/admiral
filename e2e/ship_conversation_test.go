package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A Claude Code ship's conversation (#264, u2): launch with --session-id, the
// pane option @admiral-transcript, GET /api/v1/ships/<ship>/conversation and the
// `conversation` WebSocket message (docs/api.md once it exists).
//
// Wire shapes under test (the coordinator's decisions):
//
//	GET  /api/v1/ships/<s>/conversation[?before=<id>]
//	     200 {"state":"ok"|"none"|"unreadable"|"gone","items":[...],"more":bool}
//	     404 unknown ship; 400 a before that is empty, malformed or not found
//	in   {"type":"conversation_open","ship":"<s>"} / {"type":"conversation_close","ship":"<s>"}
//	out  {"type":"conversation","ship":"<s>","items":[...]} to the opener only
//
// Every fleet here sets CLAUDE_CONFIG_DIR to a temp dir, so nothing is written
// under the engineer's ~/.claude*. The test writes the transcript itself, where
// Claude Code would, from the --session-id the fake recorded. It deliberately
// puts it under a slug Admiral would not compute ("zz"), so each read exercises
// the fallback that finds <config dir>/projects/*/<session id>.jsonl.

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type convWorld struct {
	*world
	cfgDir string
}

// newConvWorld: fleet "work" runs the fake as claude (cue: exit 3, so the ship
// anchors), fleet "resume" carries --resume in the dock args, fleet "cx" is a
// codex-only fleet.
func newConvWorld(t *testing.T) *convWorld {
	t.Helper()
	w := newWorld(t)
	c := &convWorld{world: w, cfgDir: filepath.Join(w.root, "cfg")}
	if err := os.MkdirAll(filepath.Join(c.cfgDir, "projects", "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.writeCue("exit 3")
	env := func() map[string]string {
		e := w.harnessEnv()
		e["CLAUDE_CONFIG_DIR"] = c.cfgDir
		return e
	}
	dock := func(h string, args ...string) map[string]any {
		d := map[string]any{"executable": w.fakeExe(h), "env": env()}
		if len(args) > 0 {
			d["args"] = args
		}
		return d
	}
	fleets := map[string]any{
		"work":   map[string]any{"name": "Work", "search_roots": []string{w.work}, "claude": dock("claude", "--fa-claude")},
		"resume": map[string]any{"name": "Resume", "search_roots": []string{w.work}, "claude": dock("claude", "--resume", "some-old-id")},
		"cx":     map[string]any{"name": "Codex", "search_roots": []string{w.work}, "codex": dock("codex")},
	}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": fleets})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	w.start()
	return c
}

// transcript is the path the test writes a ship's transcript to.
func (c *convWorld) transcript(id string) string {
	return filepath.Join(c.cfgDir, "projects", "zz", id+".jsonl")
}

// sessionID is the --session-id the fake recorded for a ship ("" when none).
func (c *convWorld) sessionID(session string) string {
	c.t.Helper()
	argv := c.record(session).argv
	for i, a := range argv {
		if a == "--session-id" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// launchClaude launches a claude ship in fleet and returns it with its session id.
func (c *convWorld) launchClaude(fleet, repo string) (shipJSON, string) {
	c.t.Helper()
	s := c.launch(c.order(fleet, "claude", repo, noSkill, "go"))
	id := c.sessionID(s.Session)
	if id == "" {
		c.t.Fatalf("ship %s: the harness argv has no --session-id: %v", s.Session, c.record(s.Session).argv)
	}
	return s, id
}

func (c *convWorld) convURL(session, query string) string {
	u := c.in.LocalURL + "/api/v1/ships/" + url.PathEscape(session) + "/conversation"
	if query != "" {
		u += "?" + query
	}
	return u
}

type convItem struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type convPage struct {
	State string     `json:"state"`
	Items []convItem `json:"items"`
	More  bool       `json:"more"`
}

// get fetches a page and returns the status code and the decoded body.
func (c *convWorld) get(session, query string) (int, convPage, string) {
	c.t.Helper()
	code, body := send(c.t, http.MethodGet, c.convURL(session, query), nil)
	var p convPage
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			c.t.Fatalf("GET %s: not JSON: %v\n%s", c.convURL(session, query), err, body)
		}
	}
	return code, p, body
}

// waitState polls until the endpoint answers 200 with state (and pred, if any).
func (c *convWorld) waitState(session, query, state string, pred func(convPage) bool) convPage {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, p, body := c.get(session, query)
		if code == http.StatusOK && p.State == state && (pred == nil || pred(p)) {
			return p
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("within 10s GET %s never showed state %q; last %d: %s", c.convURL(session, query), state, code, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func texts2(items []convItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Text
	}
	return out
}

// msg numbers a user line of the conversation.
func msg(i int) string { return fmt.Sprintf("message %03d", i) }

func userLines(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, userLine(fmt.Sprintf("u-%03d", i), msg(i), nil))
	}
	return out
}

// convMsg is a `conversation` WebSocket message.
type convMsg struct {
	Type  string     `json:"type"`
	Ship  string     `json:"ship"`
	Items []convItem `json:"items"`
}

func wsSend(t *testing.T, c *websocket.Conn, v map[string]any) {
	t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("websocket write %s: %v", b, err)
	}
}

// convReaders holds one background reader per connection. A timed-out Read
// closes a coder/websocket connection, so the helper never gives Read a deadline:
// a goroutine reads for the connection's life and nextConv waits on its channel.
var (
	convReadersMu sync.Mutex
	convReaders   = map[*websocket.Conn]chan convMsg{}
)

func convChan(c *websocket.Conn) chan convMsg {
	convReadersMu.Lock()
	defer convReadersMu.Unlock()
	ch, ok := convReaders[c]
	if !ok {
		ch = make(chan convMsg, 1024)
		convReaders[c] = ch
		go func() {
			defer close(ch)
			for {
				_, data, err := c.Read(context.Background())
				if err != nil {
					return
				}
				var m convMsg
				if json.Unmarshal(data, &m) == nil && m.Type == "conversation" {
					select {
					case ch <- m:
					default:
					}
				}
			}
		}()
	}
	return ch
}

// nextConv waits for a `conversation` message for ship, within d. ok is false
// when none arrived.
func nextConv(c *websocket.Conn, d time.Duration, ship string) (convMsg, bool) {
	ch := convChan(c)
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				return convMsg{}, false
			}
			if m.Ship == ship {
				return m, true
			}
		case <-timer.C:
			return convMsg{}, false
		}
	}
}

// ---- tests ----

// The launch, the recorded location, the page and `before`.
func TestShipConversation_LaunchRecordsAndServesPages(t *testing.T) {
	c := newConvWorld(t)
	s, id := c.launchClaude("work", "a")

	if !uuidRe.MatchString(id) {
		t.Errorf("--session-id = %q, want a new uuid", id)
	}
	argv := c.record(s.Session).argv
	if n := strings.Count(strings.Join(argv, "\x00"), "--session-id"); n != 1 {
		t.Errorf("argv carries --session-id %d times, want once: %v", n, argv)
	}

	// @admiral-transcript is set at launch: "<unix ns> launch <path>", before any hook.
	deadline := time.Now().Add(10 * time.Second)
	var opt string
	for {
		out, err := c.tmux("show-options", "-pv", "-t", s.Session, "@admiral-transcript")
		if err == nil && strings.TrimSpace(out) != "" {
			opt = strings.TrimSpace(out)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("@admiral-transcript was never set on %s within 10s (last %q, %v)", s.Session, out, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	f := strings.Fields(opt)
	if len(f) != 3 || f[1] != "launch" || !strings.Contains(f[2], id) {
		t.Errorf("@admiral-transcript = %q, want \"<unix ns> launch <path holding the session id %s>\"", opt, id)
	}

	// Nothing written yet: a ship that has no file yet is "none", not an error.
	if code, p, body := c.get(s.Session, ""); code != 200 || p.State != "none" || len(p.Items) != 0 {
		t.Errorf("before any transcript: %d %s, want 200 state none with no items", code, body)
	}

	// 250 items, found by session id under projects/zz/, not under the computed slug.
	appendLines(t, c.transcript(id), userLines(1, 250)...)
	last := c.waitState(s.Session, "", "ok", nil)
	if len(last.Items) != 200 || last.Items[0].Text != msg(51) || last.Items[199].Text != msg(250) || !last.More {
		t.Fatalf("last page: %d items %q..%q more=%v, want 200 items %q..%q more=true",
			len(last.Items), first(last.Items), lastText(last.Items), last.More, msg(51), msg(250))
	}
	code, older, body := c.get(s.Session, "before="+url.QueryEscape(last.Items[0].ID))
	if code != 200 || older.State != "ok" || len(older.Items) != 50 || older.Items[0].Text != msg(1) || older.Items[49].Text != msg(50) || older.More {
		t.Errorf("before=<first id>: %d %s, want the 50 items %q..%q and more=false", code, body, msg(1), msg(50))
	}

	// bad before: empty, malformed, not found.
	for _, q := range []string{"before=", "before=not-an-id", "before=" + url.QueryEscape("u-999")} {
		if code, _, body := c.get(s.Session, q); code != http.StatusBadRequest {
			t.Errorf("GET ?%s = %d, want 400: %s", q, code, body)
		}
	}
	if code, _, _ := c.get("no-such-ship", ""); code != http.StatusNotFound {
		t.Errorf("unknown ship = %d, want 404", code)
	}

	// The unversioned alias keeps working beside /api/v1 (ADR 0032): the route is
	// additive, so /api/ships/<s>/conversation is the same endpoint.
	code, body = send(t, http.MethodGet, c.in.LocalURL+"/api/ships/"+s.Session+"/conversation", nil)
	if code != 200 || !strings.Contains(body, `"state":"ok"`) {
		t.Errorf("GET /api/ships/<s>/conversation = %d %s, want the same 200", code, body)
	}
}

func first(items []convItem) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Text
}

func lastText(items []convItem) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1].Text
}

// Engineer-supplied session flags, and a harness with no adapter, mean no
// conversation, and the launch is not refused.
func TestShipConversation_NoneWhenNotAnchoredToOneOfOurs(t *testing.T) {
	c := newConvWorld(t)

	// --resume in the dock args: launch as before, no --session-id of ours.
	s := c.launch(c.order("resume", "claude", "r", noSkill, "go"))
	argv := c.record(s.Session).argv
	if strings.Contains(strings.Join(argv, "\x00"), "--session-id") {
		t.Errorf("a ship whose dock args carry --resume got --session-id: %v", argv)
	}
	if code, p, body := c.get(s.Session, ""); code != 200 || p.State != "none" || len(p.Items) != 0 {
		t.Errorf("resume ship: %d %s, want 200 state none", code, body)
	}

	// Codex has no adapter yet.
	x := c.launch(c.order("cx", "codex", "x", noSkill, "go"))
	if strings.Contains(strings.Join(c.record(x.Session).argv, "\x00"), "--session-id") {
		t.Errorf("a codex ship got --session-id")
	}
	if code, p, body := c.get(x.Session, ""); code != 200 || p.State != "none" {
		t.Errorf("codex ship: %d %s, want 200 state none", code, body)
	}
}

// A file with nothing parseable, metadata only, and a removed file.
func TestShipConversation_UnreadableEmptyAndGone(t *testing.T) {
	c := newConvWorld(t)
	s, id := c.launchClaude("work", "a")

	// Content, and not one JSON object.
	appendFile(t, c.transcript(id), "this is not a transcript\nnor is this\n")
	if p := c.waitState(s.Session, "", "unreadable", nil); len(p.Items) != 0 {
		t.Errorf("unreadable page has items: %+v", p.Items)
	}

	// A fresh file of metadata lines only is ok, with no items.
	if err := os.WriteFile(c.transcript(id), []byte(`{"type":"mode","mode":"normal","sessionId":"`+id+"\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := c.waitState(s.Session, "", "ok", nil); len(p.Items) != 0 || p.More {
		t.Errorf("metadata-only page = %+v, want ok with no items", p)
	}

	// Removed: "gone", and the ship (still anchored) is still served.
	appendLines(t, c.transcript(id), userLines(1, 2)...)
	c.waitState(s.Session, "", "ok", func(p convPage) bool { return len(p.Items) == 2 })
	if err := os.Remove(c.transcript(id)); err != nil {
		t.Fatal(err)
	}
	if p := c.waitState(s.Session, "", "gone", nil); len(p.Items) != 0 {
		t.Errorf("gone page has items: %+v", p.Items)
	}
}

// ADR 0019: a symlinked transcript, or one reached through a symlinked
// directory, is not read.
func TestShipConversation_SymlinksAreNotFollowed(t *testing.T) {
	c := newConvWorld(t)
	secret := filepath.Join(c.root, "outside.jsonl")
	appendLines(t, secret, userLines(1, 3)...)

	s, id := c.launchClaude("work", "a")
	if err := os.Symlink(secret, c.transcript(id)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // give a wrong implementation the time to serve it
	if code, p, body := c.get(s.Session, ""); code != 200 || len(p.Items) != 0 || p.State == "ok" {
		t.Errorf("symlinked transcript: %d %s, want no items and a state other than ok", code, body)
	}

	// A real file, but its project directory is a symlink to a directory outside.
	s2, id2 := c.launchClaude("work", "b")
	outDir := filepath.Join(c.root, "outdir")
	appendLines(t, filepath.Join(outDir, id2+".jsonl"), userLines(1, 3)...)
	if err := os.Symlink(outDir, filepath.Join(c.cfgDir, "projects", "yy")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if code, p, body := c.get(s2.Session, ""); code != 200 || len(p.Items) != 0 || p.State == "ok" {
		t.Errorf("transcript under a symlinked project dir: %d %s, want no items and a state other than ok", code, body)
	}
}

// New items reach only a client that opened the ship, and a closed one stops.
func TestShipConversation_WebSocketDeliversToSubscribersOnly(t *testing.T) {
	c := newConvWorld(t)
	s, id := c.launchClaude("work", "a")
	appendLines(t, c.transcript(id), userLines(1, 2)...)
	c.waitState(s.Session, "", "ok", func(p convPage) bool { return len(p.Items) == 2 })

	opener := dialWS(t, c.in.instance)
	other := dialWS(t, c.in.instance)
	wsSend(t, opener, map[string]any{"type": "conversation_open", "ship": s.Session})
	wsSend(t, opener, map[string]any{"type": "conversation_open", "ship": "no-such-ship"}) // ignored, no error
	// Opening sends nothing by itself.
	if m, ok := nextConv(opener, 700*time.Millisecond, s.Session); ok {
		t.Fatalf("opening sent a conversation message by itself: %+v", m)
	}

	appendLines(t, c.transcript(id), userLines(3, 3)...)
	m, ok := nextConv(opener, 5*time.Second, s.Session)
	if !ok || len(m.Items) != 1 || m.Items[0].Text != msg(3) {
		t.Fatalf("subscriber got %+v (ok=%v), want one conversation message with %q", m, ok, msg(3))
	}
	if m, ok := nextConv(other, 1500*time.Millisecond, s.Session); ok {
		t.Errorf("a client that never opened the ship received %+v", m)
	}

	// A changed item arrives again under its id: a tool call whose output came later.
	call := jsonLine(map[string]any{"type": "assistant", "uuid": "a-1", "timestamp": "2026-01-02T03:04:06.000Z",
		"message": map[string]any{"role": "assistant", "id": "m-1", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "ls"}}}}})
	appendLines(t, c.transcript(id), call)
	called, ok := nextConv(opener, 5*time.Second, s.Session)
	if !ok || len(called.Items) != 1 {
		t.Fatalf("tool call: got %+v (ok=%v), want one item", called, ok)
	}
	result := jsonLine(map[string]any{"type": "user", "uuid": "u-r1", "timestamp": "2026-01-02T03:04:07.000Z",
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "file-a\nfile-b"}}}})
	appendLines(t, c.transcript(id), result)
	done, ok := nextConv(opener, 5*time.Second, s.Session)
	if !ok || len(done.Items) != 1 || done.Items[0].ID != called.Items[0].ID {
		t.Errorf("the tool result gave %+v (ok=%v), want the same item id %q again", done, ok, called.Items[0].ID)
	}

	// Closing stops it.
	wsSend(t, opener, map[string]any{"type": "conversation_close", "ship": s.Session})
	time.Sleep(300 * time.Millisecond)
	appendLines(t, c.transcript(id), userLines(4, 4)...)
	if m, ok := nextConv(opener, 1500*time.Millisecond, s.Session); ok {
		t.Errorf("a closed subscription still received %+v", m)
	}
}

// A client holds at most 16 ships open; the 17th closes its oldest.
func TestShipConversation_AClientHoldsSixteenShipsOpen(t *testing.T) {
	c := newConvWorld(t)
	type sh struct {
		s  shipJSON
		id string
	}
	var ships []sh
	for i := 0; i < 17; i++ {
		s, id := c.launchClaude("work", fmt.Sprintf("r%02d", i))
		appendLines(t, c.transcript(id), userLines(1, 1)...)
		ships = append(ships, sh{s, id})
	}
	ws := dialWS(t, c.in.instance)
	for _, x := range ships {
		wsSend(t, ws, map[string]any{"type": "conversation_open", "ship": x.s.Session})
	}
	time.Sleep(500 * time.Millisecond)
	appendLines(t, c.transcript(ships[0].id), userLines(2, 2)...)
	appendLines(t, c.transcript(ships[16].id), userLines(2, 2)...)
	if m, ok := nextConv(ws, 5*time.Second, ships[16].s.Session); !ok || len(m.Items) != 1 {
		t.Fatalf("the 17th ship's append never arrived: %+v ok=%v", m, ok)
	}
	if m, ok := nextConv(ws, 1500*time.Millisecond, ships[0].s.Session); ok {
		t.Errorf("the oldest of 17 open ships still delivers after the 17th opened: %+v", m)
	}
}

// An anchored ship's conversation outlives its harness; dismissing it ends it.
func TestShipConversation_ReadableAfterExitUntilDismissed(t *testing.T) {
	c := newConvWorld(t)
	s, id := c.launchClaude("work", "a") // the fake exits 3 at once: anchored
	appendLines(t, c.transcript(id), userLines(1, 3)...)
	c.waitState(s.Session, "", "ok", func(p convPage) bool { return len(p.Items) == 3 })
	c.in.waitShipsList(t, 10*time.Second, "the ship anchored", func(l []shipJSON) bool {
		for _, x := range l {
			if x.Session == s.Session && x.Status == "anchored" {
				return true
			}
		}
		return false
	})
	if p := c.waitState(s.Session, "", "ok", nil); len(p.Items) != 3 {
		t.Errorf("after the harness exited: %d items, want 3", len(p.Items))
	}

	if code, body := send(t, http.MethodPost, c.in.LocalURL+"/api/ships/"+s.Session+"/dismiss", nil); code >= 300 {
		t.Fatalf("dismiss = %d: %s", code, body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, _, body := c.get(s.Session, "")
		if code == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 10s of dismissing, the conversation still answered %d: %s", code, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The capability list tells clients the endpoint exists (ADR 0032).
func TestShipConversation_VersionListsTheCapability(t *testing.T) {
	c := newConvWorld(t)
	code, body := send(t, http.MethodGet, c.in.LocalURL+"/api/version", nil)
	var v struct {
		Capabilities []string `json:"capabilities"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &v) != nil {
		t.Fatalf("GET /api/version = %d %s", code, body)
	}
	for _, n := range v.Capabilities {
		if n == "conversation" {
			return
		}
	}
	t.Errorf("capabilities = %v, want it to hold \"conversation\"", v.Capabilities)
}
