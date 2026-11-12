package chat

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// A Copilot chat runs in its own COPILOT_HOME: its transcripts are read from that home's session-state/, and the login is the engineer's.

// Placeholders the implementer confirms on the real Copilot 1.0.109 TUI (each a one-line change).
var (
	copilotTrustScreen    = "Confirm folder trust\nDo you trust the files in this folder?\n 1. Yes\n 2. Yes, and remember this folder\n 3. No (Esc)"
	copilotComposerScreen = "│ > Enter @ to mention files or / for commands\n  shift+tab switch mode · ctrl+q enqueue"
)

const copilotChatFleets = `{"version":1,"fleets":{
	"b":{"name":"B","claude":{},"copilot":{"env":{"COPILOT_HOME":"/srv/fake-copilot-identity"}},"chat":{"harness":"copilot","model":"gpt-5.6-sol"}}}}`

const (
	copilotSessionA = "5d1f7a3e-2b4c-4e8a-9c61-3f0a7b8d9e12"
	copilotSessionB = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

func copilotChatHome(t *testing.T, s *Service) string {
	t.Helper()
	h := launcher.ChatCopilotHome(s.Home, "b")
	if h == "" {
		t.Fatal("launcher.ChatCopilotHome is empty")
	}
	return h
}

func copilotSessionFile(home, id string) string {
	return filepath.Join(home, "session-state", id, "events.jsonl")
}

func copilotTranscript(text string) []byte {
	return []byte(`{"type":"user.message","data":{"content":"` + text + `","messageId":"m1","turnId":"0"},"id":"e1","timestamp":"2026-11-11T20:04:15.256Z","parentId":null}
{"type":"assistant.message","data":{"messageId":"m2","content":"reply to ` + text + `","toolRequests":[],"turnId":"1"},"id":"e2","timestamp":"2026-11-11T20:04:18.608Z","parentId":"e1"}
{"type":"assistant.turn_end","data":{"turnId":"1"},"id":"e3","timestamp":"2026-11-11T20:04:18.609Z","parentId":"e2"}
`)
}

func TestACopilotChatReaderReadsSessionsFromTheChatHomeOnly(t *testing.T) {
	s := harnessService(t, copilotChatFleets, fakeProbes{})
	own := writeAt(t, copilotSessionFile(copilotChatHome(t, s), copilotSessionA), copilotTranscript("hello"))
	got := servedFromFile(t, s, "b", own)
	if !slices.Contains(got, "user: hello") || !slices.Contains(got, "assistant: reply to hello") {
		t.Errorf("a session under the chat's home serves %v, want the exchange", got)
	}

	// The engineer's own session-state/ is not the chat's: it is refused, as a path outside the config dir is.
	eng := t.TempDir()
	theirs := writeAt(t, copilotSessionFile(eng, copilotSessionA), copilotTranscript("secret"))
	s2 := harnessService(t, strings.Replace(copilotChatFleets, "/srv/fake-copilot-identity", eng, 1), fakeProbes{})
	copilotChatHome(t, s2)
	if got := servedFromFile(t, s2, "b", theirs); len(got) != 0 {
		t.Errorf("a session in the engineer's session-state/ serves %v, want nothing", got)
	}
}

func TestACopilotClearRebindsTheReaderToTheNewSessionsTranscript(t *testing.T) {
	s := harnessService(t, copilotChatFleets, fakeProbes{})
	home := copilotChatHome(t, s)
	a := writeAt(t, copilotSessionFile(home, copilotSessionA), copilotTranscript("first"))
	b := writeAt(t, copilotSessionFile(home, copilotSessionB), copilotTranscript("second"))
	if got := servedFromFile(t, s, "b", a); !slices.Contains(got, "user: first") {
		t.Fatalf("before the clear the chat serves %v, want the first session", got)
	}
	// /clear starts a new session id: the hook's option now names the new file with source "clear".
	got := servedFromFile(t, s, "b", b, "clear")
	if slices.Contains(got, "user: first") || !slices.Contains(got, "user: second") {
		t.Errorf("after /clear the chat serves %v, want only the new session's transcript", got)
	}
}

func TestACopilotResetRecordsTheChatHomeSessionsUsageUnderCopilot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outcomes")
	w := outcomes.New(dir, t.Logf)
	t.Cleanup(w.Close)
	s := harnessService(t, copilotChatFleets, fakeProbes{})
	s.Outcomes = w
	home := copilotChatHome(t, s)
	b, err := os.ReadFile("../usage/testdata/copilot-recorded.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	session := writeAt(t, copilotSessionFile(home, copilotSessionA), b)

	s.chat("b").recordReset("copilot", session, home, "b", "")

	rec := waitForUsage(t, dir)
	if rec["harness"] != "copilot" {
		t.Errorf("usage harness = %v, want copilot", rec["harness"])
	}
	// usage.ParseCopilot: the recorded shutdown's input is 4 and its cache writes are 35732.
	tokens, _ := rec["tokens"].(map[string]any)
	if tokens["input"] != 4.0 || tokens["cache_write"] != 35732.0 {
		t.Errorf("usage tokens = %v, want the shutdown's totals (4 input, 35732 cache writes)", tokens)
	}
}

func TestACopilotChatWithNoTranscriptIsReadyOnceItsComposerShows(t *testing.T) {
	s := harnessService(t, copilotChatFleets, fakeProbes{})
	copilotChatHome(t, s)
	fc := s.chat("b")
	cases := []struct {
		name, screen string
		ready        bool
		msg          string
	}{
		{"folder trust dialog", copilotTrustScreen, false, CopilotTrustFolder},
		{"still starting", "", false, NotStarted},
		{"composer", copilotComposerScreen, true, ""},
	}
	for _, c := range cases {
		fc.mu.Lock()
		fc.st.opts.screen = c.screen
		ready, msg := fc.notStartedLocked()
		fc.mu.Unlock()
		if ready != c.ready || msg != c.msg {
			t.Errorf("%s: ready=%v msg=%q, want ready=%v msg=%q", c.name, ready, msg, c.ready, c.msg)
		}
	}
	if CopilotTrustFolder == "" || CopilotTrustFolder == NotStarted {
		t.Errorf("CopilotTrustFolder = %q, want a message that says what to do on the trust dialog", CopilotTrustFolder)
	}
}

func TestTheChatsResetCommandIsClearForCopilot(t *testing.T) {
	if got := resetCommand("copilot"); got != "/clear" {
		t.Errorf("resetCommand(copilot) = %q, want /clear", got)
	}
}
