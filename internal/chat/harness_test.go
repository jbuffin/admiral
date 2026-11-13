package chat

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The chat runs only on an offered harness and reads its conversation with that harness's decoder.

type fakeProbes map[string][2]string

func (p fakeProbes) probe(fleet, harness, capability string) (string, string) {
	if r, ok := p[fleet+"/"+harness+"/"+capability]; ok {
		return r[0], r[1]
	}
	return "not_checked", ""
}

func harnessService(t *testing.T, raw string, p fakeProbes) *Service {
	t.Helper()
	cfg, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ChatProbe = p.probe
	return &Service{Config: func() *config.Config { return cfg }, Home: t.TempDir()}
}

func onKeys(s *Service) []string {
	var out []string
	for _, c := range s.onFleets() {
		out = append(out, c.key)
	}
	return out
}

const twoFleets = `{"version":1,"fleets":{
	"a":{"name":"A","claude":{}},
	"b":{"name":"B","claude":{},"codex":{},"chat":{"harness":"codex","model":"gpt-6.1-sol"}}}}`

func TestAChatStartsOnlyOnAnOfferedHarnessAndStartsOnceItsProbesPass(t *testing.T) {
	p := fakeProbes{}
	s := harnessService(t, twoFleets, p)
	// Claude counts until its fence fails; codex needs both probes.
	if got := onKeys(s); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("fleets with a chat on = %v, want only a (b's codex is unchecked)", got)
	}
	if _, err := s.chatFor("b"); err == nil || err.Status != http.StatusConflict || !strings.Contains(err.Msg, "Codex") {
		t.Errorf("chatFor(b) = %+v, want a 409 naming Codex", err)
	}

	p["a/claude/bot.mcp_fence"] = [2]string{"failed", "a tool answered"}
	if got := onKeys(s); len(got) != 0 {
		t.Errorf("with a's fence failed, fleets on = %v, want none", got)
	}
	if _, err := s.chatFor("a"); err == nil || err.Status != http.StatusConflict || !strings.Contains(err.Msg, "a tool answered") {
		t.Errorf("chatFor(a) = %+v, want a 409 with the probe's detail", err)
	}

	p["a/claude/bot.mcp_fence"] = [2]string{"passed", ""}
	p["b/codex/bot.mcp_fence"] = [2]string{"passed", ""}
	p["b/codex/bot.config_home"] = [2]string{"passed", ""}
	if got := onKeys(s); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("after the probes passed, fleets on = %v, want a and b", got)
	}
	if _, err := s.chatFor("b"); err != nil {
		t.Errorf("chatFor(b) once offered = %+v, want the chat", err)
	}
}

const copilotChat = `{"version":1,"fleets":{
	"cop":{"name":"Cop","copilot":{"env":{"COPILOT_HOME":"/srv/fake-copilot-identity"}},"chat":{"harness":"copilot"}},
	"cl":{"name":"Cl","claude":{"env":{"CLAUDE_CONFIG_DIR":"/srv/fake-claude-identity"}}}}}`

func TestTheChatsLoginDirIsTheEngineersIdentityDirForEachHarness(t *testing.T) {
	s := harnessService(t, copilotChat, fakeProbes{})
	if got, want := s.Login(sessionhost.ChatSession("cl")), config.IdentityDir("claude", map[string]string{"CLAUDE_CONFIG_DIR": "/srv/fake-claude-identity"}); got != want || got == "" {
		t.Errorf("a claude chat's login dir = %q, want %q", got, want)
	}
	want := config.IdentityDir("copilot", map[string]string{"COPILOT_HOME": "/srv/fake-copilot-identity"})
	if got := s.Login(sessionhost.ChatSession("cop")); got != want || want == "" {
		t.Errorf("a copilot chat's login dir = %q, want the engineer's %q", got, want)
	}
}

const copilotEvents = `{"type":"user.message","data":{"content":"hello from the engineer","messageId":"m1","turnId":"0"},"id":"e1","timestamp":"2026-11-11T20:04:15.256Z","parentId":null}
{"type":"assistant.message","data":{"messageId":"m2","content":"Done.","toolRequests":[],"turnId":"1"},"id":"e2","timestamp":"2026-11-11T20:04:18.608Z","parentId":"e1"}
{"type":"assistant.turn_end","data":{"turnId":"1"},"id":"e3","timestamp":"2026-11-11T20:04:18.609Z","parentId":"e2"}
`

func servedFor(t *testing.T, s *Service, key string) []Message {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(copilotEvents), 0o600); err != nil {
		t.Fatal(err)
	}
	fc := s.chat(key)
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.transcriptChangedLocked("1 start " + path)
	if fc.st.rd == nil {
		t.Fatal("no reader after a transcript option")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fc.st.rd.Refresh(f, info)
	return fc.st.served()
}

func TestTheChatReaderDecodesWithTheFleetsChatHarness(t *testing.T) {
	s := harnessService(t, `{"version":1,"fleets":{
		"cop":{"name":"Cop","copilot":{},"chat":{"harness":"copilot"}},
		"cl":{"name":"Cl","claude":{}}}}`, fakeProbes{})

	got := servedFor(t, s, "cop")
	var texts []string
	for _, m := range got {
		texts = append(texts, m.Role+": "+m.Text)
	}
	if !slices.Contains(texts, "user: hello from the engineer") || !slices.Contains(texts, "assistant: Done.") {
		t.Errorf("a copilot chat reading events.jsonl serves %v, want the engineer's message and the reply", texts)
	}
	if got := servedFor(t, s, "cl"); len(got) != 0 {
		t.Errorf("a claude chat reading a copilot file serves %v, want nothing: it decodes Claude's format", got)
	}
}
