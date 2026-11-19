package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/ledger"
)

const briefText = "## Instructions\nAsk before launching.\n\n## Notes\n- deploys go out on Tuesdays (via admiralbot)\n"

// chatHook runs Hook as the hook of fleet's chat; fleet "" is a ship's hook. TMUX points at an empty
// directory so the state write finds no tmux server, never the engineer's.
func chatHook(t *testing.T, home, fleet, event, payload string) string {
	t.Helper()
	t.Setenv("ADMIRAL_HOME", home)
	t.Setenv("ADMIRAL_CHAT_FLEET", fleet)
	t.Setenv("ADMIRAL_CHAT_HARNESS", "claude")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX_PANE", "%99")
	var out, errOut bytes.Buffer
	if err := Hook([]string{event}, IO{In: strings.NewReader(payload), Out: &out, Err: &errOut}); err != nil {
		t.Fatalf("Hook(%s) = %v, want nil: a hook never fails", event, err)
	}
	return out.String()
}

func writeChatFile(t *testing.T, home, fleet, name, text string) {
	t.Helper()
	dir := filepath.Join(home, "chat", fleet)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// hookContext parses a hook's stdout as Claude Code's JSON form and returns its event and added context.
func hookContext(t *testing.T, out string) (event, added string) {
	t.Helper()
	var got struct {
		Output struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout = %q, want one JSON object with hookSpecificOutput: %v", out, err)
	}
	return got.Output.Event, got.Output.Context
}

func TestSessionStartInAChatPrintsTheBriefForEverySource(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork", "a-future-source", ""} {
		t.Run("source "+source, func(t *testing.T) {
			home := t.TempDir()
			writeChatFile(t, home, "home", "brief.md", briefText)
			out := chatHook(t, home, "home", "SessionStart", `{"source":"`+source+`","transcript_path":"/x/y.jsonl"}`)
			event, added := hookContext(t, out)
			if event != "SessionStart" {
				t.Errorf("hookEventName = %q, want SessionStart or Claude Code drops the output", event)
			}
			if strings.TrimSpace(added) != strings.TrimSpace(briefText) {
				t.Errorf("additionalContext = %q, want the brief file's text", added)
			}
		})
	}
}

// control proves the hook prints a brief where it has one, so a "prints nothing" case can't pass on a hook that never prints.
func control(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	writeChatFile(t, home, "home", "brief.md", briefText)
	if out := chatHook(t, home, "home", "SessionStart", `{"source":"startup"}`); out == "" {
		t.Fatal("the control brief printed nothing: the hook does not load a brief at all")
	}
}

func TestSessionStartPrintsNothingWithoutAUsableBrief(t *testing.T) {
	control(t)
	t.Run("no file", func(t *testing.T) {
		if out := chatHook(t, t.TempDir(), "home", "SessionStart", `{"source":"startup"}`); out != "" {
			t.Errorf("stdout = %q, want nothing", out)
		}
	})
	t.Run("an unreadable file", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "chat", "home", "brief.md"), 0o700); err != nil {
			t.Fatal(err)
		}
		if out := chatHook(t, home, "home", "SessionStart", `{"source":"startup"}`); out != "" {
			t.Errorf("stdout = %q, want nothing", out)
		}
	})
	t.Run("a fleet key that climbs out of chat", func(t *testing.T) {
		home := t.TempDir()
		writeChatFile(t, home, "home", "brief.md", briefText)
		if out := chatHook(t, home, "../chat/home", "SessionStart", `{"source":"startup"}`); out != "" {
			t.Errorf("stdout = %q, want nothing for a key that is not a fleet's", out)
		}
	})
	t.Run("a ship's hook", func(t *testing.T) {
		home := t.TempDir()
		writeChatFile(t, home, "home", "brief.md", briefText)
		if out := chatHook(t, home, "", "SessionStart", `{"source":"startup"}`); out != "" {
			t.Errorf("a ship's SessionStart printed %q, want nothing new", out)
		}
	})
	t.Run("an event that adds no context", func(t *testing.T) {
		home := t.TempDir()
		writeChatFile(t, home, "home", "brief.md", briefText)
		if out := chatHook(t, home, "home", "Stop", `{}`); out != "" {
			t.Errorf("Stop printed %q, want nothing", out)
		}
	})
}

func TestTheHookPrintsNoBriefOnACodexOrUnmarkedChat(t *testing.T) {
	home := t.TempDir()
	writeChatFile(t, home, "home", "brief.md", briefText)
	for _, harness := range []string{"codex", "copilot", ""} {
		t.Setenv("ADMIRAL_CHAT_HARNESS", harness)
		var out, errOut bytes.Buffer
		t.Setenv("ADMIRAL_HOME", home)
		t.Setenv("ADMIRAL_CHAT_FLEET", "home")
		t.Setenv("TMUX_PANE", "")
		if err := Hook([]string{"SessionStart"}, IO{In: strings.NewReader("{}"), Out: &out, Err: &errOut}); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Errorf("harness %q: SessionStart printed %q, want nothing: only Claude Code takes the brief", harness, out.String())
		}
	}
}

// seedMemory writes the instructions and a note the way the chat service does, and returns the note's id.
func seedMemory(t *testing.T, home, fleet, instr, note string) string {
	t.Helper()
	l := ledger.New(home, time.Now)
	if instr != "" {
		if _, err := l.SetInstructions(fleet, instr); err != nil {
			t.Fatal(err)
		}
	}
	if note == "" {
		return ""
	}
	e, err := l.AddNote(fleet, note, "admiralbot")
	if err != nil {
		t.Fatal(err)
	}
	return e.ID
}

func TestPreCompactInAChatPrintsTheInstructionsAndNoteIDsAsPlainText(t *testing.T) {
	home := t.TempDir()
	id := seedMemory(t, home, "home", "Ask before launching.", "deploys go out on Tuesdays")
	out := chatHook(t, home, "home", "PreCompact", `{"trigger":"auto","session_id":"s"}`)
	for _, want := range []string{"Ask before launching.", id, "deploys go out on Tuesdays"} {
		if !strings.Contains(out, want) {
			t.Errorf("PreCompact stdout lacks %q: %q", want, out)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("PreCompact stdout is JSON (%q), want plain text: Claude Code has no hookSpecificOutput form for it", out)
	}
	if len(strings.TrimSuffix(out, "\n")) > 9500 {
		t.Errorf("PreCompact stdout is %d bytes, want at most 9500", len(out))
	}
}

func TestPreCompactPrintsNothingWithoutMemoryOrOutsideAChat(t *testing.T) {
	home := t.TempDir()
	seedMemory(t, home, "home", "Ask before launching.", "deploys go out on Tuesdays")
	if out := chatHook(t, home, "home", "PreCompact", `{"trigger":"auto"}`); out == "" {
		t.Fatal("the control PreCompact printed nothing: the hook does not keep memory at all")
	}
	t.Run("a fleet with no files", func(t *testing.T) {
		if out := chatHook(t, t.TempDir(), "home", "PreCompact", `{"trigger":"auto"}`); out != "" {
			t.Errorf("stdout = %q, want nothing", out)
		}
	})
	t.Run("a ship's hook", func(t *testing.T) {
		if out := chatHook(t, home, "", "PreCompact", `{"trigger":"auto"}`); out != "" {
			t.Errorf("a ship's PreCompact printed %q, want nothing", out)
		}
	})
	t.Run("a corrupt ledger and an instructions directory", func(t *testing.T) {
		h := t.TempDir()
		writeChatFile(t, h, "home", "ledger.jsonl", "not json\n{")
		if err := os.MkdirAll(filepath.Join(h, "chat", "home", "instructions.md"), 0o700); err != nil {
			t.Fatal(err)
		}
		if out := chatHook(t, h, "home", "PreCompact", `{"trigger":"auto"}`); out != "" {
			t.Errorf("a corrupt ledger and an unreadable instructions file printed %q, want nothing", out)
		}
	})
	t.Run("a ledger over the size bound", func(t *testing.T) {
		h := t.TempDir()
		seedMemory(t, h, "home", "Be brief.", "a note")
		f, err := os.OpenFile(filepath.Join(h, "chat", "home", "ledger.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := f.Truncate(5 << 20); err != nil {
			t.Fatal(err)
		}
		if out := chatHook(t, h, "home", "PreCompact", `{"trigger":"auto"}`); out != "" {
			t.Errorf("a 5 MiB ledger printed %d bytes, want nothing", len(out))
		}
	})
}

func TestPreCompactPrintsNothingOnACodexOrUnmarkedChat(t *testing.T) {
	home := t.TempDir()
	seedMemory(t, home, "home", "Ask before launching.", "deploys go out on Tuesdays")
	if out := chatHook(t, home, "home", "PreCompact", `{"trigger":"auto"}`); out == "" {
		t.Fatal("the control PreCompact printed nothing")
	}
	for _, harness := range []string{"codex", "copilot", ""} {
		t.Setenv("ADMIRAL_CHAT_HARNESS", harness)
		var out, errOut bytes.Buffer
		if err := Hook([]string{"PreCompact"}, IO{In: strings.NewReader(`{"trigger":"auto"}`), Out: &out, Err: &errOut}); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Errorf("harness %q: PreCompact printed %q, want nothing", harness, out.String())
		}
	}
}

func TestPreCompactInAChatKeepsEveryOpenCommitmentIDAndText(t *testing.T) {
	home := t.TempDir()
	seedMemory(t, home, "home", "Ask before launching.", "deploys go out on Tuesdays")
	line := `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"c-abc123","kind":"commitment","text":"remind me about the audit","trigger":{"kind":"none"},"action":"remind"}` + "\n"
	f, err := os.OpenFile(filepath.Join(home, "chat", "home", "ledger.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	out := chatHook(t, home, "home", "PreCompact", `{"trigger":"auto","session_id":"s"}`)
	for _, want := range []string{"c-abc123", "remind me about the audit", "Ask before launching.", "deploys go out on Tuesdays"} {
		if !strings.Contains(out, want) {
			t.Errorf("PreCompact stdout lacks %q: %q", want, out)
		}
	}
}
