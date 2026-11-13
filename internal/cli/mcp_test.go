package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/launcher"
)

const mcpToken = "fake-token-work-2"

type mcpReq struct {
	Method, Path, Query, Actor string
	Body                       map[string]any
}

// admiralStandIn answers GET /api/ships as the Admiral whose home is dir (the probe `admiral mcp` makes) and every other
// request through handle, recording it.
type admiralStandIn struct {
	mu     sync.Mutex
	seen   []mcpReq
	handle func(r mcpReq) (int, string)
	srv    *httptest.Server
	home   string
	port   string
}

func newStandIn(t *testing.T, handle func(r mcpReq) (int, string)) *admiralStandIn {
	t.Helper()
	a := &admiralStandIn{handle: handle, home: t.TempDir()}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/api/ships" && r.Header.Get("X-Admiral-Actor") == "" {
			w.Write([]byte(`{"ships":[],"home":"` + home.ID(a.home) + `"}`))
			return
		}
		s := mcpReq{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Actor: r.Header.Get("X-Admiral-Actor")}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &s.Body)
		a.mu.Lock()
		a.seen = append(a.seen, s)
		a.mu.Unlock()
		code, body := 200, `{}`
		if a.handle != nil {
			code, body = a.handle(s)
		}
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(a.srv.Close)
	u, _ := url.Parse(a.srv.URL)
	a.port = u.Port()
	return a
}

func (a *admiralStandIn) requests() []mcpReq {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]mcpReq(nil), a.seen...)
}

func (a *admiralStandIn) env(t *testing.T, token string) {
	t.Helper()
	t.Setenv("ADMIRAL_ACTOR_TOKEN", token)
	t.Setenv("ADMIRAL_PORT", a.port)
	t.Setenv("ADMIRAL_HOME", a.home)
	t.Setenv("ADMIRAL_CHAT_FLEET", "work")
}

type rpcReply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// serve runs `admiral mcp` over the lines until its stdin ends and returns the replies by id, with stdout for the
// "nothing but JSON-RPC" check.
func serve(t *testing.T, lines ...string) (map[string]rpcReply, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- MCP(nil, IO{In: strings.NewReader(strings.Join(lines, "\n") + "\n"), Out: &out, Err: &errOut})
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("admiral mcp did not end when its input did")
	}
	if strings.Contains(out.String(), mcpToken) || strings.Contains(errOut.String(), mcpToken) {
		t.Error("the actor token reached stdout or stderr")
	}
	replies := map[string]rpcReply{}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var r rpcReply
		var probe map[string]any
		if json.Unmarshal([]byte(l), &probe) != nil || probe["jsonrpc"] != "2.0" {
			t.Fatalf("stdout line %q is not a JSON-RPC 2.0 message", l)
		}
		_ = json.Unmarshal([]byte(l), &r)
		replies[string(r.ID)] = r
	}
	return replies, out.String(), err
}

func rpc(id int, method, params string) string {
	if params == "" {
		params = "{}"
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": json.RawMessage(params)})
	return string(b)
}

func call(id int, tool, args string) string {
	return rpc(id, "tools/call", `{"name":"`+tool+`","arguments":`+args+`}`)
}

type toolResult struct {
	IsError bool `json:"isError"`
	Content []struct{ Type, Text string }
}

func (r toolResult) text() string {
	var s []string
	for _, c := range r.Content {
		s = append(s, c.Text)
	}
	return strings.Join(s, "\n")
}

func result(t *testing.T, replies map[string]rpcReply, id string) toolResult {
	t.Helper()
	r, ok := replies[id]
	if !ok || r.Error != nil {
		t.Fatalf("no result for request %s: %+v (replies %v)", id, r, replies)
	}
	var tr toolResult
	if err := json.Unmarshal(r.Result, &tr); err != nil || len(tr.Content) == 0 {
		t.Fatalf("result %s is not a tool result: %s", r.Result, err)
	}
	return tr
}

func TestInitializeAnswersWithTheToolsCapability(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	replies, _, err := serve(t, rpc(1, "initialize", `{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools *json.RawMessage `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct{ Name string } `json:"serverInfo"`
	}
	if err := json.Unmarshal(replies["1"].Result, &init); err != nil || init.Capabilities.Tools == nil || init.ProtocolVersion == "" || init.ServerInfo.Name == "" {
		t.Errorf("initialize result = %s, want protocolVersion, serverInfo and a tools capability", replies["1"].Result)
	}
}

func TestTheInitializedNotificationGetsNoReplyAndPingAnswers(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	replies, out, err := serve(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, rpc(2, "ping", ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies["2"].Error != nil || replies["2"].Result == nil {
		t.Errorf("replies = %v (stdout %q), want only ping's empty result", replies, out)
	}
}

func TestAnUnknownMethodIsAJSONRPCError(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	replies, _, _ := serve(t, rpc(3, "resources/list", ""))
	if e := replies["3"].Error; e == nil || e.Code != -32601 {
		t.Errorf("reply = %+v, want error -32601", replies["3"])
	}
}

func TestAMalformedLineDoesNotKillTheServer(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	replies, _, err := serve(t, `{not json`, rpc(4, "ping", ""))
	if err != nil {
		t.Fatal(err)
	}
	if e := replies["null"].Error; e == nil || e.Code != -32700 {
		t.Errorf("reply to the bad line = %+v, want a -32700 error with a null id", replies["null"])
	}
	if replies["4"].Result == nil {
		t.Errorf("ping after the bad line got %+v, want a result", replies["4"])
	}
}

// The tools by name, with the route each calls. Anything not in this list is a tool the Never list forbids or one
// nobody specified: approvals, typing mid-turn, config, sources, settings and Preferences have none.
var wantTools = []string{
	"add_order", "answer_item", "ask_question", "dismiss_ship", "hold_fire_on", "launch_ship", "list_campaigns", "list_inbox",
	"list_routines", "list_ships", "list_sources", "make_commitment", "next_prompt", "read_conversation", "read_hold_fire", "read_outcomes",
	"save_note", "stop_ship", "turn_routine_off", "wake_ship",
}

func TestToolsListNamesEveryToolWithAnInputSchemaAndNoMore(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	replies, _, err := serve(t, rpc(1, "tools/list", ""))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type string `json:"type"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(replies["1"].Result, &list); err != nil {
		t.Fatalf("tools/list result = %s", replies["1"].Result)
	}
	var names []string
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
		if tl.InputSchema.Type != "object" || tl.Description == "" {
			t.Errorf("tool %s: inputSchema type %q, description %q; want an object schema and a description", tl.Name, tl.InputSchema.Type, tl.Description)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantTools, ",") {
		t.Errorf("tools = %v, want exactly %v", names, wantTools)
	}
}

// Claude Code drops a tool whose properties is null, so every schema carries an object there.
func TestEveryToolSchemaHasObjectPropertiesCoveringItsRequiredNames(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	replies, _, err := serve(t, rpc(1, "tools/list", ""))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(replies["1"].Result, &list); err != nil {
		t.Fatalf("tools/list result = %s", replies["1"].Result)
	}
	if len(list.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	for _, tl := range list.Tools {
		s := tl.InputSchema
		if s.Type != "object" || s.Properties == nil {
			t.Errorf("tool %s: type %q, properties nil=%v; want an object schema with a properties object", tl.Name, s.Type, s.Properties == nil)
		}
		for _, r := range s.Required {
			if _, ok := s.Properties[r]; !ok {
				t.Errorf("tool %s: required %q is not a property", tl.Name, r)
			}
		}
	}
}

func TestWithNoTokenTheServerRefusesToStart(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, "")
	replies, out, err := serve(t, rpc(1, "initialize", ""))
	if err == nil {
		t.Error("admiral mcp with no token returned nil, want an error (exit non-zero)")
	}
	if out != "" || len(replies) != 0 {
		t.Errorf("stdout = %q, want nothing from a server that refused to start", out)
	}
	if len(a.requests()) != 0 {
		t.Errorf("requests = %v, want none", a.requests())
	}
}

func TestTheTokenIsNeverSentToAnotherAdmiralsServer(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	t.Setenv("ADMIRAL_HOME", t.TempDir()) // the server on the port serves a different home
	replies, _, err := serve(t, call(1, "list_ships", `{}`))
	for _, r := range a.requests() {
		if r.Actor != "" {
			t.Errorf("the actor token went to a server of another home: %+v", r)
		}
	}
	// Refusing to start and a tool error are both refusals; answering the tool with the server's data is not.
	refused := err != nil
	if r, ok := replies["1"]; ok && r.Error == nil {
		refused = refused || result(t, replies, "1").IsError
	}
	if !refused {
		t.Errorf("no refusal: err %v, replies %v", err, replies)
	}
}

// Each tool: the arguments it takes and the one request it makes.
func TestEachToolSendsItsRouteBodyAndTheActorHeader(t *testing.T) {
	for _, c := range []struct {
		tool, args, method, path, query string
		body                            map[string]any
	}{
		{"list_ships", `{}`, "GET", "/api/ships", "", nil},
		{"read_conversation", `{"session":"saltpaw"}`, "GET", "/api/ships/saltpaw/conversation", "", nil},
		{"read_outcomes", `{"ship":"saltpaw","limit":5}`, "GET", "/api/outcomes", "limit=5&ship=saltpaw", nil},
		{"list_inbox", `{}`, "GET", "/api/inbox", "", nil},
		{"list_sources", `{}`, "GET", "/api/sources", "", nil},
		{"list_campaigns", `{}`, "GET", "/api/campaigns", "", nil},
		{"add_order", `{"source":"notes","panel":"todo","text":"fix the docs"}`, "POST", "/api/sources/notes/orders", "", map[string]any{"panel": "todo", "text": "fix the docs"}},
		{"answer_item", `{"session":"saltpaw","id":"i-1","answers":{"q1":{"selected":["main"]}}}`, "POST", "/api/inbox/saltpaw/i-1/answer", "", map[string]any{"answers": map[string]any{"q1": map[string]any{"selected": []any{"main"}}}}},
		{"save_note", `{"text":"deploys go out on Tuesdays"}`, "POST", "/api/chat/notes", "", map[string]any{"text": "deploys go out on Tuesdays"}},
		{"stop_ship", `{"session":"saltpaw"}`, "POST", "/api/ships/saltpaw/stop", "", nil},
		{"dismiss_ship", `{"session":"saltpaw"}`, "POST", "/api/ships/saltpaw/dismiss", "", nil},
		{"next_prompt", `{"session":"saltpaw","text":"now the docs"}`, "POST", "/api/ships/saltpaw/next", "", map[string]any{"text": "now the docs"}},
		{"wake_ship", `{"session":"saltpaw","text":"also the docs"}`, "POST", "/api/ships/saltpaw/wake", "", map[string]any{"text": "also the docs"}},
		{"list_routines", `{}`, "GET", "/api/fleets/work/routines", "", nil},
		{"turn_routine_off", `{"routine":"r-1"}`, "POST", "/api/fleets/work/routines/r-1/off", "", nil},
		{"hold_fire_on", `{}`, "POST", "/api/fleets/work/hold-fire/on", "", nil},
		{"hold_fire_on", `{"all_fleets":true}`, "POST", "/api/hold-fire/on", "", nil},
		{"read_hold_fire", `{}`, "GET", "/api/fleets/work/hold-fire", "", nil},
	} {
		t.Run(c.tool, func(t *testing.T) {
			a := newStandIn(t, func(mcpReq) (int, string) { return 200, `{"ok":true}` })
			a.env(t, mcpToken)
			replies, _, err := serve(t, call(1, c.tool, c.args))
			if err != nil {
				t.Fatal(err)
			}
			if tr := result(t, replies, "1"); tr.IsError || !strings.Contains(tr.text(), "ok") {
				t.Errorf("result = %+v, want the server's answer", tr)
			}
			got := a.requests()
			if len(got) != 1 {
				t.Fatalf("requests = %+v, want exactly one", got)
			}
			g := got[0]
			if g.Method != c.method || g.Path != c.path || g.Query != c.query || g.Actor != mcpToken {
				t.Errorf("request = %s %s?%s actor %q, want %s %s?%s with the token", g.Method, g.Path, g.Query, g.Actor, c.method, c.path, c.query)
			}
			for k, v := range c.body {
				if gv, _ := json.Marshal(g.Body[k]); string(gv) != mustJSON(v) {
					t.Errorf("body %s = %s, want %s", k, gv, mustJSON(v))
				}
			}
		})
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestAServerErrorBecomesAToolErrorCarryingItsMessage(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) {
		return 403, `{"error":"AdmiralBot for personal can't touch w-1, which belongs to fleet work"}`
	})
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "stop_ship", `{"session":"w-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if tr := result(t, replies, "1"); !tr.IsError || !strings.Contains(tr.text(), "belongs to fleet work") {
		t.Errorf("result = %+v, want isError with the server's message", tr)
	}
}

func launchArgs(skill string, extra string) string {
	return `{"dir":"/code/widget","harness":"claude","skill":"` + skill + `","text":"fix the build"` + extra + `}`
}

func TestACommodoreLaunchIsAutopilotUnlessTheEngineerSaidWithMe(t *testing.T) {
	for _, c := range []struct {
		name, args string
		autopilot  bool
		mode       string
	}{
		{"commodore", launchArgs("commodore", ""), true, "autopilot"},
		{"commodore with me", launchArgs("commodore", `,"with_me":true`), false, "interactive"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
			a.env(t, mcpToken)
			replies, _, err := serve(t, call(1, "launch_ship", c.args))
			if err != nil {
				t.Fatal(err)
			}
			got := a.requests()
			if len(got) != 1 || got[0].Method != "POST" || got[0].Path != "/api/ships" {
				t.Fatalf("requests = %+v, want one POST /api/ships", got)
			}
			b := got[0].Body
			if ap, _ := b["autopilot"].(bool); ap != c.autopilot {
				t.Errorf("autopilot = %v, want %v (body %v)", b["autopilot"], c.autopilot, b)
			}
			if b["fleet"] != "work" || b["dir"] != "/code/widget" || b["harness"] != "claude" || b["text"] != "fix the build" {
				t.Errorf("body = %v, want fleet work (from the chat's fleet), the dir, harness and text", b)
			}
			if sk, _ := b["skill"].(map[string]any); sk["kind"] != "commodore" {
				t.Errorf("skill = %v, want kind commodore", b["skill"])
			}
			var out struct{ Mode string }
			tr := result(t, replies, "1")
			if err := json.Unmarshal([]byte(tr.text()), &out); err != nil || out.Mode != c.mode || tr.IsError {
				t.Errorf("result = %q, want a JSON object with mode %q", tr.text(), c.mode)
			}
		})
	}
}

func TestALaunchWithAnotherSkillLeavesAutopilotToTheRequest(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
	a.env(t, mcpToken)
	if _, _, err := serve(t, call(1, "launch_ship", launchArgs("none", ""))); err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 1 {
		t.Fatalf("requests = %+v, want one", got)
	}
	if ap, _ := got[0].Body["autopilot"].(bool); ap {
		t.Errorf("a Just launch got autopilot: %v", got[0].Body)
	}
	if sk, _ := got[0].Body["skill"].(map[string]any); sk["kind"] != "none" {
		t.Errorf("skill = %v, want kind none", got[0].Body["skill"])
	}
}

func TestALaunchRefusedForTheGuardIsPassedThroughNotRetried(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) {
		return 400, `{"error":"autopilot needs the guard for claude, which has not passed its probe in fleet work"}`
	})
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "launch_ship", launchArgs("commodore", "")))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(a.requests()); n != 1 {
		t.Errorf("POSTs = %d, want 1: a safety gate is not retried without autopilot", n)
	}
	if tr := result(t, replies, "1"); !tr.IsError || !strings.Contains(tr.text(), "has not passed its probe") {
		t.Errorf("result = %+v, want isError with the guard's message", tr)
	}
}

func TestALaunchRefusedForTheCapIsRelaunchedWithTheEngineer(t *testing.T) {
	a := newStandIn(t, func(r mcpReq) (int, string) {
		if ap, _ := r.Body["autopilot"].(bool); ap {
			return 409, `{"error":"autopilot is at its cap of 3 ships"}`
		}
		return 201, `{"ship":{"session":"saltpaw"}}`
	})
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "launch_ship", launchArgs("commodore", "")))
	if err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 2 {
		t.Fatalf("POSTs = %d, want 2: autopilot, then interactive", len(got))
	}
	if ap, _ := got[1].Body["autopilot"].(bool); ap || got[1].Body["text"] != got[0].Body["text"] || got[1].Body["dir"] != got[0].Body["dir"] {
		t.Errorf("second POST = %v, want the same request with autopilot false", got[1].Body)
	}
	tr := result(t, replies, "1")
	var out struct{ Mode string }
	if json.Unmarshal([]byte(tr.text()), &out) != nil || out.Mode != "interactive" || tr.IsError || !strings.Contains(tr.text(), "cap") {
		t.Errorf("result = %q, want mode interactive and the cap as the reason", tr.text())
	}
}

func TestOtherLaunchConflictsAreNotRetriedAsInteractive(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) {
		return 409, `{"error":"this order already has a ship (saltpaw)"}`
	})
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "launch_ship", launchArgs("commodore", "")))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(a.requests()); n != 1 {
		t.Errorf("POSTs = %d, want 1: only the cap is worked around", n)
	}
	if tr := result(t, replies, "1"); !tr.IsError || !strings.Contains(tr.text(), "already has a ship") {
		t.Errorf("result = %+v, want isError with the holding ship's message", tr)
	}
}

func TestAskQuestionWritesTheQuestionThenWaitsForTheAnswer(t *testing.T) {
	a := newStandIn(t, func(r mcpReq) (int, string) {
		if r.Method == "POST" {
			return 201, `{"id":"20260101T000000-ab12cd34"}`
		}
		return 200, `{"state":"answered","answer":{"answers":{"q1":{"selected":["main"]}}}}`
	})
	a.env(t, mcpToken)
	args := `{"title":"Which branch?","questions":[{"id":"q1","text":"Which branch?","multi":false,"options":[{"label":"main"}]}]}`
	replies, _, err := serve(t, call(1, "ask_question", args))
	if err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 2 || got[0].Method != "POST" || got[0].Path != "/api/chat/questions" ||
		got[1].Method != "GET" || got[1].Path != "/api/chat/questions/20260101T000000-ab12cd34/answer" {
		t.Fatalf("requests = %+v, want POST /api/chat/questions then GET its answer", got)
	}
	if _, ok := got[0].Body["fleet"]; ok {
		t.Errorf("the question body names a fleet: %v; the token decides", got[0].Body)
	}
	w, err := url.ParseQuery(got[1].Query)
	if err != nil || w.Get("wait") == "" {
		t.Errorf("poll query %q, want a wait", got[1].Query)
	}
	if tr := result(t, replies, "1"); tr.IsError || !strings.Contains(tr.text(), "main") {
		t.Errorf("result = %+v, want the answer", tr)
	}
}

func TestAskQuestionThatIsStillUnansweredReturnsItsIDToResume(t *testing.T) {
	a := newStandIn(t, func(r mcpReq) (int, string) {
		if r.Method == "POST" {
			return 201, `{"id":"20260101T000000-ab12cd34"}`
		}
		return 200, `{"state":"waiting"}`
	})
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "ask_question", `{"title":"t","questions":[{"id":"q1","text":"t","multi":false,"options":[{"label":"a"}]}],"wait_seconds":1}`),
		call(2, "ask_question", `{"wait_id":"20260101T000000-ab12cd34","wait_seconds":1}`))
	if err != nil {
		t.Fatal(err)
	}
	tr := result(t, replies, "1")
	if tr.IsError || !strings.Contains(strings.ToLower(tr.text()), "still waiting") || !strings.Contains(tr.text(), "20260101T000000-ab12cd34") {
		t.Errorf("first result = %+v, want still waiting, naming the id", tr)
	}
	var posts int
	for _, r := range a.requests() {
		if r.Method == "POST" {
			posts++
		}
	}
	if posts != 1 {
		t.Errorf("POSTs = %d, want 1: wait_id resumes a wait and asks nothing", posts)
	}
	if tr := result(t, replies, "2"); tr.IsError || !strings.Contains(strings.ToLower(tr.text()), "still waiting") {
		t.Errorf("resumed result = %+v, want still waiting again", tr)
	}
}

func TestAskQuestionReportsAWithdrawnQuestion(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) { return 200, `{"state":"withdrawn"}` })
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "ask_question", `{"wait_id":"20260101T000000-ab12cd34"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 1 || got[0].Method != "GET" {
		t.Errorf("requests = %+v, want one GET and no POST for a wait_id", got)
	}
	if tr := result(t, replies, "1"); !strings.Contains(strings.ToLower(tr.text()), "withdrawn") {
		t.Errorf("result = %+v, want it to say the question was withdrawn", tr)
	}
}

func TestALaunchSendsEveryInputItWasGivenAndOmitsTheRest(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
	a.env(t, mcpToken)
	args := `{"dir":"/code/widget","harness":"claude","skill":"commodore","text":"fix the build","source":"notes","order_id":"o-1",` +
		`"clone":true,"clone_repo":"jbuffin/widget","anyway":true,"crew":["a","b"]}`
	if _, _, err := serve(t, call(1, "launch_ship", args)); err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 1 {
		t.Fatalf("POSTs = %+v, want one", got)
	}
	want := map[string]any{"dir": "/code/widget", "harness": "claude", "text": "fix the build", "source": "notes", "order_id": "o-1",
		"clone": true, "clone_repo": "jbuffin/widget", "anyway": true, "crew": []any{"a", "b"}, "fleet": "work"}
	for k, v := range want {
		if mustJSON(got[0].Body[k]) != mustJSON(v) {
			t.Errorf("body %s = %s, want %s", k, mustJSON(got[0].Body[k]), mustJSON(v))
		}
	}

	b := newStandIn(t, func(mcpReq) (int, string) { return 201, `{"ship":{"session":"saltpaw"}}` })
	b.env(t, mcpToken)
	if _, _, err := serve(t, call(1, "launch_ship", launchArgs("commodore", ""))); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"source", "order_id", "clone", "clone_repo", "anyway", "chat_message", "crew", "issues"} {
		if v, ok := b.requests()[0].Body[k]; ok {
			t.Errorf("body holds %s = %v though the input was not given", k, v)
		}
	}
}

func TestTheClientDoesNotFollowARedirectWithTheToken(t *testing.T) {
	var mu sync.Mutex
	var hits []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Header.Get("X-Admiral-Actor"))
		mu.Unlock()
	}))
	t.Cleanup(target.Close)
	a := newStandIn(t, func(mcpReq) (int, string) { return 307, "" })
	// The stand-in's mux can't set Location, so wrap its handler.
	inner := a.srv.Config.Handler
	a.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admiral-Actor") != "" {
			w.Header().Set("Location", target.URL+"/steal")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		inner.ServeHTTP(w, r)
	})
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "list_ships", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 0 {
		t.Errorf("the redirect target was reached with actor %q", hits)
	}
	if tr := result(t, replies, "1"); !tr.IsError {
		t.Errorf("result = %+v, want a tool error for the 3xx", tr)
	}
}

func TestTheTokenFileIsReadAndTrimmedAndAMissingOneRefusesToStart(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, "")
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(mcpToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIRAL_ACTOR_TOKEN_FILE", file)
	if _, _, err := serve(t, call(1, "list_ships", `{}`)); err != nil {
		t.Fatalf("with a token file: %v", err)
	}
	reqs := a.requests()
	if len(reqs) != 1 || reqs[0].Actor != mcpToken {
		t.Errorf("requests = %+v, want one carrying the file's token without its newline", reqs)
	}

	t.Setenv("ADMIRAL_ACTOR_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
	_, _, err := serve(t, rpc(1, "initialize", ""))
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Errorf("a missing token file returned %v, want a usage error (exit 2)", err)
	}
}

// Copilot takes an MCP server's tools only by name, so the chat's allowlist is a list kept beside the server's.
func TestTheCopilotChatsToolAllowlistNamesExactlyTheServersTools(t *testing.T) {
	var served []string
	for _, tool := range mcpTools {
		served = append(served, tool.Name)
	}
	got := append([]string(nil), launcher.ChatTools...)
	sort.Strings(served)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(served, ",") {
		t.Errorf("launcher.ChatTools = %v, want the admiral server's tools %v", got, served)
	}
}

func commitArgs(extra string) string {
	return `{"text":"when saltpaw sails, fix the build","trigger":"ship_sailed","subject":"saltpaw","action":"launch","launch":{"dir":"/code/widget","harness":"claude","skill":"commodore","text":"fix the build","resources":["gpu"]` + extra + `}}`
}

const commitmentReply = `{"id":"c-abc123","text":"the quarterly numbers go out","trigger":{"kind":"ship_sailed","subject":"saltpaw"},"action":"launch","via":"admiralbot"}`

func TestMakeCommitmentPostsTheTriggerActionTextAndALaunchSpecInTheShipsBodyShape(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) { return 201, commitmentReply })
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "make_commitment", commitArgs("")))
	if err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) != 1 || got[0].Method != "POST" || got[0].Path != "/api/chat/commitments" || got[0].Actor != mcpToken {
		t.Fatalf("requests = %+v, want one POST /api/chat/commitments with the actor token", got)
	}
	b := got[0].Body
	trig, _ := b["trigger"].(map[string]any)
	if b["text"] != "when saltpaw sails, fix the build" || b["action"] != "launch" || trig["kind"] != "ship_sailed" || trig["subject"] != "saltpaw" {
		t.Errorf("body = %v, want text, action launch and trigger {ship_sailed, saltpaw}", b)
	}
	l, _ := b["launch"].(map[string]any)
	if l["fleet"] != "work" || l["dir"] != "/code/widget" || l["harness"] != "claude" || l["text"] != "fix the build" {
		t.Errorf("launch = %v, want the /api/ships body: fleet work (the chat's), dir, harness and text", l)
	}
	if sk, _ := l["skill"].(map[string]any); sk["kind"] != "commodore" {
		t.Errorf("launch skill = %v, want kind commodore, as launch_ship sends it", l["skill"])
	}
	if mustJSON(l["resources"]) != `["gpu"]` {
		t.Errorf("launch resources = %v, want [gpu]", l["resources"])
	}
	if ap, _ := l["autopilot"].(bool); !ap {
		t.Errorf("launch autopilot = %v, want true: a fired commitment is always unattended", l["autopilot"])
	}
	tr := result(t, replies, "1")
	for _, want := range []string{"c-abc123", "the quarterly numbers go out", "saltpaw", "sail", "launch"} {
		if tr.IsError || !strings.Contains(strings.ToLower(tr.text()), strings.ToLower(want)) {
			t.Errorf("result = %+v, want it to quote back %q so AdmiralBot can repeat it", tr, want)
		}
	}
	if strings.Contains(tr.text(), "ship_sailed") {
		t.Errorf("result = %q, want the trigger in words, not by its code ship_sailed", tr.text())
	}
}

func TestACommitmentLaunchIsAutopilotWhateverTheSkillAndNeverCarriesWithMeOrAnyway(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) { return 201, commitmentReply })
	a.env(t, mcpToken)
	args := strings.Replace(commitArgs(`,"with_me":true,"anyway":true`), `"skill":"commodore"`, `"skill":"none"`, 1)
	replies, _, err := serve(t, call(1, "make_commitment", args))
	if err != nil {
		t.Fatal(err)
	}
	got := a.requests()
	if len(got) == 0 {
		if r := replies["1"]; r.Error == nil && !result(t, replies, "1").IsError {
			t.Fatalf("no POST and no refusal: %+v", r)
		}
		if tr := result(t, replies, "1"); !tr.IsError || !(strings.Contains(tr.text(), "with_me") || strings.Contains(tr.text(), "anyway")) {
			t.Fatalf("no POST, and the refusal %+v does not name with_me or anyway", tr)
		}
		return // a tool that refuses with_me and anyway outright is as good as one that drops them
	}
	if len(got) != 1 {
		t.Fatalf("requests = %+v, want one", got)
	}
	l, _ := got[0].Body["launch"].(map[string]any)
	if ap, _ := l["autopilot"].(bool); !ap {
		t.Errorf("a skill-less commitment launch has autopilot %v, want true", l["autopilot"])
	}
	if _, ok := l["anyway"]; ok {
		t.Errorf("launch carries anyway: %v", l)
	}
}

func TestARefusedCommitmentIsAToolErrorAndIsNotRetried(t *testing.T) {
	a := newStandIn(t, func(mcpReq) (int, string) {
		return 422, `{"error":"autopilot is not proven on claude in fleet work"}`
	})
	a.env(t, mcpToken)
	replies, _, err := serve(t, call(1, "make_commitment", commitArgs("")))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(a.requests()); n != 1 {
		t.Errorf("POSTs = %d, want 1: there is no cap-fallback retry for a commitment", n)
	}
	if tr := result(t, replies, "1"); !tr.IsError || !strings.Contains(tr.text(), "not proven") {
		t.Errorf("result = %+v, want isError with the server's reason", tr)
	}
}

func TestMakeCommitmentIsDescribedAsOnlyForWhatTheEngineerToldItAndItsSchemaHasNoWithMeOrAnyway(t *testing.T) {
	a := newStandIn(t, nil)
	a.env(t, mcpToken)
	replies, _, err := serve(t, rpc(1, "tools/list", ""))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Tools []struct {
			Name        string
			Description string
			InputSchema json.RawMessage
		}
	}
	if err := json.Unmarshal(replies["1"].Result, &list); err != nil {
		t.Fatal(err)
	}
	for _, tl := range list.Tools {
		if tl.Name != "make_commitment" {
			continue
		}
		if d := strings.ToLower(tl.Description); !strings.Contains(d, "engineer") || !(strings.Contains(d, "told") || strings.Contains(d, "asked")) {
			t.Errorf("description = %q, want it limited to something the engineer told or asked AdmiralBot", tl.Description)
		}
		for _, bad := range []string{"with_me", "anyway"} {
			if strings.Contains(string(tl.InputSchema), bad) {
				t.Errorf("make_commitment's schema names %s: %s", bad, tl.InputSchema)
			}
		}
		if !strings.Contains(string(tl.InputSchema), "resources") {
			t.Errorf("make_commitment's schema has no resources: %s", tl.InputSchema)
		}
		return
	}
	t.Error("tools/list has no make_commitment")
}
