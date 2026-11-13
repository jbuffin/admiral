package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
)

// mcpLatest is the protocol revision the server speaks when the client asks for one it doesn't know.
const mcpLatest = "2025-06-18"

var mcpRevisions = map[string]bool{"2024-11-05": true, "2025-03-26": true, mcpLatest: true}

// mcpMaxInFlight bounds the requests served at once; a client that floods the pipe waits instead of growing the process.
const mcpMaxInFlight = 8

// mcpAskWait is how long ask_question waits for an answer before it says so and gives the id back.
const mcpAskWait = 100

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// httpError is the Admiral server's refusal, kept with its status so a tool can act on a 409.
type httpError struct {
	Status int
	Msg    string
}

func (e *httpError) Error() string { return e.Msg }

// mcpClient is a thin client of the Admiral server on loopback, acting as AdmiralBot.
type mcpClient struct {
	token, port, fleet string

	mu   sync.Mutex
	base string // set once the server on the port proved to be this home's
}

// server finds the server the way the CLI does, and only then lets the token go to it: another Admiral may hold the port.
func (c *mcpClient) server() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.base != "" {
		return c.base, nil
	}
	dir, err := admiralHome()
	if err != nil {
		return "", err
	}
	base, _, err := serverFor(c.port, dir)
	if err != nil {
		return "", err
	}
	c.base = base
	return base, nil
}

// do sends one request with the actor token and returns the body of a 2xx answer.
func (c *mcpClient) do(method, path string, body any, timeout time.Duration) ([]byte, error) {
	base, err := c.server()
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Admiral-Actor", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// A redirect would carry the actor token to wherever it points; a 3xx is an error answer here.
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := (&http.Client{Timeout: timeout, CheckRedirect: noRedirect}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &httpError{resp.StatusCode, errorText(bytes.TrimSpace(b))}
	}
	return b, nil
}

func (c *mcpClient) get(path string) (string, error) {
	b, err := c.do("GET", path, nil, 20*time.Second)
	return strings.TrimSpace(string(b)), err
}

func (c *mcpClient) post(path string, body any) (string, error) {
	b, err := c.do("POST", path, body, 20*time.Second)
	return strings.TrimSpace(string(b)), err
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	run         func(c *mcpClient, args json.RawMessage) (string, error)
}

func obj(required []string, props map[string]any) map[string]any {
	if props == nil {
		props = map[string]any{} // a null properties makes Claude Code drop the tool
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

func flag01(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// sessionPath is the path of a ship route; the session comes from the model, so it is escaped.
func sessionPath(session, rest string) (string, error) {
	if session == "" {
		return "", errors.New("session is required")
	}
	return "/api/ships/" + url.PathEscape(session) + rest, nil
}

// shipPost is a tool that posts to one of a ship's routes.
func shipPost(rest string, withText bool) func(*mcpClient, json.RawMessage) (string, error) {
	return func(c *mcpClient, raw json.RawMessage) (string, error) {
		var a struct{ Session, Text string }
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", err
		}
		path, err := sessionPath(a.Session, rest)
		if err != nil {
			return "", err
		}
		var body any
		if withText {
			body = map[string]string{"text": a.Text}
		}
		return c.post(path, body)
	}
}

// fleetPath is the path of one of this chat's fleet routes; the fleet comes from the chat's environment, never the model.
func (c *mcpClient) fleetPath(rest string) (string, error) {
	if c.fleet == "" {
		return "", errors.New("this chat has no fleet")
	}
	return "/api/fleets/" + url.PathEscape(c.fleet) + rest, nil
}

func fleetGet(rest string) func(*mcpClient, json.RawMessage) (string, error) {
	return func(c *mcpClient, _ json.RawMessage) (string, error) {
		path, err := c.fleetPath(rest)
		if err != nil {
			return "", err
		}
		return c.get(path)
	}
}

func plainGet(path string) func(*mcpClient, json.RawMessage) (string, error) {
	return func(c *mcpClient, _ json.RawMessage) (string, error) { return c.get(path) }
}

var mcpTools = []mcpTool{
	{"list_ships", "List the ships of this fleet with their status, what they are working on and what they need.", obj(nil, nil), plainGet("/api/ships")},
	{"read_conversation", "Read a ship's conversation: what the engineer and the agent said, its tool calls and its pending questions.",
		obj([]string{"session"}, map[string]any{"session": str("the ship's session name")}),
		func(c *mcpClient, raw json.RawMessage) (string, error) {
			var a struct{ Session string }
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			path, err := sessionPath(a.Session, "/conversation")
			if err != nil {
				return "", err
			}
			return c.get(path)
		}},
	{"read_outcomes", "Read the history of this fleet's ships: launched, stopped, resumed, dismissed and so on, oldest first.",
		obj(nil, map[string]any{"ship": str("only this ship's records"), "limit": num("how many of the latest records to return")}),
		func(c *mcpClient, raw json.RawMessage) (string, error) {
			var a struct {
				Ship  string
				Limit int
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			q := url.Values{}
			if a.Ship != "" {
				q.Set("ship", a.Ship)
			}
			if a.Limit > 0 {
				q.Set("limit", fmt.Sprint(a.Limit))
			}
			path := "/api/outcomes"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			return c.get(path)
		}},
	{"list_inbox", "List the Inbox: what is waiting for the engineer (questions, proposals, reports).", obj(nil, nil), plainGet("/api/inbox")},
	{"list_sources", "List the order sources with their panels and orders.", obj(nil, nil), plainGet("/api/sources")},
	{"list_campaigns", "List the campaigns.", obj(nil, nil), plainGet("/api/campaigns")},
	{"launch_ship", "Launch a ship, from an order (source and order_id) or freeform text. A Commodore launch runs in autopilot mode unless with_me is true; the result says which mode the ship runs in.",
		obj([]string{"harness"}, map[string]any{
			"harness":    str("the harness: claude, codex or copilot"),
			"dir":        str("a checkout or search root of the fleet"),
			"clone":      flag01("clone the order's repo instead of naming dir"),
			"clone_repo": str("the host/owner/repo the clone is for"),
			"skill":      str("commodore, none, or a skill's name"),
			"text":       str("the prompt, for a freeform launch"),
			"source":     str("an order source's key, for a launch from an order"),
			"order_id":   str("the order's id"),
			"crew":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "crew members to pin, on a Commodore launch"},
			"issues":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "GitHub issue URLs the ship claims; only on a launch with no source"},
			"anyway":     flag01("launch again for a message that already has a freeform ship"),
			"with_me":    flag01("true when the engineer said to work on it with them: the ship runs interactive, not in autopilot"),
		}), launchShip},
	{"add_order", "Add an order to a source's panel, for a source that supports adding.",
		obj([]string{"source", "panel", "text"}, map[string]any{"source": str("the order source's key"), "panel": str("the panel's id"), "text": str("the order's text")}),
		addOrder},
	{"answer_item", "Answer a question in the Inbox with the engineer's choice.",
		obj([]string{"session", "id", "answers"}, map[string]any{
			"session": str("the Inbox item's ship or chat session"), "id": str("the item's id"),
			"answers": map[string]any{"type": "object", "description": "by question id: {\"selected\": [labels], \"text\": ...}"}}),
		func(c *mcpClient, raw json.RawMessage) (string, error) {
			var a struct {
				Session, ID string
				Answers     json.RawMessage
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			if a.Session == "" || a.ID == "" {
				return "", errors.New("session and id are required")
			}
			return c.post("/api/inbox/"+url.PathEscape(a.Session)+"/"+url.PathEscape(a.ID)+"/answer", map[string]json.RawMessage{"answers": a.Answers})
		}},
	{"next_prompt", "Give a ship whose turn has ended its next prompt. Refused while the ship is mid-turn or waiting on something else; a moored ship takes wake_ship.",
		obj([]string{"session", "text"}, map[string]any{"session": str("the ship's session name"), "text": str("the prompt")}), shipPost("/next", true)},
	{"wake_ship", "Wake a moored ship, optionally with text after its resume prompt.",
		obj([]string{"session"}, map[string]any{"session": str("the moored ship's session name"), "text": str("optional text for the new round")}), shipPost("/wake", true)},
	{"stop_ship", "Stop a ship.", obj([]string{"session"}, map[string]any{"session": str("the ship's session name")}), shipPost("/stop", false)},
	{"dismiss_ship", "Dismiss an anchored ship.", obj([]string{"session"}, map[string]any{"session": str("the ship's session name")}), shipPost("/dismiss", false)},
	{"list_routines", "List this fleet's routines: when each runs, its mode, and whether it is on.", obj(nil, nil), fleetGet("/routines")},
	{"get_standing_orders", "Read this fleet's standing orders: its Checks, Guidance and Limits. You can't file or change them. When the engineer wants one, draft it as prose and link them to /app/routines (or /app/routines/new for a routine), where they file it.",
		obj(nil, nil), fleetGet("/standing-orders")},
	{"turn_routine_off", "Turn one of this fleet's routines off. Turning one on, saving, editing or deleting a routine is the engineer's.",
		obj([]string{"routine"}, map[string]any{"routine": str("the routine's id")}),
		func(c *mcpClient, raw json.RawMessage) (string, error) {
			var a struct{ Routine string }
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			if a.Routine == "" {
				return "", errors.New("routine is required")
			}
			path, err := c.fleetPath("/routines/" + url.PathEscape(a.Routine) + "/off")
			if err != nil {
				return "", err
			}
			return c.post(path, nil)
		}},
	{"hold_fire_on", "Turn Hold fire on, so this fleet's routines propose instead of acting; with all_fleets, every fleet's. Only the engineer turns it off.",
		obj(nil, map[string]any{"all_fleets": flag01("true to turn Hold fire on for every fleet, not only this one")}),
		func(c *mcpClient, raw json.RawMessage) (string, error) {
			var a struct {
				AllFleets bool `json:"all_fleets"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", err
			}
			if a.AllFleets {
				return c.post("/api/hold-fire/on", nil)
			}
			path, err := c.fleetPath("/hold-fire/on")
			if err != nil {
				return "", err
			}
			return c.post(path, nil)
		}},
	{"read_hold_fire", "Read whether Hold fire is on for this fleet.", obj(nil, nil), fleetGet("/hold-fire")},
	{"ask_question", "Ask the engineer a question in the chat and wait for the answer. If it is still unanswered after the wait, the result gives the question's id: call again with wait_id to keep waiting.",
		obj(nil, map[string]any{
			"title":        str("the question's title"),
			"questions":    map[string]any{"type": "array", "description": "each {id, text, multi, options: [{label, description}]}", "items": map[string]any{"type": "object"}},
			"wait_id":      str("the id of a question already asked, to wait on it instead of asking again"),
			"wait_seconds": num("how long to wait for the answer"),
		}), askQuestion},
	{"save_note", "Save a fact the engineer asked you to remember. Admiral keeps it and the engineer manages the saved notes on Preferences; a Claude Code chat reads its notes again after every reset, a chat on another harness does not yet. One short fact per note.",
		obj([]string{"text"}, map[string]any{"text": str("the fact to remember, in a sentence or two")}), saveNote},
	{"make_commitment", "Record a promise the engineer told you to keep: something to do, or to tell them, when a ship sails off or anchors, a PR merges or closes, an issue closes, or at a time, or with no trigger. Use it only for what the engineer asked for in this chat, never for something you thought of; then quote it back with its id. Admiral fires it with no chat turn and a launch runs in autopilot. You cannot cancel or change one: only the engineer cancels, on Preferences.",
		obj([]string{"text", "trigger", "action"}, map[string]any{
			"text":    str("the promise as the engineer agreed it, in a sentence or two; you will quote it back"),
			"trigger": map[string]any{"type": "string", "enum": []string{"ship_sailed", "ship_anchored", "pr_merged", "pr_closed", "issue_closed", "time", "none"}, "description": "what to wait for"},
			"subject": str("the trigger's target: a ship's session name, a PR or issue URL on a GitHub host of the fleet, or an RFC 3339 time in the future; none for no trigger"),
			"action":  map[string]any{"type": "string", "enum": []string{"launch", "remind"}, "description": "launch a ship, or remind the engineer with a chat card"},
			"launch": map[string]any{"type": "object", "description": "for action launch: the ship to launch, as launch_ship takes it, plus the resources it needs", "properties": map[string]any{
				"harness":    str("the harness: claude, codex or copilot"),
				"dir":        str("a checkout or search root of the fleet"),
				"clone":      flag01("clone the order's repo instead of naming dir"),
				"clone_repo": str("the host/owner/repo the clone is for"),
				"skill":      str("commodore, none, or a skill's name"),
				"text":       str("the prompt, for a freeform launch"),
				"source":     str("an order source's key, for a launch from an order"),
				"order_id":   str("the order's id"),
				"crew":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "crew members to pin, on a Commodore launch"},
				"issues":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "GitHub issue URLs the ship claims; only on a launch with no source"},
				"resources":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "the configured resources the ship needs while it runs"},
			}},
		}), makeCommitment},
}

func saveNote(c *mcpClient, raw json.RawMessage) (string, error) {
	var a struct{ Text string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Text) == "" {
		return "", errors.New("text is required")
	}
	return c.post("/api/chat/notes", map[string]string{"text": a.Text})
}

func addOrder(c *mcpClient, raw json.RawMessage) (string, error) {
	var a struct{ Source, Panel, Text string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.Source == "" || a.Panel == "" || strings.TrimSpace(a.Text) == "" {
		return "", errors.New("source, panel and text are required")
	}
	return c.post("/api/sources/"+url.PathEscape(a.Source)+"/orders", map[string]string{"panel": a.Panel, "text": a.Text})
}

// shipSpec is what launch_ship and make_commitment take to describe a launch.
type shipSpec struct {
	Harness, Dir, Skill, Text, Source string
	OrderID                           string `json:"order_id"`
	CloneRepo                         string `json:"clone_repo"`
	Clone, Anyway                     bool
	Crew, Issues, Resources           []string
}

// shipsBody is the POST /api/ships body for a, in the chat's fleet.
func (c *mcpClient) shipsBody(a shipSpec, autopilot bool) (map[string]any, error) {
	kind, name := skillKind(a.Skill)
	body := map[string]any{
		"fleet": c.fleet, "harness": a.Harness, "skill": map[string]string{"kind": kind, "name": name}, "autopilot": autopilot,
	}
	for k, v := range map[string]string{"dir": a.Dir, "text": a.Text, "source": a.Source, "order_id": a.OrderID, "clone_repo": a.CloneRepo} {
		if v != "" {
			body[k] = v
		}
	}
	for k, v := range map[string]bool{"clone": a.Clone, "anyway": a.Anyway} {
		if v {
			body[k] = true
		}
	}
	if len(a.Crew) > 0 {
		body["crew"] = a.Crew
	}
	if len(a.Resources) > 0 {
		body["resources"] = a.Resources
	}
	// An order has its own issue, so the server refuses issues beside a source.
	if len(a.Issues) > 0 {
		if a.Source != "" {
			return nil, errors.New("issues go only on a launch with no source: an order has its own issue")
		}
		body["issues"] = a.Issues
	}
	return body, nil
}

func skillKind(skill string) (kind, name string) {
	switch skill {
	case "", "none":
		return "none", ""
	case "commodore":
		return "commodore", ""
	}
	return "skill", skill
}

func launchShip(c *mcpClient, raw json.RawMessage) (string, error) {
	var a struct {
		shipSpec
		WithMe bool `json:"with_me"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	a.Resources = nil
	kind, _ := skillKind(a.Skill)
	autopilot := kind == "commodore" && !a.WithMe
	body, err := c.shipsBody(a.shipSpec, autopilot)
	if err != nil {
		return "", err
	}
	out, err := c.do("POST", "/api/ships", body, 120*time.Second)
	reason := ""
	var he *httpError
	// Autopilot at its cap is the one refusal worked around: a launch on the engineer's current instruction is not blocked by it.
	if errors.As(err, &he) && autopilot && he.Status == http.StatusConflict && strings.Contains(he.Msg, launcher.AutopilotCapText) {
		reason = he.Msg + ", so the ship runs with the engineer instead"
		body["autopilot"] = false
		autopilot = false
		out, err = c.do("POST", "/api/ships", body, 120*time.Second)
	}
	if err != nil {
		return "", err
	}
	mode := "interactive"
	if autopilot {
		mode = "autopilot"
	}
	res := map[string]any{"mode": mode, "launched": json.RawMessage(bytes.TrimSpace(out))}
	if reason != "" {
		res["reason"] = reason
	}
	b, err := json.Marshal(res)
	return string(b), err
}

// makeCommitment records a promise the engineer asked for; the launch it may hold always runs in autopilot,
// since nobody is there when it fires, and it never carries with_me or anyway.
func makeCommitment(c *mcpClient, raw json.RawMessage) (string, error) {
	var a struct {
		Text, Trigger, Subject, Action string
		Launch                         *shipSpec
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	body := map[string]any{"text": a.Text, "action": a.Action, "trigger": map[string]string{"kind": a.Trigger, "subject": a.Subject}}
	if a.Launch != nil {
		a.Launch.Anyway = false
		launch, err := c.shipsBody(*a.Launch, true)
		if err != nil {
			return "", err
		}
		body["launch"] = launch
	}
	out, err := c.post("/api/chat/commitments", body)
	if err != nil {
		return "", err
	}
	var got struct {
		ID, Text, Action string
		Trigger          struct{ Kind, Subject string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.ID == "" {
		return out, nil
	}
	when := map[string]string{
		"ship_sailed": "when " + got.Trigger.Subject + " sails off", "ship_anchored": "when " + got.Trigger.Subject + " anchors",
		"pr_merged": "when " + got.Trigger.Subject + " is merged", "pr_closed": "when " + got.Trigger.Subject + " is closed",
		"issue_closed": "when " + got.Trigger.Subject + " is closed", "time": "at " + got.Trigger.Subject, "none": "with no trigger",
	}[got.Trigger.Kind]
	if when == "" {
		when = "on " + got.Trigger.Kind
	}
	return fmt.Sprintf("Recorded commitment %s: %q, %s, then %s. Quote this back to the engineer. Admiral fires it itself; only the engineer can cancel it, on Preferences.", got.ID, got.Text, when, got.Action), nil
}

func askQuestion(c *mcpClient, raw json.RawMessage) (string, error) {
	var a struct {
		Title       string
		Questions   json.RawMessage
		WaitID      string `json:"wait_id"`
		WaitSeconds int    `json:"wait_seconds"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	id := a.WaitID
	if id == "" {
		// The fleet comes from the token, so the body names none.
		out, err := c.post("/api/chat/questions", map[string]json.RawMessage{"title": mustMarshal(a.Title), "questions": a.Questions})
		if err != nil {
			return "", err
		}
		var made struct{ ID string }
		if json.Unmarshal([]byte(out), &made) != nil || made.ID == "" {
			return "", errors.New("the question was sent but the server gave no id")
		}
		id = made.ID
	}
	wait := a.WaitSeconds
	if wait <= 0 || wait > mcpAskWait+10 {
		wait = mcpAskWait
	}
	out, err := c.do("GET", "/api/chat/questions/"+url.PathEscape(id)+"/answer?wait="+fmt.Sprint(wait), nil, time.Duration(wait+15)*time.Second)
	if err != nil {
		return "", err
	}
	var got struct {
		State  string
		Answer json.RawMessage
	}
	if err := json.Unmarshal(out, &got); err != nil {
		return "", err
	}
	switch got.State {
	case "answered":
		return string(got.Answer), nil
	case "withdrawn":
		return "The question " + id + " was withdrawn: the engineer started a new chat. Ask again if it still matters.", nil
	}
	return "Still waiting for the engineer's answer to question " + id + ". Call ask_question with wait_id " + id + " to keep waiting.", nil
}

func mustMarshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// MCP serves the Model Context Protocol over stdio as AdmiralBot: JSON-RPC 2.0, one message a line.
func MCP(args []string, std IO) error {
	if len(args) > 0 {
		return usagef("%s", Usage("mcp"))
	}
	token := os.Getenv("ADMIRAL_ACTOR_TOKEN")
	if f := os.Getenv("ADMIRAL_ACTOR_TOKEN_FILE"); token == "" && f != "" {
		// Codex's argv is public on the host, so its chat hands the token over as a 0600 file.
		b, err := os.ReadFile(f)
		if err != nil {
			return usagef("admiral mcp could not read ADMIRAL_ACTOR_TOKEN_FILE: %v", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return usagef("admiral mcp is AdmiralBot's tool server: ADMIRAL_ACTOR_TOKEN (or ADMIRAL_ACTOR_TOKEN_FILE) is not set")
	}
	c := &mcpClient{token: token, port: defaultPort(), fleet: os.Getenv("ADMIRAL_CHAT_FLEET")}
	var out sync.Mutex
	send := func(r rpcResponse) {
		r.JSONRPC = "2.0"
		b, err := json.Marshal(r)
		if err != nil {
			return
		}
		out.Lock()
		defer out.Unlock()
		std.Out.Write(append(b, '\n'))
	}
	in := bufio.NewReaderSize(std.In, 64<<10)
	// One call at a time would hold a ping behind a question the engineer has not answered yet.
	var wg sync.WaitGroup
	defer wg.Wait()
	inFlight := make(chan struct{}, mcpMaxInFlight)
	for {
		line, err := in.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			inFlight <- struct{}{} // past the cap, the next line waits unread
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-inFlight }()
				serveRPC(c, line, send)
			}()
		}
		if err != nil {
			return nil
		}
	}
}

var nullID = json.RawMessage("null")

func serveRPC(c *mcpClient, line []byte, send func(rpcResponse)) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil || req.Method == "" {
		send(rpcResponse{ID: nullID, Error: &rpcError{-32700, "the line is not a JSON-RPC request"}})
		return
	}
	if req.ID == nil { // a notification gets no reply
		return
	}
	reply := func(result any) { send(rpcResponse{ID: req.ID, Result: result}) }
	fail := func(code int, msg string) { send(rpcResponse{ID: req.ID, Error: &rpcError{code, msg}}) }
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpLatest
		if mcpRevisions[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		reply(map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]string{"name": "admiral", "version": "1"}})
	case "ping":
		reply(map[string]any{})
	case "tools/list":
		reply(map[string]any{"tools": mcpTools})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			fail(-32602, "the call's params are not valid")
			return
		}
		for _, t := range mcpTools {
			if t.Name != p.Name {
				continue
			}
			args := p.Arguments
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			text, err := t.run(c, args)
			if err != nil {
				text = err.Error()
			}
			reply(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": err != nil})
			return
		}
		fail(-32602, "unknown tool "+p.Name)
	default:
		fail(-32601, "method not found: "+req.Method)
	}
}
