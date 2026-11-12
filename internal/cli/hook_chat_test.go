package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/digest"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

const briefText = "## Instructions\nAsk before launching.\n\n## Notes\n- deploys go out on Tuesdays (via admiralbot)\n"

// chatHook runs Hook as the hook of fleet's chat; fleet "" is a ship's hook. TMUX points at an empty
// directory so the state write finds no tmux server, never the engineer's.
func chatHook(t *testing.T, home, fleet, event, payload string) string {
	t.Helper()
	t.Setenv("ADMIRAL_HOME", home)
	t.Setenv("ADMIRAL_CHAT_FLEET", fleet)
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

func TestUserPromptSubmitPrintsTheDigestAndNamesItsSequence(t *testing.T) {
	home := t.TempDir()
	if err := digest.Add(home, "home", "ship bold-heron moored with a PR open", "an Inbox item appeared"); err != nil {
		t.Fatal(err)
	}
	out := chatHook(t, home, "home", "UserPromptSubmit", `{"prompt":"hi"}`)
	event, added := hookContext(t, out)
	if event != "UserPromptSubmit" {
		t.Errorf("hookEventName = %q, want UserPromptSubmit", event)
	}
	for _, want := range []string{"Since your last turn:", "ship bold-heron moored with a PR open", "an Inbox item appeared"} {
		if !strings.Contains(added, want) {
			t.Errorf("additionalContext = %q, want it to hold %q", added, want)
		}
	}
	f, _ := digest.Read(home, "home")
	printed, seq := chatContext("UserPromptSubmit", home, "home")
	if len(f.Lines) != 2 || seq != f.Lines[1].Seq || printed == "" {
		t.Errorf("chatContext gave seq %d (output %q), want the highest line's sequence", seq, printed)
	}
}

func TestUserPromptSubmitPrintsNothingWithoutDigestLines(t *testing.T) {
	control(t)
	home := t.TempDir()
	if out := chatHook(t, home, "home", "UserPromptSubmit", `{"prompt":"hi"}`); out != "" {
		t.Errorf("with no digest the hook printed %q, want nothing", out)
	}
	_ = digest.Add(home, "home", "one")
	f, _ := digest.Read(home, "home")
	if len(f.Lines) != 1 {
		t.Fatalf("digest = %+v, want the line just added", f)
	}
	_ = digest.Ack(home, "home", f.Lines[0].Seq)
	if out := chatHook(t, home, "home", "UserPromptSubmit", `{"prompt":"hi"}`); out != "" {
		t.Errorf("with an emptied digest the hook printed %q, want nothing", out)
	}
	if _, seq := chatContext("UserPromptSubmit", home, "home"); seq != 0 {
		t.Errorf("seq = %d with nothing printed, want 0", seq)
	}
}

func TestAShipsUserPromptSubmitPrintsNoDigest(t *testing.T) {
	control(t)
	home := t.TempDir()
	_ = digest.Add(home, "home", "one")
	if out := chatHook(t, home, "", "UserPromptSubmit", `{"prompt":"hi"}`); out != "" {
		t.Errorf("a ship's hook printed %q, want nothing", out)
	}
}

func TestTheHookRecordsTheSequenceItPrintedOnItsPane(t *testing.T) {
	got := digestSeqCall("%7", 12)
	want := sessionhost.SetPaneCall("%7", sessionhost.Prefix+sessionhost.ChatDigestOpt, "12")
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("digestSeqCall = %q, want %q", got, want)
	}
	if got := digestSeqCall("%7", 0); got != nil {
		t.Errorf("digestSeqCall with nothing printed = %q, want no call", got)
	}
}

func TestALineWrittenAfterTheHookReadSurvivesItsAck(t *testing.T) {
	home := t.TempDir()
	_ = digest.Add(home, "home", "before the read")
	_, seq := chatContext("UserPromptSubmit", home, "home")
	_ = digest.Add(home, "home", "after the read")
	if err := digest.Ack(home, "home", seq); err != nil {
		t.Fatal(err)
	}
	f, _ := digest.Read(home, "home")
	if len(f.Lines) != 1 || f.Lines[0].Text != "after the read" {
		t.Errorf("digest after the ack = %+v, want only the line written after the read", f)
	}
}
