package shipconv

// Serving a Copilot ship's and boat's conversation: HTTP, WebSocket and the ship-list tail.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// copilotPath is where Copilot records a session under the fixture's home; the fixture's config dir doubles as the Copilot home.
func (f *fixture) copilotPath(id string) string {
	return filepath.Join(f.cfg, "session-state", id, "events.jsonl")
}

// copilotShip adds a copilot ship whose option records path as written by its launch.
func (f *fixture) copilotShip(name, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.harness[name] = "copilot"
	f.options[name] = "1700000000000000000 launch " + path
	f.starts[name] = "'/bin/copilot' '--session-id' '" + sid + "' '--' 'go'"
}

func copilotUser(i int) string {
	return fmt.Sprintf(`{"type":"user.message","id":"u-%03d","parentId":null,"timestamp":"2026-01-02T03:04:05.000Z","data":{"content":"message %03d"}}`+"\n", i, i)
}

func copilotAssistant(i int, text string) string {
	return fmt.Sprintf(`{"type":"assistant.message","id":"a-%03d","parentId":null,"timestamp":"2026-01-02T03:04:06.000Z","data":{"messageId":"m%d","content":%q,"toolRequests":[]}}`+"\n", i, i, text)
}

func TestCopilotConversationEndpoint_States(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := f.copilotPath(sid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	f.copilotShip("pilot", path)
	f.wantState("pilot", "none") // not written yet, never seen

	appendTo(t, path, copilotUser(1)+copilotAssistant(2, "Hi."))
	if code, p, body := f.get("pilot", ""); code != 200 || p.State != "ok" || len(p.Items) != 2 || p.Items[0].Text != "message 001" || p.Items[1].Text != "Hi." {
		t.Fatalf("GET = %d %s, want ok with the user's message and the reply", code, body)
	}

	f.copilotShip("steered", path)
	f.starts["steered"] = "copilot --session-id 11111111-2222-4333-8444-555555555555 -- go"
	f.wantState("steered", "none") // the option names another session than the launch gave the harness

	f.copilotShip("resumed", path)
	f.starts["resumed"] = "copilot --resume " + sid
	f.wantState("resumed", "none") // the launch named no session of ours

	f.copilotShip("hooked", path)
	f.options["hooked"] = "1700000000000000000 hook " + path
	f.wantState("hooked", "none") // only a path the launch recorded is ours

	outside := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(outside, []byte(copilotUser(1)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.copilotShip("outside", outside)
	f.wantState("outside", "none") // an id that is not its directory's name
}

func TestCopilotConversationEndpoint_RefusesALinkedEventsFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	good := f.copilotPath(sid)
	if err := os.MkdirAll(filepath.Dir(good), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, good, copilotUser(1))
	f.copilotShip("pilot", good)
	f.wantState("pilot", "ok") // the control: the layout itself is served

	other := "5d1f7a3e-2b4c-4e8a-9c61-3f0a7b8d9e13"
	outside := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(outside, []byte(copilotUser(2)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.cfg, "session-state", other), 0o755); err != nil {
		t.Fatal(err)
	}
	linked := f.copilotPath(other)
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	f.copilotShip("linked", linked)
	f.starts["linked"] = "'/bin/copilot' '--session-id' '" + other + "' '--' 'go'"
	f.wantState("linked", "unreadable")
}

func TestCopilotConversationSocket_DeliversAppends(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := f.copilotPath(sid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, copilotUser(1))
	f.copilotShip("pilot", path)
	sub := dial(t, serve(t, f))

	sub.send("conversation_open", "pilot")
	sub.opened("pilot")
	appendTo(t, path, copilotAssistant(2, "A live reply."))
	m := sub.next("the appended reply")
	if got := texts(m); len(got) != 1 || got[0] != "A live reply." || m["ship"] != "pilot" {
		t.Errorf("pushed %v for ship %v, want [A live reply.] for pilot", got, m["ship"])
	}
}

func TestCopilotTailsFollowTheLastThingTheShipSaid(t *testing.T) {
	f := newFixture(t)
	path := f.copilotPath(sid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, copilotUser(1)+copilotAssistant(2, "First thought.\nmore"))
	f.copilotShip("pilot", path)
	tails := tailsOf(f)

	if !tails.Poll() || tails.Tail("pilot") != "First thought." {
		t.Fatalf("tail = %q, want the first line of the assistant text", tails.Tail("pilot"))
	}
	appendTo(t, path, copilotAssistant(3, "Second thought."))
	if !tails.Poll() || tails.Tail("pilot") != "Second thought." {
		t.Errorf("tail after an append = %q, want %q", tails.Tail("pilot"), "Second thought.")
	}
}

func TestCopilotCrewBoatConversation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.harness["alpha"] = "claude"
	path := f.copilotPath(sid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, copilotUser(1)+copilotAssistant(2, "Reviewed."))
	boats := map[string]sessionhost.Crew{
		"rev": {Name: "rev", Harness: "copilot", Launched: "B1", Transcript: path},
		"new": {Name: "new", Harness: "copilot", Launched: "B2"}, // no agentStop yet, so no path
	}
	f.svc.crew = func(sh ships.Ship, name string) (sessionhost.Crew, string, bool) {
		b, ok := boats[name]
		return b, f.cfg, ok
	}
	mux := http.NewServeMux()
	f.svc.Routes(mux)
	get := func(name string) (int, pageJSON) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships/alpha/crew/"+name+"/conversation", nil))
		var p pageJSON
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return rec.Code, p
	}
	if code, p := get("rev"); code != 200 || p.State != "ok" || len(p.Items) != 2 || !strings.Contains(p.Items[1].Text, "Reviewed.") {
		t.Errorf("copilot boat = %d %+v, want 200 ok with its 2 items", code, p)
	}
	if code, p := get("new"); code != 200 || p.State != "none" {
		t.Errorf("a boat before its first agentStop = %d %+v, want 200 none", code, p)
	}
}

// A Copilot ship or boat reads the fleet's Copilot home, never its Claude config dir.
func TestCopilotLooksUpItsOwnConfigDir(t *testing.T) {
	t.Parallel()
	svc := &Service{Config: func() *config.Config {
		return &config.Config{Fleets: []config.Fleet{{Key: "work", Harnesses: []config.Harness{
			{Name: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": "/id/claude"}},
			{Name: "copilot", Env: map[string]string{"COPILOT_HOME": "/id/copilot"}},
		}}}}
	}}
	sh := ships.Ship{Session: "s", Fleet: "work"}
	for harness, want := range map[string]string{"claude": "/id/claude", "copilot": "/id/copilot"} {
		if got, ok := svc.configDir(sh, harness); !ok || got != want {
			t.Errorf("config dir for %s = %q (found %v), want %q", harness, got, ok, want)
		}
	}
}
