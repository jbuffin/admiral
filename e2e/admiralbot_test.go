package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/capabilities"
)

type botOpts struct {
	chatArgs []string // the chat dock's args; default a ship's widening ones
	shipCue  []string
	// chatShipCue is the cue a ship launched in the chat's own fleet plays, as FAKE_SHIP_CUE; without it that ship replays the chat's cue.
	chatShipCue []string
	sources     func(w *world) string // builds the raw `sources` object once the world exists
}

// newBotWorld writes a config with two fleets: "chat" (the chat's dock, cue file
// w.cue) and "ship" (its own cue file, so a ship never shares the chat's).
func newBotWorld(t *testing.T, o botOpts) *chatWorld {
	t.Helper()
	w := newWorld(t)
	c := &chatWorld{world: w, cfgDir: filepath.Join(w.root, "cfg")}
	if err := os.MkdirAll(filepath.Join(c.cfgDir, "projects", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.home, 0o700); err != nil {
		t.Fatal(err)
	}
	if o.chatArgs == nil {
		// A ship's dock args, as engineers set them: the chat must not take them.
		o.chatArgs = []string{"--fa-chat", "--allowedTools", "Bash(git *)", "--add-dir", "/tmp"}
	}
	shipCue := filepath.Join(w.root, "cue-ship")
	if o.shipCue != nil {
		if err := os.WriteFile(shipCue, []byte(strings.Join(o.shipCue, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chatEnv := map[string]string{"FAKE_RECORD_DIR": w.rec, "FAKE_CUE": w.cue, "CLAUDE_CONFIG_DIR": c.cfgDir}
	if o.chatShipCue != nil {
		chatShipCue := filepath.Join(w.root, "cue-chat-ship")
		if err := os.WriteFile(chatShipCue, []byte(strings.Join(o.chatShipCue, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		chatEnv["FAKE_SHIP_CUE"] = chatShipCue
	}
	shipEnv := map[string]string{"FAKE_RECORD_DIR": w.rec, "FAKE_CUE": shipCue}
	cfg := map[string]any{"version": 1,
		"fleets": map[string]any{
			"chat": map[string]any{"name": "Chat", "search_roots": []string{w.work},
				"claude": map[string]any{"executable": w.fakeExe("claude"), "args": o.chatArgs, "env": chatEnv},
				"chat":   map[string]any{"model": "opus", "effort": "high"}},
			"ship": map[string]any{"name": "Ship", "search_roots": []string{w.work},
				"claude": map[string]any{"executable": w.fakeExe("claude"), "args": []string{"--fa-ship"}, "env": shipEnv}},
		}}
	if o.sources != nil {
		cfg["sources"] = json.RawMessage(o.sources(w))
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	return c
}

func (w *world) questionsDir() string { return filepath.Join(w.home, "chat", chatFleet, "questions") }

func (w *world) questionFiles() []string {
	entries, _ := os.ReadDir(w.questionsDir())
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// botEnv is the world's environment without ADMIRAL_PORT and ADMIRAL_SHIP, so a
// command finds a server only through what the test passes. It is the chat
// of chatFleet that runs the command: ADMIRAL_CHAT_FLEET names that fleet.
func (w *world) botEnv(extra ...string) []string {
	return w.botEnvFor(chatFleet, extra...)
}

func (w *world) botEnvFor(fleet string, extra ...string) []string {
	var env []string
	for _, kv := range w.baseEnv() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "ADMIRAL_PORT" || k == "ADMIRAL_SHIP" || k == "ADMIRAL_CHAT_FLEET" {
			continue
		}
		env = append(env, kv)
	}
	return append(append(env, "ADMIRAL_CHAT_FLEET="+fleet), extra...)
}

type botProc struct {
	t    *testing.T
	args []string
	cmd  *exec.Cmd
	out  syncBuffer
	err  syncBuffer
	done chan struct{}
}

func (w *world) botStart(env []string, stdin string, args ...string) *botProc {
	w.t.Helper()
	p := &botProc{t: w.t, args: args, done: make(chan struct{})}
	p.cmd = exec.Command(binaryPath(w.t), args...)
	p.cmd.Dir = w.userHom
	p.cmd.Env = env
	p.cmd.Stdin = strings.NewReader(stdin)
	p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.err
	if err := p.cmd.Start(); err != nil {
		w.t.Fatalf("admiral %v: %v", args, err)
	}
	go func() { _ = p.cmd.Wait(); close(p.done) }()
	w.t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
	return p
}

func (p *botProc) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *botProc) result() cliResult {
	return cliResult{stdout: p.out.String(), stderr: p.err.String(), code: p.cmd.ProcessState.ExitCode()}
}

func (p *botProc) wait() cliResult {
	p.t.Helper()
	select {
	case <-p.done:
		return p.result()
	case <-testCtx(p.t).Done():
		hung(p.t, fmt.Sprintf("admiral %v still running at the test run's deadline; stdout %q stderr %q", p.args, p.out.String(), p.err.String()))
		return cliResult{}
	}
}

func (w *world) botCLI(env []string, stdin string, args ...string) cliResult {
	w.t.Helper()
	return w.botStart(env, stdin, args...).wait()
}

// expectExitOf is expectExit for a command that must exist: an exit code from
// a binary that does not know the command is not the behaviour under test.
func (w *world) expectExitOf(r cliResult, want int, what string) {
	w.t.Helper()
	if strings.Contains(r.stderr, "unknown command") {
		w.t.Fatalf("%s: %s", what, strings.TrimSpace(r.stderr))
	}
	w.expectExit(r, want, what)
}

func (p *botProc) failIfExited(what string) {
	p.t.Helper()
	if p.exited() {
		r := p.result()
		p.t.Fatalf("admiral %v exited %d while waiting for %s\nstdout: %s\nstderr: %s", p.args, r.code, what, r.stdout, r.stderr)
	}
}

type botSession struct {
	Type      string                       `json:"type"`
	Running   bool                         `json:"running"`
	State     string                       `json:"state"`
	Error     string                       `json:"error"`
	Questions []map[string]json.RawMessage `json:"questions"`
	// The `chat` message has the session's shape beside "type"; a nested
	// {"chat": {...}} is read as well.
	Chat *botSession `json:"chat"`
}

func (q botSession) ids() []string {
	out := []string{}
	for _, m := range q.Questions {
		out = append(out, qStr(m, "id"))
	}
	return out
}

func qStr(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

func fetchBot(t *testing.T, base string) botSession {
	t.Helper()
	code, body := send(t, http.MethodGet, base+"/api/chat/chat/session", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/chat/chat/session = %d, want 200: %s", code, body)
	}
	var s botSession
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("GET /api/chat/chat/session is not JSON: %v\n%s", err, body)
	}
	return s
}

// waitQuestions fails with p's exit code and output once p has ended, so a
// missing command reads as that and not as a timeout.
func waitQuestions(t *testing.T, base string, p *botProc, what string, pred func(botSession) bool) botSession {
	t.Helper()
	var got botSession
	eventually(t, "GET /api/chat/chat/session showing "+what, func() (bool, string) {
		s := fetchBot(t, base)
		if pred(s) {
			got = s
			return true, ""
		}
		if p != nil {
			p.failIfExited(what)
		}
		return false, fmt.Sprintf("%+v", s)
	})
	return got
}

func hasQuestions(n int) func(botSession) bool {
	return func(s botSession) bool { return len(s.Questions) == n }
}

func waitBotWS(t *testing.T, c *websocket.Conn, what string, pred func(botSession) bool) botSession {
	t.Helper()
	ctx := testCtx(t)
	var last string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			failRequest(t, fmt.Sprintf("the websocket delivering a `chat` message with %s (last chat message: %s)", what, last), err)
		}
		var m botSession
		if json.Unmarshal(data, &m) != nil || m.Type != "chat" {
			continue
		}
		last = string(data)
		if m.Chat != nil {
			m = *m.Chat
		}
		if pred(m) {
			return m
		}
	}
}

func askStdin(title string) string {
	return jsonLine(map[string]any{"title": title, "questions": []any{
		map[string]any{"id": "1", "text": "Which panel?", "multi": false,
			"options": []any{map[string]any{"label": "Freeform"}, map[string]any{"label": "Work queue", "description": "the shared queue"}}}}})
}

func botAnswer(label string) map[string]any {
	return map[string]any{"answers": map[string]any{"1": map[string]any{"choice": label}}}
}

func (w *world) answerChatQuestion(id string, body any) (int, string) {
	w.t.Helper()
	return send(w.t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/questions/"+id+"/answer", body)
}

func (w *world) rawInbox() string {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/inbox", nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET /api/inbox = %d, want 200: %s", code, body)
	}
	return body
}

// errMessage is the message of an error body: its `error` field, else the text.
func errMessage(body string) string {
	var m struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(body), &m) == nil && m.Error != "" {
		return m.Error
	}
	return strings.TrimSpace(body)
}

func atomicWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// Every command takes its server from ADMIRAL_PORT, so a chat started on a
// non-default port reaches that Admiral with no --port.
func TestAdmiralBot_ReadCommandsFindTheServerByADMIRAL_PORT(t *testing.T) {
	t.Parallel()
	c := newBotWorld(t, botOpts{shipCue: []string{"hook needs_you"}, sources: func(w *world) string {
		p := w.newTP("a")
		p.put("panels.json", `[{"id":"main","title":"Main"},{"id":"extra","title":"Extra"}]`)
		p.setOrders(map[string][]map[string]any{"main": {ord("o1", "First thing")}, "extra": {}})
		w.installPlugin("testplug", "testplug")
		return rawSourcesIn("ship", tpSource("a", p, nil))
	}})
	w := c.world
	w.start()
	if w.in.Port == "7420" {
		t.Fatalf("the test needs a non-default port")
	}
	w.waitSource("a", "both panels, main with its order", func(s sourceJSON) bool {
		return s.State == "ok" && len(s.Views) == 2 && s.Views[0].hasOrder("o1")
	})
	ship := w.launchIn("ship", "claude", "repo1")
	w.in.waitStatusList(t, ship.Session, "needs_you")
	_, inboxDir := shipPaths(w.record(ship.Session))
	seedItem(t, inboxDir, "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	w.in.waitInbox(t, "q-1")

	env := w.botEnv("ADMIRAL_PORT=" + w.in.Port)

	t.Run("orders --json is the server's body", func(t *testing.T) {
		w := w.at(t)
		r := w.botCLI(env, "", "orders", "--json")
		w.expectExit(r, 0, "admiral orders --json")
		code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/sources", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /api/sources = %d", code)
		}
		if compactJSON(t, []byte(r.stdout)) != compactJSON(t, []byte(body)) {
			t.Errorf("orders --json differs from GET /api/sources\ncli: %s\napi: %s", r.stdout, body)
		}
		s, ok := find(decodeSources(t, r.stdout), "a")
		if !ok || len(s.Views) != 2 {
			t.Errorf("orders --json lists %s, want source a with both panels", dump(decodeSources(t, r.stdout)))
		}
	})

	t.Run("orders prints a line for every panel, the empty one too", func(t *testing.T) {
		w := w.at(t)
		r := w.botCLI(env, "", "orders")
		w.expectExit(r, 0, "admiral orders")
		var main, extra, order bool
		for _, l := range strings.Split(r.stdout, "\n") {
			f := strings.Fields(l)
			indented := strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")
			switch {
			case !indented && len(f) >= 3 && f[0] == "a" && f[1] == "main" && f[2] == "Main":
				main = true
			case !indented && len(f) >= 3 && f[0] == "a" && f[1] == "extra" && f[2] == "Extra":
				extra = true
			case indented && len(f) >= 2 && f[0] == "o1" && strings.Join(f[1:], " ") == "First thing":
				order = true
			}
		}
		if !main || !extra || !order {
			t.Errorf("orders printed:\n%s\nwant a `a main Main` line, a `a extra Extra` line (empty panel) and an indented `o1 First thing` line", r.stdout)
		}
	})

	t.Run("inbox --json holds the item and the needs-you ship", func(t *testing.T) {
		w := w.at(t)
		r := w.botCLI(env, "", "inbox", "--json")
		w.expectExit(r, 0, "admiral inbox --json")
		var got struct {
			Items    json.RawMessage `json:"items"`
			NeedsYou []shipJSON      `json:"needs_you"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
			t.Fatalf("inbox --json is not JSON: %v\n%s", err, r.stdout)
		}
		var api struct {
			Items json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal([]byte(w.rawInbox()), &api); err != nil {
			t.Fatal(err)
		}
		if compactJSON(t, got.Items) != compactJSON(t, api.Items) {
			t.Errorf("inbox --json items differ from GET /api/inbox\ncli: %s\napi: %s", got.Items, api.Items)
		}
		if len(got.NeedsYou) != 1 || got.NeedsYou[0].Session != ship.Session || got.NeedsYou[0].Status != "needs_you" {
			t.Errorf("needs_you = %+v, want the one needs-you ship %s", got.NeedsYou, ship.Session)
		}
	})

	t.Run("no server for this home exits 1", func(t *testing.T) {
		w := w.at(t)
		free := w.botEnv("ADMIRAL_PORT=" + freePort(t))
		// With neither ADMIRAL_PORT nor --port the default is 7420: it must not reach
		// this Admiral on its own port (and an Admiral of the engineer's there is
		// another home).
		for name, e := range map[string][]string{"a free ADMIRAL_PORT": free, "neither ADMIRAL_PORT nor --port": w.botEnv()} {
			for _, cmd := range []string{"inbox", "orders"} {
				r := w.botCLI(e, "", cmd)
				if r.code != 1 {
					t.Errorf("admiral %s with %s: exit %d, want 1\nstdout: %s\nstderr: %s", cmd, name, r.code, r.stdout, r.stderr)
				}
			}
		}
	})
}

// `orders add` posts to the server and prints the new order's id; the cases
// that must not reach the server don't.
func TestOrdersAddCommand(t *testing.T) {
	t.Parallel()
	var hang *tplug
	c := newBotWorld(t, botOpts{sources: func(w *world) string {
		hang = w.newTP("hang")
		w.installPlugin("testplug", "testplug")
		return rawSourcesIn("ship",
			w.orderSrcDef("orders", "Orders"),
			tpSource("hang", hang, map[string]any{"capabilities": []string{"add", "refresh"}}))
	}})
	w := c.world
	w.extra = append(w.extra, "ADMIRAL_CALL_TIMEOUT="+shortCall.String())
	w.start()
	w.waitSource("orders", "state ok", isOK)
	w.waitSource("hang", "state ok", isOK)
	panel := w.panelID("orders")
	env := w.botEnv("ADMIRAL_PORT=" + w.in.Port)

	t.Run("text on stdin or after -- adds an order and prints its id", func(t *testing.T) {
		w := w.at(t)
		r := w.botCLI(env, "Review the harbour log\nsecond line\n", "orders", "add", "--source", "orders", "--panel", panel)
		w.expectExit(r, 0, "orders add (stdin)")
		id := strings.TrimSpace(r.stdout)
		if id == "" || strings.ContainsAny(id, " \t\n") {
			t.Fatalf("orders add printed %q, want just the new order's id", r.stdout)
		}
		s := w.waitSource("orders", "the new order", func(s sourceJSON) bool { return s.hasOrder(id) })
		if o, _ := s.order(id); !strings.Contains(o.Text, "second line") {
			t.Errorf("the order's text = %q, want the stdin text", o.Text)
		}
		r = w.botCLI(env, "", "orders", "add", "--source", "orders", "--panel", panel, "--", "Check out the lighthouse")
		w.expectExit(r, 0, "orders add (-- text)")
		id2 := strings.TrimSpace(r.stdout)
		w.waitSource("orders", "the second order", func(s sourceJSON) bool { return id2 != "" && id2 != id && s.hasOrder(id2) })
	})

	t.Run("an unknown panel exits 1 with the server's error", func(t *testing.T) {
		w := w.at(t)
		code, body := w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": "no-such-panel", "text": "x"})
		if code/100 == 2 {
			t.Fatalf("the server accepted an order for an unknown panel: %d %s", code, body)
		}
		r := w.botCLI(env, "x\n", "orders", "add", "--source", "orders", "--panel", "no-such-panel")
		w.expectExit(r, 1, "orders add to an unknown panel")
		if msg := errMessage(body); !strings.Contains(r.both(), msg) {
			t.Errorf("orders add said %q, want the server's message %q", r.both(), msg)
		}
	})

	t.Run("text that is only whitespace exits 2 without calling the server", func(t *testing.T) {
		w := w.at(t)
		// The port answers nothing: a call to the server would exit 1, not 2.
		dead := w.botEnv("ADMIRAL_PORT=" + freePort(t))
		w.expectExitOf(w.botCLI(dead, " \n\t \n", "orders", "add", "--source", "orders", "--panel", panel), 2, "orders add of whitespace")
		before := len(w.panelOrders("orders"))
		w.expectExitOf(w.botCLI(env, "   ", "orders", "add", "--source", "orders", "--panel", panel), 2, "orders add of whitespace")
		if after := len(w.panelOrders("orders")); after != before {
			t.Errorf("a whitespace-only order changed the panel from %d to %d orders", before, after)
		}
	})

	t.Run("a timeout may mean the order was added: exit 5, and no second try", func(t *testing.T) {
		w := w.at(t)
		hang.put("hang-orders.add", "")
		hp := w.panelID("hang")
		r := w.botCLI(env, "Order that may land\n", "orders", "add", "--source", "hang", "--panel", hp)
		w.expectExit(r, 5, "orders add when the plugin never answers")
		if !strings.Contains(r.both(), "orders") {
			t.Errorf("exit 5 should say to check with `admiral orders`, got %q", r.both())
		}
		if n := len(hang.callsOf("orders.add")); n != 1 {
			t.Errorf("the plugin got %d orders.add calls, want exactly 1 (an uncertain add is never sent again)", n)
		}
	})
}

func mcpConfigPath(w *world) string { return filepath.Join(w.home, "chat", chatFleet, "mcp.json") }

type mcpReply struct {
	IsError bool `json:"isError"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func (r mcpReply) text() string {
	var parts []string
	for _, c := range r.Content {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, "\n")
}

// mcpCall has the fake chat send one tools/call through the server its --mcp-config names, by
// the cue `mcp <name>`: the request is the file <rec>/_chat-chat/<name>, the reply <name>.reply.
func (c *chatWorld) mcpCall(name, tool string, args map[string]any) mcpReply {
	c.t.Helper()
	req, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		c.t.Fatal(err)
	}
	atomicWrite(c.t, filepath.Join(c.recDir(), name), string(req))
	reply := filepath.Join(c.recDir(), name+".reply")
	var raw []byte
	eventually(c.t, "the fake chat answering `mcp "+name+"` ("+tool+")", func() (bool, string) {
		raw, err = os.ReadFile(reply)
		if err == nil && len(raw) > 0 {
			return true, ""
		}
		mcpErr, _ := os.ReadFile(filepath.Join(c.recDir(), "mcp-error"))
		return false, fmt.Sprintf("the cue is not run or the MCP server is not reached; chat session %+v; the cue's errors: %s", c.session(), mcpErr)
	})
	var rep mcpReply
	if err := json.Unmarshal(raw, &rep); err != nil {
		c.t.Fatalf("the fake's reply to %s %s is not a tool result: %v\n%s", name, tool, err, raw)
	}
	return rep
}

// AdmiralBot acts only through the MCP server: it launches in its own fleet, a repeat in one
// message is refused naming the ship (Admiral counts the messages from the chat's hook, so the
// model passes no id), the next message may launch again, another fleet's ship is out of
// reach, and its own can be stopped.
func TestAdmiralBot_ChatDrivesAdmiralOverMCP(t *testing.T) {
	t.Parallel()
	c := newBotWorld(t, botOpts{})
	w := c.world
	prompt := `event UserPromptSubmit {"prompt":"look at the harbour log"}`
	w.writeCue(prompt, "mcp launch", "mcp again", prompt, "mcp next", "mcp stop-other", "mcp stop-own",
		"mcp note", "wait briefed", `event SessionStart {"source":"clear"}`, `event PreCompact {"trigger":"auto","session_id":"s"}`, "mcp promise")
	// every AdmiralBot launch is autopilot, which needs the guard proven in the chat's fleet
	w.proveGuard(chatFleet)
	w.start()
	other := w.launch(w.order("ship", "claude", "otherrepo", noSkill, "x"))
	c.start()

	launch := map[string]any{"harness": "claude", "dir": w.checkout("repo1"), "skill": "none", "text": "Look at the harbour log", "reason": "to read the harbour log"}
	first := c.mcpCall("launch", "launch_ship", launch)
	if first.IsError {
		t.Fatalf("launch_ship was refused: %s", first.text())
	}
	var launched struct{ Mode string }
	if err := json.Unmarshal([]byte(first.text()), &launched); err != nil || launched.Mode != "autopilot" {
		t.Errorf("launch_ship result = %q, want mode autopilot: every AdmiralBot launch runs in autopilot unless with_me", first.text())
	}
	var mine shipJSON
	for end := time.Now().Add(10 * time.Second); mine.Session == ""; time.Sleep(50 * time.Millisecond) {
		for _, s := range w.in.ships(t) {
			if s.Fleet == chatFleet && s.Via == "admiralbot" {
				mine = s
			}
		}
		if time.Now().After(end) {
			t.Fatalf("GET /api/ships never listed a ship of fleet %s stamped via admiralbot: %s", chatFleet, dump(w.in.ships(t)))
		}
	}
	if !strings.HasSuffix(mine.Dir, "repo1") {
		t.Errorf("the launched ship's dir = %q, want repo1", mine.Dir)
	}
	if other.Via != "" {
		t.Errorf("the engineer's own launch is stamped via %q, want none", other.Via)
	}
	if log := w.waitShipLog(mine.Session, "a launched record", func(l []outRec) bool { return len(l) > 0 }); log[0]["via"] != "admiralbot" {
		t.Errorf("the launched record = %v, want via admiralbot", log[0])
	}

	t.Run("the same chat message is refused naming its ship", func(t *testing.T) {
		// one message, so the second freeform launch is refused whatever the model sends
		again := c.mcpCall("again", "launch_ship", launch)
		if !again.IsError || !strings.Contains(again.text(), "already has a ship") || !strings.Contains(again.text(), mine.Name) {
			t.Errorf("the repeat launch gave %+v, want an error saying the message already has a ship (%s)", again, mine.Name)
		}
		n := 0
		for _, s := range w.in.ships(t) {
			if s.Fleet == chatFleet {
				n++
			}
		}
		if n != 1 {
			t.Errorf("fleet %s has %d ships after the repeat, want 1", chatFleet, n)
		}
	})

	t.Run("the next chat message may launch again", func(t *testing.T) {
		next := c.mcpCall("next", "launch_ship", launch)
		if next.IsError {
			t.Errorf("the launch after a new message was refused: %s", next.text())
		}
	})

	t.Run("another fleet's ship is refused naming its fleet and keeps running", func(t *testing.T) {
		r := c.mcpCall("stop-other", "stop_ship", map[string]any{"session": other.Session})
		if !r.IsError || !strings.Contains(r.text(), "is in fleet ship") || !strings.Contains(r.text(), other.Session) {
			t.Errorf("stop of %s gave %+v, want an error saying it is in fleet ship", other.Session, r)
		}
		for _, m := range w.shipLog(other.Session) {
			if m["event"] == "stopped" {
				t.Errorf("the other fleet's ship has a stopped record: %v", m)
			}
		}
		listed := false
		for _, s := range w.in.ships(t) {
			listed = listed || s.Session == other.Session
		}
		if !listed {
			t.Errorf("GET /api/ships no longer lists %s", other.Session)
		}
	})

	t.Run("its own ship is stopped and the record says via admiralbot", func(t *testing.T) {
		r := c.mcpCall("stop-own", "stop_ship", map[string]any{"session": mine.Session})
		if r.IsError || !strings.Contains(r.text(), mine.Session) {
			t.Fatalf("stop of %s gave %+v, want the stopped session", mine.Session, r)
		}
		log := w.waitShipLog(mine.Session, "a stopped record", func(l []outRec) bool { return lastOf(l, "stopped") != nil })
		if got := lastOf(log, "stopped"); got["via"] != "admiralbot" {
			t.Errorf("the stopped record = %v, want via admiralbot", got)
		}
	})

	t.Run("a note it saves is in the brief the next SessionStart loads", func(t *testing.T) {
		const fact = "the harbour log is rotated on Mondays"
		if r := c.mcpCall("note", "save_note", map[string]any{"text": fact}); r.IsError {
			t.Fatalf("save_note was refused: %s", r.text())
		}
		briefPath := filepath.Join(w.home, "chat", chatFleet, "brief.md")
		for end := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if b, _ := os.ReadFile(briefPath); strings.Contains(string(b), fact) {
				break
			}
			if time.Now().After(end) {
				b, _ := os.ReadFile(briefPath)
				t.Fatalf("within 10s chat/%s/brief.md never held the note; it is %q", chatFleet, b)
			}
		}
		c.touch("briefed")
		stdoutPath := filepath.Join(c.recDir(), "event-stdout")
		var out string
		for end := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			b, _ := os.ReadFile(stdoutPath)
			if out = string(b); strings.Contains(out, fact) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("within 10s the SessionStart hook never printed the note; event-stdout is %q", out)
			}
		}
		// The PreCompact cue's plain text follows the SessionStart line in the same file.
		var got struct {
			Output struct {
				Event   string `json:"hookEventName"`
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])), &got); err != nil || got.Output.Event != "SessionStart" || !strings.Contains(got.Output.Context, fact) {
			t.Errorf("SessionStart printed %q, want hookSpecificOutput for SessionStart whose additionalContext holds the brief with the note (%v)", out, err)
		}
	})

	t.Run("the auto PreCompact hook prints the saved note's id to keep", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(w.home, "chat", chatFleet, "ledger.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		var id string
		for _, line := range strings.Split(string(raw), "\n") {
			var ev struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && strings.Contains(ev.Text, "harbour log") {
				id = ev.ID
			}
		}
		if id == "" {
			t.Fatalf("the ledger holds no saved note: %s", raw)
		}
		stdoutPath := filepath.Join(c.recDir(), "event-stdout")
		var out string
		for end := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			b, _ := os.ReadFile(stdoutPath)
			if out = string(b); strings.Contains(out, id) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("within 10s the PreCompact hook never printed note id %s; event-stdout is %q", id, out)
			}
		}
	})

	t.Run("a time commitment it records fires as a card in the chat with no new turn", func(t *testing.T) {
		const promise = "tell me the harbour log is rotated"
		due := time.Now().UTC().Add(3 * time.Second).Format(time.RFC3339)
		r := c.mcpCall("promise", "make_commitment", map[string]any{"text": promise, "trigger": "time", "subject": due, "action": "remind"})
		if r.IsError {
			t.Fatalf("make_commitment was refused: %s", r.text())
		}
		typed := c.stdin() // what the chat was sent so far: a turn would add to it
		var card *chatMsg
		for end := time.Now().Add(15 * time.Second); card == nil; time.Sleep(50 * time.Millisecond) {
			for _, m := range c.session().Messages {
				if m.Kind == "commitment" && strings.Contains(m.Text, promise) {
					card = &m
				}
			}
			if card == nil && time.Now().After(end) {
				t.Fatalf("within 15s GET /api/chat/%s/session never listed a commitment card for %q; %s", chatFleet, promise, c.diag())
			}
		}
		if got := c.stdin(); got != typed {
			t.Errorf("the chat was sent %q after the card, want %q: a fired commitment starts no turn", got, typed)
		}
	})
}

func (w *world) panelOrders(key string) []string {
	w.t.Helper()
	s, ok := find(w.sources(), key)
	if !ok {
		w.t.Fatalf("no source %q", key)
	}
	return s.orderIDs()
}

// The chat starts with the fence in its argv, ADMIRAL_PORT in its env, the
// skill on disk and the permissions in its bundle. A ship gets none of it.
func TestAdmiralBot_ChatSessionIsFenced(t *testing.T) {
	t.Parallel()
	c := newBotWorld(t, botOpts{})
	w := c.world
	w.start()

	// A link planted at the skill's path is never followed.
	skillDir := filepath.Join(w.home, "chat", chatFleet, ".claude", "skills", "admiralbot")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	outside := filepath.Join(w.root, "outside.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("OUTSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, skillPath); err != nil {
		t.Fatal(err)
	}

	c.start()
	r := w.record(chatSessionName)
	// tmux's server exists now. A ship must not inherit the port from it.
	w.mustTmux("set-environment", "-g", "ADMIRAL_PORT", "9")

	t.Run("argv", func(t *testing.T) {
		idx := func(a string) int {
			for i, x := range r.argv {
				if x == a {
					return i
				}
			}
			return -1
		}
		settings := idx("--settings")
		model := idx("--model")
		for _, pair := range [][2]string{{"--permission-mode", "dontAsk"}, {"--setting-sources", "project"}, {"--tools", ""}} {
			if !hasPair(r.argv, pair[0], pair[1]) {
				t.Errorf("argv = %q, want %s %s as separate elements", r.argv, pair[0], pair[1])
			}
			if i := idx(pair[0]); !(model >= 0 && model < i && i < settings) {
				t.Errorf("argv = %q, want %s after --model and before --settings", r.argv, pair[0])
			}
		}
		i := idx("--append-system-prompt")
		if i < 0 || i+1 >= len(r.argv) || !(model < i && i < settings) {
			t.Fatalf("argv = %q, want --append-system-prompt <paragraph> after --model and before --settings", r.argv)
		}
		for _, a := range []string{"--fa-chat", "--allowedTools", "Bash(git *)", "--add-dir", "/tmp"} {
			if has(r.argv, a) {
				t.Errorf("argv = %q holds the dock's %q: the chat takes no dock args", r.argv, a)
			}
		}
		if !has(r.argv, "--strict-mcp-config") || !hasPair(r.argv, "--mcp-config", mcpConfigPath(w)) {
			t.Errorf("argv = %q, want --strict-mcp-config and --mcp-config %s", r.argv, mcpConfigPath(w))
		}
		if has(r.argv, "Bash,Skill,Read") {
			t.Errorf("argv = %q still lists built-in tools", r.argv)
		}
		if p := r.argv[i+1]; !strings.Contains(p, "AdmiralBot") || strings.HasPrefix(p, "--") {
			t.Errorf("the system prompt element = %q, want one paragraph that says it is AdmiralBot", p)
		}
		if i+2 != settings && !strings.HasPrefix(r.argv[i+2], "--") {
			t.Errorf("argv = %q: the system prompt must be a single element, but %q follows it", r.argv, r.argv[i+2])
		}
	})

	t.Run("env has the port Admiral listens on", func(t *testing.T) {
		if got := r.env["ADMIRAL_PORT"]; got != w.in.Port {
			t.Errorf("env ADMIRAL_PORT = %q, want %q ", got, w.in.Port)
		}
		if r.env["ADMIRAL_HOME"] != w.home {
			t.Errorf("env ADMIRAL_HOME = %q, want %q", r.env["ADMIRAL_HOME"], w.home)
		}
	})

	t.Run("the skill is written fresh, never through a link", func(t *testing.T) {
		if got, _ := os.ReadFile(outside); string(got) != "OUTSIDE" {
			t.Errorf("the file the planted link pointed to now holds %q: the link was followed", got)
		}
		// A skill the chat can't load is optional; one that is written replaced the link and teaches no CLI.
		fi, err := os.Lstat(skillPath)
		if err != nil {
			return
		}
		if !fi.Mode().IsRegular() {
			t.Fatalf("%s is %v, want a regular file that replaced the planted link, or nothing", skillPath, fi.Mode())
		}
		b, _ := os.ReadFile(skillPath)
		for _, banned := range []string{"admiral orders add", "admiral chat ask", "admiral ships"} {
			if strings.Contains(string(b), banned) {
				t.Errorf("SKILL.md still teaches %q: the chat has no shell", banned)
			}
		}
	})

	t.Run("the MCP config names only admiral and holds the token", func(t *testing.T) {
		path := mcpConfigPath(w)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("the MCP config: %v", err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", path, fi.Mode().Perm())
		}
		raw, _ := os.ReadFile(path)
		var cfg struct {
			Servers map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Servers) != 1 {
			t.Fatalf("mcp.json = %s (%v), want one server", raw, err)
		}
		srv, ok := cfg.Servers["admiral"]
		if !ok {
			t.Fatalf("mcp.json = %s, want the one server named admiral", raw)
		}
		if want, _ := filepath.EvalSymlinks(binaryPath(t)); srv.Command != want && srv.Command != binaryPath(t) {
			t.Errorf("command = %q, want the running Admiral %q", srv.Command, binaryPath(t))
		}
		if len(srv.Args) != 1 || srv.Args[0] != "mcp" {
			t.Errorf("args = %q, want [mcp]", srv.Args)
		}
		token := srv.Env["ADMIRAL_ACTOR_TOKEN"]
		if token == "" || srv.Env["ADMIRAL_PORT"] != w.in.Port || srv.Env["ADMIRAL_HOME"] != w.home || srv.Env["ADMIRAL_CHAT_FLEET"] != chatFleet || len(srv.Env) != 4 {
			t.Errorf("env = %v, want the token, ADMIRAL_PORT %s, ADMIRAL_HOME %s and ADMIRAL_CHAT_FLEET %s only", srv.Env, w.in.Port, w.home, chatFleet)
		}
		if token != "" {
			if strings.Contains(strings.Join(r.argv, "\x00"), token) {
				t.Error("the actor token is in the chat's argv")
			}
			for k, v := range r.env {
				if strings.Contains(v, token) {
					t.Errorf("the actor token is in the chat's env %s", k)
				}
			}
		}
	})

	t.Run("the bundle holds the permissions", func(t *testing.T) {
		i := -1
		for k, a := range r.argv {
			if a == "--settings" {
				i = k
			}
		}
		if i < 0 || i+1 >= len(r.argv) {
			t.Fatalf("argv = %q has no --settings", r.argv)
		}
		raw, err := os.ReadFile(r.argv[i+1])
		if err != nil {
			t.Fatal(err)
		}
		var b struct {
			Permissions struct {
				Allow      []string `json:"allow"`
				Deny       []string `json:"deny"`
				BlockReads bool     `json:"blockReadsOutsideWorkingDirectories"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatalf("the chat bundle is not JSON: %v", err)
		}
		sameSet := func(got, want []string) bool {
			if len(got) != len(want) {
				return false
			}
			for _, x := range want {
				if !has(got, x) {
					return false
				}
			}
			return true
		}
		allow := []string{"mcp__admiral__*"}
		if !sameSet(b.Permissions.Allow, allow) {
			t.Errorf("permissions.allow = %q, want exactly %q", b.Permissions.Allow, allow)
		}
		if deny := []string{"Edit", "Write", "NotebookEdit"}; !sameSet(b.Permissions.Deny, deny) {
			t.Errorf("permissions.deny = %q, want exactly %q", b.Permissions.Deny, deny)
		}
		if !b.Permissions.BlockReads {
			t.Errorf("permissions.blockReadsOutsideWorkingDirectories is not true")
		}
	})

	t.Run("a ship has none of it", func(t *testing.T) {
		w := w.at(t)
		ship := w.launch(w.order("ship", "claude", "shiprepo", noSkill, "x"))
		sr := w.record(ship.Session)
		for _, a := range []string{"--permission-mode", "--setting-sources", "--tools", "--append-system-prompt", "--mcp-config", "--strict-mcp-config"} {
			if has(sr.argv, a) {
				t.Errorf("the ship's argv = %q holds %s", sr.argv, a)
			}
		}
		if v, ok := sr.env["ADMIRAL_CHAT_FLEET"]; ok {
			t.Errorf("the ship's env has ADMIRAL_CHAT_FLEET=%q: only a chat gets it", v)
		}
		if v, ok := sr.env["ADMIRAL_PORT"]; ok {
			t.Errorf("the ship's env has ADMIRAL_PORT=%q, even with it in tmux's global environment", v)
		}
	})
}

// A chat question is served by the chat and listed in the inbox beside a ship's item.
func TestAdmiralBot_ChatAskRoundTrip(t *testing.T) {
	t.Parallel()
	c := newBotWorld(t, botOpts{})
	w := c.world
	w.start()
	seedItem(t, filepath.Join(w.home, "inbox", "some-ship"), "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	w.in.waitInbox(t, "q-1")
	ws := dialWS(t, w.in.instance)
	c.start()

	p := w.botStart(w.botEnv(), askStdin("Where does it go?"), "chat", "ask")
	s := waitQuestions(t, w.in.LocalURL, p, "the question", hasQuestions(1))
	q := s.Questions[0]
	id := qStr(q, "id")

	t.Run("the question is served and listed in the inbox", func(t *testing.T) {
		if qStr(q, "kind") != "question" || qStr(q, "session") != chatSessionName || qStr(q, "title") != "Where does it go?" {
			t.Errorf("question = %v, want kind question, session %s and the title", q, chatSessionName)
		}
		if _, err := time.Parse(time.RFC3339, qStr(q, "created")); err != nil {
			t.Errorf("created = %s, want an RFC 3339 time", q["created"])
		}
		if string(q["answer"]) != "null" {
			t.Errorf("answer = %s, want null while unanswered", q["answer"])
		}
		var nested struct {
			Questions []struct{ ID string } `json:"questions"`
		}
		if json.Unmarshal(q["question"], &nested) != nil || len(nested.Questions) != 1 || nested.Questions[0].ID != "1" {
			t.Errorf("question.questions = %s, want the item's one question in the inbox's shape", q["question"])
		}
		if s.State != "waiting" {
			t.Errorf("state = %q with a question open, want waiting", s.State)
		}
		raw, err := os.ReadFile(filepath.Join(w.questionsDir(), id+".json"))
		if err != nil {
			t.Fatalf("the question file: %v", err)
		}
		var item map[string]any
		if json.Unmarshal(raw, &item) != nil || item["version"] != float64(1) || item["id"] != id || item["kind"] != "question" {
			t.Errorf("question file = %s, want version 1, the id and kind question", raw)
		}
		waitBotWS(t, ws, "the question", func(m botSession) bool { return len(m.Questions) == 1 && m.State == "waiting" })
		if got := w.in.waitInbox(t, "q-1", id); len(got) != 2 || got[1].Session != chatSessionName {
			t.Errorf("the inbox lists %+v, want the ship's q-1 and the chat question of session %s", got, chatSessionName)
		}
		if _, err := os.Lstat(filepath.Join(w.home, "inbox", chatSessionName)); err == nil {
			t.Errorf("inbox/%s exists: chat questions never go in the inbox", chatSessionName)
		}
	})

	t.Run("a bad answer is refused and the question stays open", func(t *testing.T) {
		w := w.at(t)
		if code, body := w.answerChatQuestion(id, botAnswer("Not an option")); code != http.StatusBadRequest {
			t.Errorf("answer naming a label that is not an option = %d, want 400: %s", code, body)
		}
		if code, body := w.answerChatQuestion("no-such-question", botAnswer("Freeform")); code != http.StatusNotFound {
			t.Errorf("answer to an unknown id = %d, want 404: %s", code, body)
		}
		if p.exited() {
			t.Errorf("chat ask exited %d after answers that were refused", p.result().code)
		}
	})

	t.Run("the answer reaches chat ask and the session", func(t *testing.T) {
		w := w.at(t)
		if code, body := w.answerChatQuestion(id, botAnswer("Work queue")); code/100 != 2 {
			t.Fatalf("answer = %d, want 2xx: %s", code, body)
		}
		r := p.wait()
		w.expectExit(r, 0, "chat ask after the answer")
		file, err := os.ReadFile(filepath.Join(w.questionsDir(), id+".answer.json"))
		if err != nil {
			t.Fatalf("the answer file: %v", err)
		}
		if strings.TrimSpace(r.stdout) != strings.TrimSpace(string(file)) {
			t.Errorf("chat ask printed %q, want the answer file %q", r.stdout, file)
		}
		var a struct {
			Answers map[string]struct {
				Choice string `json:"choice"`
			} `json:"answers"`
		}
		if json.Unmarshal(file, &a) != nil || a.Answers["1"].Choice != "Work queue" {
			t.Errorf("answer file = %s, want the choice Work queue", file)
		}
		s := waitQuestions(t, w.in.LocalURL, nil, "the answer on the question", func(s botSession) bool {
			return len(s.Questions) == 1 && string(s.Questions[0]["answer"]) != "null"
		})
		if s.State == "waiting" {
			t.Errorf("state = waiting with every question answered")
		}
		if code, body := w.answerChatQuestion(id, botAnswer("Freeform")); code != http.StatusConflict {
			t.Errorf("a second answer = %d, want 409: %s", code, body)
		}
		w.in.waitInbox(t, "q-1")
	})
}

// The edges that need no server: bad input, the ship refusal, the timeout and
// --wait, all through the files alone.
func TestAdmiralBot_ChatAskWorksOnFilesAlone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := os.MkdirAll(w.home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := w.botEnv()
	answerFile := func(id, choice string) string {
		return jsonLine(map[string]any{"version": 1, "id": id, "kind": "question", "answered": "2026-10-22T10:00:00Z",
			"answers": map[string]any{"1": map[string]any{"choice": choice}}})
	}

	t.Run("a question with no title exits 2 and writes nothing", func(t *testing.T) {
		w := w.at(t)
		noTitle := jsonLine(map[string]any{"questions": []any{map[string]any{"id": "1", "text": "Which?", "options": []any{map[string]any{"label": "A"}}}}})
		w.expectExitOf(w.botCLI(env, noTitle, "chat", "ask"), 2, "chat ask without a title")
		w.expectExitOf(w.botCLI(env, "not json", "chat", "ask"), 2, "chat ask of text that is not JSON")
		if got := w.questionFiles(); len(got) != 0 {
			t.Errorf("a bad question left %v in chat/questions", got)
		}
	})

	t.Run("a ship may not ask in the chat", func(t *testing.T) {
		w := w.at(t)
		shipEnv := w.botEnv("ADMIRAL_SHIP=some-ship")
		w.expectExitOf(w.botCLI(shipEnv, askStdin("From a ship"), "chat", "ask"), 2, "chat ask with ADMIRAL_SHIP set")
		if got := w.questionFiles(); len(got) != 0 {
			t.Errorf("a ship's question left %v in chat/questions", got)
		}
	})

	var id string
	t.Run("--timeout returns exit 3 with the way to resume, --wait then gets the answer", func(t *testing.T) {
		w := w.at(t)
		start := time.Now()
		r := w.botCLI(env, askStdin("Slow one"), "chat", "ask", "--timeout", "1s")
		w.expectExit(r, 3, "chat ask --timeout 1s")
		if time.Since(start) > 15*time.Second {
			t.Errorf("chat ask --timeout 1s took %s", time.Since(start))
		}
		files := w.questionFiles()
		if len(files) != 1 || !strings.HasSuffix(files[0], ".json") || strings.Contains(files[0], "answer") {
			t.Fatalf("chat/questions holds %v, want exactly the one question file", files)
		}
		id = strings.TrimSuffix(files[0], ".json")
		if out := r.both(); !strings.Contains(out, id) || !strings.Contains(out, "--wait") {
			t.Errorf("exit 3 said %q, want the question id %s and `--wait`", out, id)
		}
		var item struct {
			Version  int    `json:"version"`
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Created  string `json:"created"`
			Title    string `json:"title"`
			Question struct {
				Questions []struct {
					ID string `json:"id"`
				} `json:"questions"`
			} `json:"question"`
		}
		raw, _ := os.ReadFile(filepath.Join(w.questionsDir(), files[0]))
		if err := json.Unmarshal(raw, &item); err != nil || item.Version != 1 || item.ID != id || item.Kind != "question" ||
			item.Title != "Slow one" || len(item.Question.Questions) != 1 {
			t.Errorf("question file = %s (%v), want version 1, the id, kind question, the title and its question", raw, err)
		}
		if _, err := time.Parse(time.RFC3339, item.Created); err != nil {
			t.Errorf("created = %q, want an RFC 3339 time", item.Created)
		}

		p := w.botStart(env, "", "chat", "ask", "--wait", id)
		time.Sleep(500 * time.Millisecond)
		p.failIfExited("an answer that has not been written")
		atomicWrite(t, filepath.Join(w.questionsDir(), id+".answer.json"), answerFile(id, "Freeform"))
		got := p.wait()
		w.expectExit(got, 0, "chat ask --wait after the answer")
		if !strings.Contains(got.stdout, `"Freeform"`) {
			t.Errorf("--wait printed %q, want the answer file", got.stdout)
		}
	})

	t.Run("--wait for a question that is gone exits 4 at once", func(t *testing.T) {
		w := w.at(t)
		start := time.Now()
		r := w.botCLI(env, "", "chat", "ask", "--wait", "gone-1")
		w.expectExit(r, 4, "chat ask --wait of an unknown id")
		if time.Since(start) > 5*time.Second {
			t.Errorf("--wait of an unknown id took %s, want it at once", time.Since(start))
		}
	})

	t.Run("a question withdrawn while waiting exits 4", func(t *testing.T) {
		w := w.at(t)
		p := w.botStart(env, askStdin("Withdrawn one"), "chat", "ask")
		deadline := time.Now().Add(10 * time.Second)
		var name string
		for name == "" {
			for _, f := range w.questionFiles() {
				if f != id+".json" && f != id+".answer.json" && strings.HasSuffix(f, ".json") && !strings.Contains(f, "answer") {
					name = f
				}
			}
			p.failIfExited("the question file")
			if time.Now().After(deadline) {
				t.Fatalf("chat ask wrote no question file within 10s: %v", w.questionFiles())
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := os.Remove(filepath.Join(w.questionsDir(), name)); err != nil {
			t.Fatal(err)
		}
		r := p.wait()
		w.expectExit(r, 4, "chat ask after its question file was removed")
		if !strings.Contains(strings.ToLower(r.both()), "withdrawn") {
			t.Errorf("exit 4 said %q, want it to say the question was withdrawn", r.both())
		}
	})
}

// New chat withdraws every question, answered or not, whether the session is
// reset or ended and started again.
func TestAdmiralBot_NewChatWithdrawsChatQuestions(t *testing.T) {
	t.Parallel()
	c := newBotWorld(t, botOpts{})
	w := c.world
	// New chat needs the chat's transcript recorded (a SessionStart), or it would
	// type /clear into the trust dialog.
	w.writeCue(sessionStartCue(filepath.Join(c.cfgDir, "projects", "p", "t.jsonl"), "startup"))
	w.start()
	ws := dialWS(t, w.in.instance)
	c.start()
	waitQuestions(t, w.in.LocalURL, nil, "the chat started (its transcript recorded)", func(s botSession) bool { return s.Running && s.Error == "" })

	p := w.botStart(w.botEnv(), askStdin("Will be withdrawn"), "chat", "ask")
	waitQuestions(t, w.in.LocalURL, p, "the question", hasQuestions(1))
	waitBotWS(t, ws, "the question", func(m botSession) bool { return len(m.Questions) == 1 })

	if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/new", nil); code != http.StatusOK {
		t.Fatalf("POST /api/chat/chat/new = %d, want 200: %s", code, body)
	}
	r := p.wait()
	w.expectExit(r, 4, "chat ask when New chat withdrew its question")
	if s := fetchBot(t, w.in.LocalURL); len(s.Questions) != 0 {
		t.Errorf("right after New chat: questions %v, want none", s.ids())
	}
	waitBotWS(t, ws, "no questions", hasQuestions(0))
	if got := w.questionFiles(); len(got) != 0 {
		t.Errorf("New chat left %v in chat/questions", got)
	}

	// Starting a fresh session after the old one ended withdraws them too.
	p2 := w.botStart(w.botEnv(), askStdin("Withdrawn by a fresh start"), "chat", "ask")
	waitQuestions(t, w.in.LocalURL, p2, "the second question", hasQuestions(1))
	w.mustTmux("kill-session", "-t", "="+chatSessionName)
	waitQuestions(t, w.in.LocalURL, nil, "the chat no longer running", func(s botSession) bool { return !s.Running })
	c.start()
	w.expectExit(p2.wait(), 4, "chat ask when a fresh session withdrew its question")
	if s := fetchBot(t, w.in.LocalURL); len(s.Questions) != 0 || len(w.questionFiles()) != 0 {
		t.Errorf("after a fresh start: questions %v, files %v, want none", s.ids(), w.questionFiles())
	}
}

// A link, an oversized file and a link or an oversized file standing in for an
// answer are skipped; a real question is served beside them.
func TestAdmiralBot_ChatQuestionsAreReadDefensively(t *testing.T) {
	t.Parallel()
	c := newBotWorld(t, botOpts{})
	w := c.world
	w.start()
	c.start()

	itemJSON := func(id, title string) string {
		return jsonLine(map[string]any{"version": 1, "id": id, "kind": "question", "created": "2026-10-22T10:00:00Z", "title": title,
			"question": map[string]any{"questions": []any{map[string]any{"id": "1", "text": "Which?", "multi": false,
				"options": []any{map[string]any{"label": "A"}}}}}})
	}
	// Planted before the real question, so a bad file that hid it would show.
	target := filepath.Join(w.root, "target", "link-1.json")
	atomicWrite(t, target, itemJSON("link-1", "Through a link"))
	if err := os.MkdirAll(w.questionsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(w.questionsDir(), "link-1.json")); err != nil {
		t.Fatal(err)
	}
	atomicWrite(t, filepath.Join(w.questionsDir(), "big-1.json"), itemJSON("big-1", strings.Repeat("x", 1<<20+10)))

	p := w.botStart(w.botEnv(), askStdin("The real one"), "chat", "ask")
	s := waitQuestions(t, w.in.LocalURL, p, "the real question", func(s botSession) bool {
		for _, q := range s.Questions {
			if qStr(q, "title") == "The real one" {
				return true
			}
		}
		return false
	})
	s = fetchBot(t, w.in.LocalURL)
	if len(s.Questions) != 1 || qStr(s.Questions[0], "title") != "The real one" {
		t.Fatalf("questions = %v, want only the real one (the link and the oversized file skipped)", s.ids())
	}
	id := qStr(s.Questions[0], "id")

	// A link in place of the answer counts as no answer.
	good := jsonLine(map[string]any{"version": 1, "id": id, "kind": "question", "answered": "2026-10-22T10:00:00Z",
		"answers": map[string]any{"1": map[string]any{"choice": "A"}}})
	answerTarget := filepath.Join(w.root, "target", "answer.json")
	atomicWrite(t, answerTarget, good)
	if err := os.Symlink(answerTarget, filepath.Join(w.questionsDir(), id+".answer.json")); err != nil {
		t.Fatal(err)
	}
	s = fetchBot(t, w.in.LocalURL)
	if len(s.Questions) != 1 || string(s.Questions[0]["answer"]) != "null" || s.State != "waiting" {
		t.Errorf("with a link for its answer: %+v, want the question still open and the state waiting", s)
	}
	if p.exited() {
		t.Errorf("chat ask exited %d on an answer that is a link", p.result().code)
	}

	// An oversized answer counts as no answer either.
	if err := os.Remove(filepath.Join(w.questionsDir(), id+".answer.json")); err != nil {
		t.Fatal(err)
	}
	atomicWrite(t, filepath.Join(w.questionsDir(), id+".answer.json"), jsonLine(map[string]any{"version": 1, "id": id, "kind": "question",
		"answers": map[string]any{"1": map[string]any{"choice": "A", "other": strings.Repeat("y", 1<<20+10)}}}))
	s = fetchBot(t, w.in.LocalURL)
	if len(s.Questions) != 1 || string(s.Questions[0]["answer"]) != "null" {
		t.Errorf("with an oversized answer: %+v, want the question still open", s)
	}
}

// A quick ship AdmiralBot launches is told to finish with a PR and an Admiral report, so it moors; a scout is not.
func TestAdmiralBot_AQuickShipFollowsAdmiralsClosingStepsToAPRAndMoors(t *testing.T) {
	t.Parallel()
	const text = "Read the logs and fix the 500 in widgets"
	var g *fakeGH
	c := newBotWorld(t, botOpts{
		chatShipCue: []string{"record", "bracketedpaste", "hook working", "wait go", `event Stop {"background_tasks":[]}`},
		sources: func(w *world) string {
			g = w.newFakeGH()
			g.token("", "fake-token-chat")
			return rawSourcesIn(chatFleet, srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Settings: map[string]any{"views": []any{}}})
		}})
	w := c.world
	prompt := `event UserPromptSubmit {"prompt":"fix the 500 in widgets"}`
	w.writeCue(prompt, "mcp quick", prompt, "mcp scout")
	w.checkoutOf(w.work, "widgets", "acme/widgets")
	g.prState(mooredPR, "OPEN")
	w.proveGuard(chatFleet)
	if err := capabilities.Open(w.home).Record(chatFleet, "claude", fakeVersion, capabilities.ScoutGuard,
		capabilities.Result{Passed: true, At: time.Now(), Detail: "seeded by the test"}); err != nil {
		t.Fatal(err)
	}
	w.start()
	c.start()
	dir := filepath.Join(w.work, "widgets")

	shipOf := func(skip string) shipJSON {
		var found shipJSON
		waitCond(t, "a chat ship", func() bool {
			for _, s := range w.in.ships(t) {
				if s.Fleet == chatFleet && s.Via == "admiralbot" && s.Session != skip {
					found = s
					return true
				}
			}
			return false
		})

		return found
	}
	promptOf := func(s shipJSON) string {
		args := strings.Split(strings.TrimSuffix(w.waitFile(s.Session, "argv"), "\x00"), "\x00")
		return args[len(args)-1]
	}

	r := c.mcpCall("quick", "launch_ship", map[string]any{"harness": "claude", "dir": dir, "skill": "none", "text": text, "reason": "to fix the 500 in widgets"})
	if r.IsError {
		t.Fatalf("launch_ship was refused: %s", r.text())
	}
	ship := shipOf("")
	got := promptOf(ship)
	if !strings.HasPrefix(got, text) {
		t.Errorf("the quick ship's prompt = %q, want it to start with AdmiralBot's launch text", got)
	}
	closing := strings.TrimPrefix(got, text)
	if !strings.Contains(closing, "ADMIRAL_REPORT") {
		t.Errorf("the text after the launch text = %q, want Admiral's closing steps naming ADMIRAL_REPORT", closing)
	}

	// The fake harness opens no PR and writes no report, so the test plays the ship following its closing steps.
	w.finishTurn(ship, mooredReport(mooredPR))
	w.wantMoored(ship)
	var line *chatMsg
	for _, m := range c.session().Messages {
		if m.Kind == "launch_line" {
			line = &m
		}
	}
	if line == nil {
		t.Errorf("the fleet chat holds no launch_line entry after the launch: %+v", c.session().Messages)
	} else if !strings.Contains(string(line.Card), "to fix the 500 in widgets") || !strings.Contains(string(line.Card), ship.Name) || strings.Contains(string(line.Card), "ADMIRAL_REPORT") {
		t.Errorf("launch_line card = %s, want the ship's name and reason and no closing steps", line.Card)
	}

	t.Run("a scout's prompt is the launch text alone", func(t *testing.T) {
		const scoutText = "Find where widgets logs the 500"
		r := c.mcpCall("scout", "launch_ship", map[string]any{"harness": "claude", "dir": dir, "scout": true, "text": scoutText, "reason": "to find the 500"})
		if r.IsError {
			t.Fatalf("scout launch_ship was refused: %s", r.text())
		}
		got := promptOf(shipOf(ship.Session))
		if !strings.HasPrefix(got, scoutText) || strings.Contains(got, "ADMIRAL_REPORT") || strings.Contains(got, "full URL") {
			t.Errorf("the scout's prompt = %q, want AdmiralBot's text and no closing steps", got)
		}
	})
}
