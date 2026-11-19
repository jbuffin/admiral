package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmuxLog puts a tmux on PATH that appends each call's arguments to the returned file.
func fakeTmuxLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// leftoverDigest is the file an older install's server wrote, seeded raw so no digest package is needed.
const leftoverDigest = `{"next_seq":3,"lines":[{"seq":1,"text":"ship bold-heron moored with a PR open"},{"seq":2,"text":"an Inbox item appeared"}]}`

func TestAChatsUserPromptSubmitAddsNoDigestEvenWithALeftoverDigestFile(t *testing.T) {
	log := fakeTmuxLog(t)
	home := t.TempDir()
	writeChatFile(t, home, "home", "digest.json", leftoverDigest)

	out := chatHook(t, home, "home", "UserPromptSubmit", `{"prompt":"hi"}`)

	if out != "" {
		t.Errorf("UserPromptSubmit printed %q, want nothing: the engineer's message reaches AdmiralBot with no digest", out)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the hook made no tmux call at all: %v", err)
	}
	if !strings.Contains(string(calls), "@admiral-chat-prompt") {
		t.Errorf("tmux calls = %q, want the chat prompt stamp still set", calls)
	}
	if strings.Contains(string(calls), "@admiral-chat-digest") {
		t.Errorf("tmux calls = %q, want no @admiral-chat-digest pane option", calls)
	}
}
