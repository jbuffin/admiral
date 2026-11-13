package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// chatFleet is the fleet the single-chat worlds give their chat to.
const chatFleet = "chat"

// chatSessionName is the tmux session of chatFleet's chat.
var chatSessionName = chatSessionFor(chatFleet)

func chatSessionFor(fleet string) string { return "_chat-" + fleet }

type chatMsg struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // "message" (or empty), "question" or "reset"
	Role string `json:"role"`
	Text string `json:"text"`
	At   string `json:"at"`
}

type chatSession struct {
	Type     string    `json:"type"`
	Fleet    string    `json:"fleet"`
	Running  bool      `json:"running"`
	State    string    `json:"state"`
	Messages []chatMsg `json:"messages"`
	Error    string    `json:"error"`
}

// chatWorld is a world whose config has one fleet, "chat", for the chat only,
// so no ship shares its cue file.
type chatWorld struct {
	*world
	cfgDir string // the dock's CLAUDE_CONFIG_DIR
}

// newChatWorld writes the config (chat on opus/high) and the cue file. A nil
// cue writes no cue file. link makes the dock's CLAUDE_CONFIG_DIR a symlink to
// the real directory.
func newChatWorld(t *testing.T, link bool, cue ...string) *chatWorld {
	t.Helper()
	w := newWorld(t)
	c := &chatWorld{world: w, cfgDir: filepath.Join(w.root, "cfg")}
	if err := os.MkdirAll(filepath.Join(c.cfgDir, "projects", "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	dockCfg := c.cfgDir
	if link {
		dockCfg = filepath.Join(w.root, "cfglink")
		if err := os.Symlink(c.cfgDir, dockCfg); err != nil {
			t.Fatal(err)
		}
	}
	if cue != nil {
		w.writeCue(cue...)
	}
	// No ADMIRAL_* in the dock env: the chat must not get a ship's variables.
	env := map[string]string{"FAKE_RECORD_DIR": w.rec, "FAKE_CUE": w.cue, "FLEET_VAR": "fleet-value", "CLAUDE_CONFIG_DIR": dockCfg}
	fleets := map[string]any{"chat": map[string]any{"name": "Chat", "search_roots": []string{w.work},
		"claude": map[string]any{"executable": w.fakeExe("claude"), "args": []string{"--fa-chat"}, "env": env},
		"chat":   map[string]any{"model": "opus", "effort": "high"}}}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": fleets})
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	return c
}

func (c *chatWorld) recDir() string { return filepath.Join(c.rec, chatSessionName) }

// touch creates <rec>/_chat/<name>, which releases a `wait <name>` cue.
func (c *chatWorld) touch(name string) {
	c.t.Helper()
	if err := os.MkdirAll(c.recDir(), 0o755); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.recDir(), name), nil, 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *chatWorld) stdin() string {
	data, _ := os.ReadFile(filepath.Join(c.recDir(), "stdin"))
	return string(data)
}

func (c *chatWorld) waitStdin(want string) {
	c.t.Helper()
	var got string
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got = c.stdin(); got == want {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("within 5s the fake's terminal input never became %q; it is %q", want, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *chatWorld) waitStdinOneOf(wants ...string) {
	c.t.Helper()
	var got string
	deadline := time.Now().Add(5 * time.Second)
	for {
		got = c.stdin()
		for _, w := range wants {
			if got == w {
				return
			}
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("within 5s the fake's terminal input never became one of %q; it is %q", wants, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *chatWorld) waitEvents(n int) {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(c.recDir(), "events-done"))
		if strings.Count(string(data), "\n") >= n {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("within 10s the fake never ran %d hook cues; %s", n, c.diag())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// diag is what the fake recorded about its hook handlers.
func (c *chatWorld) diag() string {
	var sb strings.Builder
	for _, f := range []string{"handlers", "event-error", "handler-1.stderr", "handler-2.stderr", "handler-3.stderr", "mcp-error", "reply-error", "stdin"} {
		data, _ := os.ReadFile(filepath.Join(c.recDir(), f))
		if len(data) > 0 {
			fmt.Fprintf(&sb, "%s: %q; ", f, data)
		}
	}
	return "fake's handlers: " + sb.String()
}

// moveRecord sets the first run's record aside so the next run's files can't be
// mistaken for it.
func (c *chatWorld) moveRecord(suffix string) {
	c.t.Helper()
	if err := os.Rename(c.recDir(), c.recDir()+suffix); err != nil {
		c.t.Fatal(err)
	}
}

func (c *chatWorld) start() chatSession {
	c.t.Helper()
	code, body := send(c.t, http.MethodPost, c.in.LocalURL+"/api/chat/chat/start", nil)
	if code != http.StatusOK {
		c.t.Fatalf("POST /api/chat/chat/start = %d, want 200: %s", code, body)
	}
	var r struct {
		Running bool `json:"running"`
	}
	if json.Unmarshal([]byte(body), &r) != nil || !r.Running {
		c.t.Fatalf(`POST /api/chat/chat/start answered %s, want {"running": true}`, body)
	}
	return c.session()
}

func (c *chatWorld) session() chatSession {
	c.t.Helper()
	return fetchChat(c.t, c.in.LocalURL)
}

func fetchChat(t *testing.T, base string) chatSession { return fetchFleetChat(t, base, chatFleet) }

func fetchFleetChat(t *testing.T, base, fleet string) chatSession {
	t.Helper()
	code, body := send(t, http.MethodGet, base+"/api/chat/"+fleet+"/session", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/chat/%s/session = %d, want 200: %s", fleet, code, body)
	}
	var s chatSession
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("GET /api/chat/%s/session is not JSON: %v\n%s", fleet, err, body)
	}
	return s
}

func (c *chatWorld) waitSession(d time.Duration, what string, pred func(chatSession) bool) chatSession {
	c.t.Helper()
	deadline := time.Now().Add(d)
	for {
		s := c.session()
		if pred(s) {
			return s
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("within %s GET /api/chat/chat/session never showed %s; last: %+v; %s", d, what, s, c.diag())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// stays requires pred to hold for the whole of d.
func (c *chatWorld) stays(d time.Duration, what string, pred func(chatSession) bool) {
	c.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if s := c.session(); !pred(s) {
			c.t.Fatalf("expected %s throughout %s, got %+v", what, d, s)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// texts lists the text messages only; a reset marker or a question card is not one.
func texts(msgs []chatMsg) []string {
	out := []string{}
	for _, m := range msgs {
		if m.Kind == "" || m.Kind == "message" {
			out = append(out, m.Role+": "+m.Text)
		}
	}
	return out
}

func resets(msgs []chatMsg) int {
	n := 0
	for _, m := range msgs {
		if m.Kind == "reset" {
			n++
		}
	}
	return n
}

func hasResets(n int) func(chatSession) bool {
	return func(s chatSession) bool { return resets(s.Messages) == n }
}

func uniqueIDs(msgs []chatMsg) bool {
	seen := map[string]bool{}
	for _, m := range msgs {
		if m.ID != "" {
			if seen[m.ID] {
				return false
			}
			seen[m.ID] = true
		}
	}
	return true
}

// messagesEndWith holds when the text messages end with want and no message is served twice.
func messagesEndWith(want ...string) func(chatSession) bool {
	return func(s chatSession) bool {
		got := texts(s.Messages)
		return uniqueIDs(s.Messages) && len(got) >= len(want) && sameStrings(got[len(got)-len(want):], want)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func messagesAre(want ...string) func(chatSession) bool {
	return func(s chatSession) bool { return sameStrings(texts(s.Messages), want) }
}

func waitChatWS(t *testing.T, c *websocket.Conn, d time.Duration, what string, pred func(chatSession) bool) chatSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var last string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within %s the websocket never delivered a `chat` message with %s (last chat message: %s): %v", d, what, last, err)
		}
		var m chatSession
		if json.Unmarshal(data, &m) != nil || m.Type != "chat" {
			continue
		}
		last = string(data)
		if pred(m) {
			return m
		}
	}
}

// Transcript lines: invented text in Claude Code's shapes.

func jsonLine(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func userLine(uuid string, content any, extra map[string]any) string {
	m := map[string]any{"type": "user", "uuid": uuid, "timestamp": "2026-01-02T03:04:05.000Z",
		"message": map[string]any{"role": "user", "content": content}}
	for k, v := range extra {
		m[k] = v
	}
	return jsonLine(m)
}

func assistantLine(uuid, msgID string, block map[string]any) string {
	return jsonLine(map[string]any{"type": "assistant", "uuid": uuid, "timestamp": "2026-01-02T03:04:06.000Z",
		"message": map[string]any{"role": "assistant", "id": msgID, "content": []any{block}}})
}

func appendFile(t *testing.T, path string, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	appendFile(t, path, strings.Join(lines, "\n")+"\n")
}

func sessionStartCue(path, source string) string {
	return `event SessionStart ` + jsonLine(map[string]any{"transcript_path": path, "source": source})
}

// The chat starts on demand, is never a ship, launches with its contract, and
// its terminal is reachable through the bridge only while it runs.
func TestChatSession_StartIsNotAShipAndLaunchContract(t *testing.T) {
	t.Parallel()
	c := newChatWorld(t, false)
	w := c.world
	w.start()

	if s := c.session(); s.Running || s.State != "" {
		t.Fatalf("before the chat starts: %+v, want running false and state \"\"", s)
	}
	for _, path := range []string{"/api/chat/chat/messages", "/api/chat/chat/new"} {
		if code, _ := send(t, http.MethodPost, w.in.LocalURL+path, map[string]any{"text": "hello"}); code != http.StatusConflict {
			t.Errorf("POST %s while the chat is not running = %d, want 409", path, code)
		}
	}
	// A tmux server that outlived another Admiral, or was started from a ship's shell.
	// Admiral's host starts the server a moment after the page is up: wait for it.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := w.tmux("list-sessions"); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("within 5s Admiral's tmux server never came up: %v", err)
		}
	}
	for _, kv := range [][2]string{{"ADMIRAL_SHIP", "leak-ship"}, {"ADMIRAL_REPORT", "/leak/report"}, {"ADMIRAL_INBOX", "/leak/inbox"},
		{"ADMIRAL_AUTOPILOT", "1"}, {"ADMIRAL_AUTOPILOT_LOW_STAKES", "1"},
		{"ADMIRAL_LOW_STAKES", "never"}, {"ADMIRAL_AUTO_MERGE", "squash"}} {
		w.mustTmux("set-environment", "-g", kv[0], kv[1])
	}

	if s := c.start(); !s.Running {
		t.Errorf("after start: %+v, want running", s)
	}
	pane := w.mustTmux("display-message", "-p", "-t", w.target(chatSessionName), "#{pane_pid}")

	t.Run("argv, env and cwd", func(t *testing.T) {
		r := w.at(t).record(chatSessionName)
		idx := func(a string) int {
			for i, x := range r.argv {
				if x == a {
					return i
				}
			}
			return -1
		}
		if !hasPair(r.argv, "--model", "opus") || !hasPair(r.argv, "--effort", "high") {
			t.Errorf("argv = %q, want --model opus and --effort high as separate elements", r.argv)
		}
		settings := ""
		if i := idx("--settings"); i >= 0 && i+1 < len(r.argv) {
			settings = r.argv[i+1]
		}
		if settings == "" {
			t.Fatalf("argv = %q has no --settings <bundle>", r.argv)
		}
		// ADR 0020: the chat takes the dock's executable and env, never its args (a
		// ship's args commonly widen permissions), so its own argv is all it gets.
		if idx("--fa-chat") >= 0 || !(idx("--model") >= 0 && idx("--model") < idx("--settings")) {
			t.Errorf("argv = %q, want no dock args, then the chat's own, then the hook flags", r.argv)
		}
		if settings == filepath.Join(w.home, "harness", "claude-settings.json") {
			t.Errorf("the chat uses the ships' bundle %s; it needs its own", settings)
		}
		raw, err := os.ReadFile(settings)
		if err != nil {
			t.Fatalf("the chat bundle %s: %v", settings, err)
		}
		var b struct {
			Hooks map[string]json.RawMessage `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatalf("the chat bundle is not JSON: %v", err)
		}
		for _, ev := range []string{"SessionStart", "UserPromptSubmit", "Stop", "Notification"} {
			if _, ok := b.Hooks[ev]; !ok {
				t.Errorf("the chat bundle has no %s hook", ev)
			}
		}
		for _, ev := range []string{"SubagentStart", "SubagentStop"} {
			if _, ok := b.Hooks[ev]; ok {
				t.Errorf("the chat bundle registers %s, which would write crew facts on the chat pane", ev)
			}
		}
		if strings.Contains(string(b.Hooks["Notification"]), "idle_prompt") {
			t.Errorf("the chat bundle's Notification hook takes idle_prompt, which would make every idle chat waiting")
		}

		if r.env["FLEET_VAR"] != "fleet-value" {
			t.Errorf("env FLEET_VAR = %q, want the dock's env to reach the chat", r.env["FLEET_VAR"])
		}
		if r.env["ADMIRAL_HOME"] != w.home {
			t.Errorf("env ADMIRAL_HOME = %q, want %q", r.env["ADMIRAL_HOME"], w.home)
		}
		if !strings.HasPrefix(r.env["PATH"], filepath.Join(w.home, "bin")+":") {
			t.Errorf("env PATH = %q, want $ADMIRAL_HOME/bin first", r.env["PATH"])
		}
		for k := range r.env {
			// ADMIRAL_PORT, ADMIRAL_CHAT_FLEET and ADMIRAL_CHAT_HARNESS are the chat's own: the port Admiral listens on,
			// the fleet it answers for and the harness whose hooks may print context.
			if strings.HasPrefix(k, "ADMIRAL_") && k != "ADMIRAL_HOME" && !(k == "ADMIRAL_PORT" && r.env[k] == w.in.Port) &&
				!(k == "ADMIRAL_CHAT_FLEET" && r.env[k] == chatFleet) && !(k == "ADMIRAL_CHAT_HARNESS" && r.env[k] == "claude") {
				t.Errorf("env has %s=%q; the tmux server's global environment must not reach the chat", k, r.env[k])
			}
		}
		if r.env["ADMIRAL_CHAT_FLEET"] != chatFleet {
			t.Errorf("env ADMIRAL_CHAT_FLEET = %q, want %q: `admiral chat ask` finds its fleet there", r.env["ADMIRAL_CHAT_FLEET"], chatFleet)
		}
		if r.env["ADMIRAL_CHAT_HARNESS"] != "claude" {
			t.Errorf("env ADMIRAL_CHAT_HARNESS = %q, want claude: the hooks print the brief and digest only for it", r.env["ADMIRAL_CHAT_HARNESS"])
		}
		if want := filepath.Join(w.home, "chat", chatFleet); r.cwd != want {
			t.Errorf("cwd = %q, want %q", r.cwd, want)
		}
	})

	t.Run("a second start leaves the same session", func(t *testing.T) {
		w := w.at(t)
		code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/start", nil)
		if code != http.StatusOK || !strings.Contains(body, `"running"`) {
			t.Fatalf("second POST /api/chat/chat/start = %d %s, want 200 with running", code, body)
		}
		if got := w.mustTmux("display-message", "-p", "-t", w.target(chatSessionName), "#{pane_pid}"); got != pane {
			t.Errorf("the chat's pane pid changed from %s to %s: a second start must leave it alone", pane, got)
		}
	})

	t.Run("never a ship", func(t *testing.T) {
		w := w.at(t)
		ws := dialWS(t, w.in.instance)
		onWire := waitShipsMsg(t, ws, 5*time.Second, "any ships message", func([]shipJSON) bool { return true })
		for _, s := range append(onWire, w.in.ships(t)...) {
			if s.Session == chatSessionName {
				t.Errorf("%s is in the ships list: %+v", chatSessionName, s)
			}
		}
		if r := w.admiral("ships", "--port", w.in.Port); strings.Contains(r.stdout, chatSessionName) {
			t.Errorf("admiral ships lists the chat:\n%s", r.stdout)
		}
		dir, rec := w.installFakeTmux()
		r := w.admiralWith(cliOpts{path: []string{dir}, env: []string{"FAKE_TMUX_RECORD=" + rec}}, "attach", chatSessionName)
		w.expectExit(r, 1, "attach "+chatSessionName)
		if !strings.Contains(r.both(), chatSessionName) {
			t.Errorf("the refusal should name %s, got:\n%s", chatSessionName, r.both())
		}
		if got := attachCalls(w, rec); len(got) != 0 {
			t.Errorf("a refused attach still ran tmux attach: %q", got)
		}
		// The chat's Claude can run tmux on its own session; it must not make itself a ship.
		w.mustTmux("set-option", "-t", "="+chatSessionName+":", "@admiral-name", "Sneaky")
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			for _, s := range w.in.ships(t) {
				if s.Session == chatSessionName {
					t.Fatalf("setting @admiral-name made the chat a ship: %+v", s)
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	})

	t.Run("the bridge follows the session", func(t *testing.T) {
		w := w.at(t)
		b, _ := w.joinBridge(t, chatSessionName, 80, 24)
		b.close()
		w.mustTmux("kill-session", "-t", "="+chatSessionName)
		c.waitSession(5*time.Second, "the chat no longer running", func(s chatSession) bool { return !s.Running })
		expectBridgeRefused(t, w.in.LocalURL, chatSessionName, "", nil, "", http.StatusNotFound)
	})
}

func TestChatSession_ConversationSendNewChatAndRestart(t *testing.T) {
	t.Parallel()
	// The cue file names the transcripts, so it is written once the config dir exists.
	c := newChatWorld(t, false)
	tp1 := filepath.Join(c.cfgDir, "projects", "p", "first.jsonl")
	tp2 := filepath.Join(c.cfgDir, "projects", "p", "second.jsonl")
	c.writeCue(
		"record",
		"bracketedpaste",
		"wait go-start",
		sessionStartCue(tp1, "startup"),
		"wait go-prompt",
		`event UserPromptSubmit {"prompt":"hello"}`,
		"wait go-perm",
		`event Notification {"notification_type":"permission_prompt"}`,
		"wait go-stop",
		`event Stop {"background_tasks":[]}`,
		"wait go-clear",
		sessionStartCue(tp2, "clear"),
	)
	w := c.world
	w.start()
	ws1, ws2 := dialWS(t, w.in.instance), dialWS(t, w.in.instance)
	c.start()
	c.waitFile(chatSessionName, "recording")

	t.Run("send is refused before the first SessionStart", func(t *testing.T) {
		code, _ := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/messages", map[string]any{"text": "too early"})
		if code != http.StatusConflict {
			t.Errorf("POST /api/chat/chat/messages before SessionStart = %d, want 409", code)
		}
		s := c.session()
		if !s.Running || s.Error != "Open the terminal to finish starting the chat" {
			t.Errorf("session before SessionStart = %+v, want running with the error to open the terminal", s)
		}
		if got := c.stdin(); got != "" {
			t.Errorf("the refused message reached the pane: %q", got)
		}
	})

	// Batch one is on disk before the hook fires; batch two arrives after.
	long := strings.Repeat("x", 1<<20+10)
	appendLines(t, tp1,
		`{"type":"mode","mode":"default"}`,
		`{"type":"file-history-snapshot","messageId":"snap"}`,
		userLine("u-1", "Please summarise the harbour log", nil),
		assistantLine("a-1", "msg_1", map[string]any{"type": "thinking", "thinking": "THINKING-NOISE"}),
		assistantLine("a-2", "msg_1", map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Read", "input": map[string]any{"file_path": "/nowhere"}}),
		userLine("u-2", []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "TOOLRESULT-NOISE"}}, nil),
		assistantLine("a-3", "msg_1", map[string]any{"type": "text", "text": "The log has three entries."}),
		`this is not json {`,
		userLine("u-3", "META-NOISE", map[string]any{"isMeta": true}),
		userLine("u-3b", "COMPACT-NOISE", map[string]any{"isCompactSummary": true, "isVisibleInTranscriptOnly": true}),
		jsonLine(map[string]any{"type": "assistant", "uuid": "a-4", "isSidechain": true, "message": map[string]any{"role": "assistant", "id": "msg_9", "content": []any{map[string]any{"type": "text", "text": "SIDECHAIN-NOISE"}}}}),
		jsonLine(map[string]any{"type": "system", "uuid": "s-1", "subtype": "local_command", "content": "SYSTEM-NOISE"}),
		userLine("u-3c", "<task-notification>Subagent finished</task-notification>", nil),
		userLine("u-3d", "Another Claude session sent a message: META-SESSION-NOISE", map[string]any{"isMeta": true}),
		userLine("u-4", []any{map[string]any{"type": "text", "text": "Now list the ships"}}, nil),
	)
	first := []string{"user: Please summarise the harbour log", "assistant: The log has three entries.", "user: <task-notification>Subagent finished</task-notification>", "user: Now list the ships"}

	t.Run("the transcript becomes the conversation", func(t *testing.T) {
		c := &chatWorld{world: w.at(t), cfgDir: c.cfgDir}
		c.touch("go-start")
		got := c.waitSession(10*time.Second, "the first four messages", messagesAre(first...))
		if got.Error != "" || !got.Running {
			t.Errorf("after SessionStart: %+v, want running with no error", got)
		}
		for i, id := range []string{"u-1", "a-3", "u-3c", "u-4"} {
			if m := got.Messages[i]; m.ID != id || !strings.HasPrefix(m.At, "2026-01-02T03:04:0") {
				t.Errorf("message %d = %+v, want id %s (the line's uuid) and its timestamp as at", i, m, id)
			}
		}
		// Every browser gets the update.
		waitChatWS(t, ws1, 5*time.Second, "the first four messages", messagesAre(first...))
		waitChatWS(t, ws2, 5*time.Second, "the first four messages", messagesAre(first...))

		// More lines, then a line cut in two: it must be read whole once finished.
		appendLines(t, tp1,
			userLine("u-5", "<command-name>/clear</command-name>\n<command-message>clear</command-message>", nil),
			userLine("u-6", "<local-command-stdout></local-command-stdout>", nil),
			assistantLine("a-5", "msg_2", map[string]any{"type": "text", "text": "Two ships are afloat."}),
			userLine("u-7", long, nil),
		)
		part := userLine("u-8", "Thanks, that is all", nil)
		appendFile(t, tp1, part[:len(part)/2])
		second := append(append([]string{}, first...), "assistant: Two ships are afloat.")
		c.waitSession(10*time.Second, "the new assistant message and nothing else", messagesAre(second...))
		appendFile(t, tp1, part[len(part)/2:]+"\n")
		c.waitSession(10*time.Second, "the line finished", messagesAre(append(second, "user: Thanks, that is all")...))
	})

	t.Run("state follows the hooks", func(t *testing.T) {
		c := &chatWorld{world: w.at(t), cfgDir: c.cfgDir}
		for _, step := range []struct{ file, state string }{{"go-prompt", "working"}, {"go-perm", "waiting"}, {"go-stop", "idle"}} {
			c.touch(step.file)
			c.waitSession(5*time.Second, "state "+step.state, func(s chatSession) bool { return s.State == step.state })
			waitChatWS(t, ws2, 5*time.Second, "state "+step.state, func(s chatSession) bool { return s.State == step.state })
		}
	})

	t.Run("send is validated", func(t *testing.T) {
		for name, text := range map[string]string{"empty": "", "blank": " \n\t ", "too long": strings.Repeat("x", 64*1024)} {
			if code, _ := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/messages", map[string]any{"text": text}); code != http.StatusBadRequest {
				t.Errorf("POST /api/chat/chat/messages with %s text = %d, want 400", name, code)
			}
		}
	})

	want := "\x1b[200~line one\nline two\x1b[201~\r"
	t.Run("send pastes one bracketed message then Enter", func(t *testing.T) {
		c := &chatWorld{world: w.at(t), cfgDir: c.cfgDir}
		code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/messages", map[string]any{"text": "line one\nline two"})
		if code < 200 || code > 299 {
			t.Fatalf("POST /api/chat/chat/messages = %d, want 2xx: %s", code, body)
		}
		// tmux 3.8 on this Mac delivers `paste-buffer -p -r` as ESC[200~ text ESC[201~
		// with the LF kept, and send-keys Enter as CR.
		c.waitStdin(want)
	})

	tpClear := []string{
		userLine("c-1", "<command-name>/clear</command-name>\n<command-message>clear</command-message>", nil),
		userLine("c-2", "<local-command-stdout></local-command-stdout>", nil),
	}
	appendLines(t, tp2, tpClear...)

	history := append(append([]string{}, first...), "assistant: Two ships are afloat.", "user: Thanks, that is all")
	t.Run("New chat keeps the history and adds a marker", func(t *testing.T) {
		c := &chatWorld{world: w.at(t), cfgDir: c.cfgDir}
		code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/new", nil)
		if code != http.StatusOK {
			t.Fatalf("POST /api/chat/chat/new = %d, want 200: %s", code, body)
		}
		if s := c.session(); !messagesAre(history...)(s) || resets(s.Messages) != 1 {
			t.Errorf("right after New chat: %v with %d reset markers, want the history and one marker", texts(s.Messages), resets(s.Messages))
		}
		waitChatWS(t, ws1, 3*time.Second, "the history and a marker", func(s chatSession) bool { return messagesAre(history...)(s) && resets(s.Messages) == 1 })
		waitChatWS(t, ws2, 3*time.Second, "the history and a marker", func(s chatSession) bool { return messagesAre(history...)(s) && resets(s.Messages) == 1 })
		// /clear typed as keys, or pasted as one bracketed paste: either reaches the pane.
		c.waitStdinOneOf(want+"/clear\r", want+"\x1b[200~/clear\x1b[201~\r")
		// The /clear path writes one usage record for the chat, from the file it had before the clear.
		appendLines(t, tp1, `{"type":"cost-state","totalCostUSD":0.5,"modelUsage":{"claude-fake":{"inputTokens":7,"outputTokens":3}}}`)
		c.touch("go-clear")
		c.waitEvents(5)
		c.stays(1500*time.Millisecond, "the same history after SessionStart (clear)", messagesAre(history...))
		got := lastOf(c.waitEvent(chatSessionName, "usage", 1), "usage")
		wantUsage(t, got, "chat", "claude", map[string]any{"input": 7.0, "output": 3.0, "reasoning": 0.0, "cache_read": 0.0, "cache_write": 0.0},
			map[string]any{"amount": 0.5, "basis": "estimate", "incomplete": false})
		holdThat(t, "the clear wrote a second usage record for the chat", func() bool { return usageCount(c.shipLog(chatSessionName)) == 1 })
		appendLines(t, tp2, userLine("u-20", "After the clear", nil))
		c.waitSession(10*time.Second, "the message written after the clear", messagesAre(append(history, "user: After the clear")...))
	})

	t.Run("a restarted Admiral serves the same conversation", func(t *testing.T) {
		c := &chatWorld{world: w.at(t), cfgDir: c.cfgDir}
		c.restart()
		// The log serves the history before the chat is found running again, so wait for both.
		got := c.waitSession(10*time.Second, "the same conversation, running", func(s chatSession) bool {
			return s.Running && messagesAre(append(history, "user: After the clear")...)(s)
		})
		if !got.Running || got.Error != "" || resets(got.Messages) != 1 || !uniqueIDs(got.Messages) {
			t.Errorf("after the restart: %+v, want running with no error, one marker and no message twice", got)
		}
	})
}

// A clear on the same path serves only later lines, and New chat with a changed
// model restarts the session with the new argv (and no --effort when it is empty).
func TestChatSession_NewChatRestartsWhenTheConfigChanged(t *testing.T) {
	t.Parallel()
	c := newChatWorld(t, false)
	w := c.world
	tpA := filepath.Join(c.cfgDir, "projects", "p", "a.jsonl")
	tpB := filepath.Join(c.cfgDir, "projects", "p", "b.jsonl")
	tpC := filepath.Join(c.cfgDir, "projects", "p", "c.jsonl")
	w.writeCue("record", sessionStartCue(tpA, "startup"), "wait go-clear", sessionStartCue(tpA, "clear"),
		"wait go-late", sessionStartCue(tpC, "startup"), "wait go-late-clear", sessionStartCue(tpC, "clear"))
	appendLines(t, tpA,
		userLine("u-1", "Before the clear", nil),
		assistantLine("a-1", "msg_1", map[string]any{"type": "text", "text": "Answer before the clear."}),
	)
	w.start()
	c.start()
	c.waitSession(10*time.Second, "the conversation", messagesAre("user: Before the clear", "assistant: Answer before the clear."))

	// A SessionStart with source clear on the same path serves only lines written after it.
	c.touch("go-clear")
	c.waitEvents(2)
	appendLines(t, tpA, userLine("u-2", "After the same-path clear", nil))
	c.waitSession(10*time.Second, "the later line, with nothing served twice", messagesEndWith("user: After the same-path clear"))

	// A clear before the transcript exists skips nothing: its lines show once it appears.
	c.touch("go-late")
	c.waitEvents(3)
	c.touch("go-late-clear")
	c.waitEvents(4)
	appendLines(t, tpC, userLine("u-3", "Written after the clear", nil))
	c.waitSession(10*time.Second, "the line of a transcript that did not exist at the clear", messagesEndWith("user: Written after the clear"))

	// The chat's cost-state is read from the path recorded before the reset.
	appendLines(t, tpC, `{"type":"cost-state","totalCostUSD":0.5,"modelUsage":{"claude-fake":{"inputTokens":7,"outputTokens":3}}}`)

	// Change the model and the effort, then New chat. The new run records into a
	// fresh _chat directory and fires its own SessionStart.
	if code, body := send(t, http.MethodPut, w.in.LocalURL+"/api/fleets/chat", map[string]any{"chat": map[string]any{"harness": "claude", "model": "sonnet", "effort": ""}}); code != http.StatusOK {
		t.Fatalf("PUT /api/fleets/chat = %d: %s", code, body)
	}
	if got := c.session(); !got.Running {
		t.Fatalf("a config change must not stop a running chat: %+v", got)
	}
	appendLines(t, tpB, userLine("u-30", "Fresh start", nil))
	w.writeCue("record", sessionStartCue(tpB, "startup"))
	c.moveRecord(".run1")
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/new", nil)
	if code != http.StatusOK {
		t.Fatalf("POST /api/chat/chat/new = %d, want 200: %s", code, body)
	}
	if s := c.session(); resets(s.Messages) != 1 {
		t.Errorf("right after New chat: %v, want one reset marker (the kill-and-start path adds it too)", s.Messages)
	}
	r := w.record(chatSessionName)
	if !hasPair(r.argv, "--model", "sonnet") || has(r.argv, "--effort") {
		t.Errorf("the restarted chat's argv = %q, want --model sonnet and no --effort", r.argv)
	}
	c.waitSession(10*time.Second, "the new run's conversation", messagesEndWith("user: Fresh start"))

	// The reset wrote one usage record for the chat, from the transcript it had before the kill.
	got := lastOf(w.waitEvent(chatSessionName, "usage", 1), "usage")
	if got["fleet"] != "chat" || got["harness"] != "claude" || !reflect.DeepEqual(got["tokens"], map[string]any{"input": 7.0, "output": 3.0, "reasoning": 0.0, "cache_read": 0.0, "cache_write": 0.0}) ||
		!reflect.DeepEqual(got["money"], map[string]any{"amount": 0.5, "basis": "estimate", "incomplete": false}) {
		t.Errorf("the chat's usage record = %v, want fleet chat, claude, 7 in 3 out, 0.5 estimate", got)
	}
	holdThat(t, "the reset wrote a second usage record for the chat", func() bool { return usageCount(w.shipLog(chatSessionName)) == 1 })
}

// The transcript path comes from a pane option anything in the chat can write,
// so it is read defensively. A config directory that is
// itself a symlink must not hide a real transcript.
func TestChatSession_OnlyARealTranscriptUnderProjectsIsRead(t *testing.T) {
	t.Parallel()
	c := newChatWorld(t, true)
	w := c.world
	real := filepath.Join(c.cfgDir, "projects", "p", "real.jsonl")
	good := userLine("u-1", "A real line", nil)
	appendLines(t, real, good, `{"type":"user","uuid":"big","message":{"role":"user","content":"`+strings.Repeat("y", 1<<20+10)+`"}}`,
		userLine("u-2", "Another real line", nil))

	appendLines(t, filepath.Join(c.cfgDir, "projects", "p", "viaLink.jsonl"), userLine("v-1", "Read through the link", nil))
	linked := filepath.Join(c.cfgDir, "projects", "p", "linked.jsonl")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(c.cfgDir, "elsewhere", "x.jsonl")
	appendLines(t, outside, userLine("o-1", "OUTSIDE-NOISE", nil))
	realDir := filepath.Join(c.cfgDir, "elsewhere")
	appendLines(t, filepath.Join(realDir, "viadir.jsonl"), userLine("d-1", "DIRLINK-NOISE", nil))
	if err := os.Symlink(realDir, filepath.Join(c.cfgDir, "projects", "dirlink")); err != nil {
		t.Fatal(err)
	}
	w.writeCue("record",
		"wait go-1", sessionStartCue(linked, "startup"),
		"wait go-2", sessionStartCue(outside, "startup"),
		"wait go-3", sessionStartCue(filepath.Join(c.cfgDir, "projects", "dirlink", "viadir.jsonl"), "startup"),
		"wait go-4", sessionStartCue(real, "startup"),
		"wait go-5", sessionStartCue(filepath.Join(w.root, "cfglink", "projects", "p", "viaLink.jsonl"), "startup"))
	w.start()
	c.start()
	c.waitFile(chatSessionName, "recording")

	for i, what := range []string{"a symlinked transcript", "a path outside projects/", "a symlinked directory under projects/"} {
		c.touch(fmt.Sprintf("go-%d", i+1))
		c.waitEvents(i + 1)
		c.stays(1500*time.Millisecond, "no messages for "+what, messagesAre())
		if got := c.session().Error; got != "Admiral can't read this chat's transcript" {
			t.Errorf("for %s the session error = %q, want it to say the transcript can't be read", what, got)
		}
	}
	c.touch("go-4")
	real2 := []string{"user: A real line", "user: Another real line"}
	c.waitSession(10*time.Second, "the real transcript's lines (the over-long line skipped)", messagesAre(real2...))
	// Claude may report the path through the link a dotfiles config dir is; a
	// different transcript named that way must be read too (the dock's
	// CLAUDE_CONFIG_DIR is that link).
	c.touch("go-5")
	c.waitEvents(5)
	// The history keeps the earlier transcript's lines and adds this one's.
	c.waitSession(10*time.Second, "the transcript named through the config dir's link", messagesAre(append(real2, "user: Read through the link")...))
}

func TestChatSession_NeedsTheTokenOverTheLAN(t *testing.T) {
	t.Parallel()
	c := newChatWorld(t, false)
	in := c.world.startLAN(t)
	c.start()
	base := in.lanURL()

	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/chat/chat/session"}, {http.MethodPost, "/api/chat/chat/start"},
		{http.MethodPost, "/api/chat/chat/messages"}, {http.MethodPost, "/api/chat/chat/new"},
	} {
		if resp, body := do(t, r.method, base+r.path, nil); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without the token = %d, want 401: %s", r.method, r.path, resp.StatusCode, body)
		}
	}
	expectBridgeRefused(t, base, chatSessionName, "", nil, "", http.StatusUnauthorized)

	cookie := map[string]string{"Cookie": in.cookieName() + "=" + in.Token}
	if resp, body := do(t, http.MethodGet, base+"/api/chat/chat/session", cookie); resp.StatusCode != http.StatusOK || !strings.Contains(body, `"running":true`) {
		t.Errorf("GET /api/chat/chat/session with the token = %d %s, want 200 with the running chat", resp.StatusCode, body)
	}
	b, resp, err := bridgeDial(t, base, chatSessionName, "", http.Header{"Cookie": {in.cookieName() + "=" + in.Token}}, "")
	if err != nil {
		t.Fatalf("/ws/bridge/_chat with the token: %v (%v)", resp, err)
	}
	b.send(map[string]any{"type": "join", "cols": 80, "rows": 24})
	b.waitJoin(5*time.Second, 1)
}
