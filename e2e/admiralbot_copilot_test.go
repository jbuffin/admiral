package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/launcher"
)

// AdmiralBot on Copilot. The fake copilot needs these things the fake does not have yet (see the report):
//   - FAKE_COPILOT_PROBE in the dock env picks what a fenced `copilot -p … --output-format json` probe run does: pass
//     (default), shell-ran (its shell tool ran), login-failed (exit 1, "not logged in" on stderr), plugin-loaded (a
//     plugin lands in $COPILOT_HOME/installed-plugins). A fenced run is one whose argv names the probe server
//     (ADMIRAL_PROBE_DIR, inline or as @file); any other `copilot -p` stays a headless run.
//   - the cue `mcp <name>` also works for Copilot: it takes the server from --additional-mcp-config.
//   - the cue `copilotready`: a Copilot TUI waiting for its first message, showing its composer (the folder is
//     already trusted in $COPILOT_HOME/config.json); `copilotreply <text>` does that too after the cues before it,
//     answers every pasted message by appending the engineer's message and an assistant line holding <text> to
//     $COPILOT_HOME/session-state/<--session-id>/events.jsonl, sets the pane's @admiral-transcript to it as the
//     sessionStart hook would, and on /clear writes a session.shutdown line and starts a new session id, bound at the
//     next message with source "new", as the real CLI does. It appends every byte it reads to `stdin`.

// copilotResetCommand is what New chat types into a running Copilot chat.
const copilotResetCommand = "/clear"

const copilotStdinReset = copilotResetCommand + "\r"

var copilotUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func newCopilotBotWorld(t *testing.T, probe string) (*chatWorld, string) {
	t.Helper()
	w := newWorld(t)
	c := &chatWorld{world: w, cfgDir: filepath.Join(w.root, "cfg")}
	eng := filepath.Join(w.root, "engineer-copilot")
	if err := os.MkdirAll(eng, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eng, "mcp-config.json"), []byte(`{"mcpServers":{"engineer_only":{"command":"/bin/false"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"FAKE_RECORD_DIR": w.rec, "FAKE_CUE": w.cue, "COPILOT_HOME": eng, "FAKE_COPILOT_PROBE": probe,
		"ADMIRAL_SHIP": "stray-ship", "ADMIRAL_REPORT": "/stray/report", "GH_TOKEN": "fake-token-dock"}
	cfg := map[string]any{"version": 1, "fleets": map[string]any{
		"chat": map[string]any{"name": "Chat", "search_roots": []string{w.work},
			"copilot": map[string]any{"executable": w.fakeExe("copilot"), "args": []string{"--fa-chat", "--allow-all"}, "env": env},
			"chat":    map[string]any{"harness": "copilot", "model": "gpt-5.6-sol"}}}}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	return c, eng
}

func copilotChatStart(c *chatWorld) (int, string) {
	return send(c.t, http.MethodPost, c.in.LocalURL+"/api/chat/chat/start", nil)
}

func TestAdmiralBot_CopilotChatStartsOnlyOnceBothProbesPassAndIsFenced(t *testing.T) {
	t.Parallel()
	c, eng := newCopilotBotWorld(t, "pass")
	w := c.world
	w.writeCue("copilotready", "mcp ships", "copilotreply Two ships are at anchor.")
	w.start()

	// Greyed until the engineer checks: nothing is stored.
	if code, body := copilotChatStart(c); code == http.StatusOK {
		t.Fatalf("a Copilot chat started with its probes unchecked: %s", body)
	}
	if got := w.capsOf("chat", "copilot").status("bot.mcp_fence"); got != "not_checked" {
		t.Fatalf("before the check bot.mcp_fence = %q, want not_checked", got)
	}

	// Check again runs both probes.
	if code, body := w.check("chat", "copilot"); code != http.StatusOK {
		t.Fatalf("POST check = %d, want 200: %s", code, body)
	}
	h := w.capsOf("chat", "copilot")
	for _, id := range []string{"bot.mcp_fence", "bot.config_home"} {
		if s := h.status(id); s != "passed" {
			t.Fatalf("after the check %s = %q, want passed (version %q)", id, s, h.Version)
		}
	}
	if !capabilities.Open(w.home).Proven("chat", "copilot", capabilities.BotMCPFence) {
		t.Error("the store does not hold bot.mcp_fence as proven under the detected version")
	}

	c.start()
	r := w.record(chatSessionName)
	if r.harness != "copilot" {
		t.Fatalf("the chat ran %q, want copilot", r.harness)
	}
	chatHome := launcher.ChatCopilotHome(w.home, "chat")
	if chatHome == "" {
		t.Fatal("launcher.ChatCopilotHome is empty")
	}

	t.Run("argv", func(t *testing.T) {
		for _, f := range []string{"--disable-builtin-mcps", "--allow-tool", "--available-tools", "--additional-mcp-config", "--session-id", "--model", "--plugin-dir"} {
			if !slices.ContainsFunc(r.argv, func(a string) bool { return a == f || strings.HasPrefix(a, f+"=") }) {
				t.Errorf("argv = %q lacks %s", r.argv, f)
			}
		}
		if i := slices.Index(r.argv, "--session-id"); i < 0 || i+1 >= len(r.argv) || !copilotUUIDRe.MatchString(r.argv[i+1]) {
			t.Errorf("argv = %q: --session-id is not followed by a UUID", r.argv)
		}
		for _, a := range r.argv {
			if a == "--fa-chat" || a == "--allow-all" || a == "--yolo" {
				t.Errorf("argv = %q carries %q: the chat takes no dock args and never allows everything", r.argv, a)
			}
		}
		// Only the variable naming the token's file may be on argv: the token's own value is in a 0600 file.
		var holders []string
		_ = filepath.WalkDir(filepath.Join(w.home, "chat", "chat"), func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if b, _ := os.ReadFile(p); strings.Contains(string(b), "ADMIRAL_ACTOR_TOKEN_FILE") {
					holders = append(holders, p)
				}
			}
			return nil
		})
		joined := strings.Join(r.argv, "\x00")
		if !strings.Contains(joined, "ADMIRAL_ACTOR_TOKEN_FILE") && len(holders) == 0 {
			t.Errorf("neither argv nor a chat file names ADMIRAL_ACTOR_TOKEN_FILE: the admiral server has no identity")
		}
		if strings.Contains(joined, "ADMIRAL_ACTOR_TOKEN\"") || strings.Contains(joined, "ADMIRAL_ACTOR_TOKEN=") {
			t.Errorf("argv carries the actor token itself: %q", r.argv)
		}
	})

	t.Run("env and home", func(t *testing.T) {
		if got := r.env["COPILOT_HOME"]; got != chatHome {
			t.Errorf("COPILOT_HOME = %q, want the chat's own %q", got, chatHome)
		}
		if got := r.env["COPILOT_PLUGIN_DIR_ONLY"]; got != "true" {
			t.Errorf("COPILOT_PLUGIN_DIR_ONLY = %q, want true", got)
		}
		for _, k := range []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX", "GH_TOKEN", "GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN"} {
			if v, ok := r.env[k]; ok {
				t.Errorf("env holds %s=%q", k, v)
			}
		}
		for _, name := range []string{"mcp-config.json", "settings.json", "installed-plugins", "agents", "skills", "hooks"} {
			if _, err := os.Lstat(filepath.Join(chatHome, name)); err == nil {
				t.Errorf("the chat home holds %s: nothing of the engineer's config is the chat's", name)
			}
		}
		if b, _ := os.ReadFile(filepath.Join(eng, "mcp-config.json")); !strings.Contains(string(b), "engineer_only") {
			t.Error("the engineer's mcp-config.json changed")
		}
	})

	t.Run("the first message and its reply show with nothing typed in the terminal", func(t *testing.T) {
		var code int
		var body string
		deadline := time.Now().Add(15 * time.Second)
		for {
			code, body = send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/messages", map[string]any{"text": "Which ships are at anchor?"})
			if code != http.StatusConflict || time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if code != http.StatusOK {
			t.Fatalf("POST /api/chat/chat/messages on a fresh trusted Copilot chat = %d, want 200: %s", code, body)
		}
		got := c.mcpCall("ships", "list_ships", map[string]any{})
		if got.IsError {
			t.Fatalf("list_ships over the Copilot chat's MCP server was refused: %s", got.text())
		}
		c.waitSession(15*time.Second, "the first message and the reply from the chat home's session-state", func(s chatSession) bool {
			m := texts(s.Messages)
			return slices.Contains(m, "user: Which ships are at anchor?") && slices.Contains(m, "assistant: Two ships are at anchor.")
		})
	})

	t.Run("New chat sends /clear, follows the new session and records the chat's usage", func(t *testing.T) {
		if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/new", nil); code != http.StatusOK {
			t.Fatalf("POST /api/chat/chat/new = %d, want 200: %s", code, body)
		}
		first := "Which ships are at anchor?\r"
		c.waitStdinOneOf(first+copilotStdinReset, first+"\x1b[200~"+copilotResetCommand+"\x1b[201~\r")
		got := lastOf(w.waitEvent(chatSessionName, "usage", 1), "usage")
		if got["fleet"] != "chat" || got["harness"] != "copilot" {
			t.Errorf("the chat's usage record = %v, want fleet chat and harness copilot", got)
		}
		// The reply to a message sent after /clear can only come from the new session's transcript.
		var code int
		var body string
		for end := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			code, body = send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/messages", map[string]any{"text": "And the harbours?"})
			if code != http.StatusConflict || time.Now().After(end) {
				break
			}
		}
		if code != http.StatusOK {
			t.Fatalf("POST /api/chat/chat/messages after /clear = %d, want 200: %s", code, body)
		}
		// The history keeps what came before the New chat marker; after it only the new session's exchange may show.
		c.waitSession(15*time.Second, "only the new session's exchange after the New chat marker", func(s chatSession) bool {
			i := slices.IndexFunc(s.Messages, func(m chatMsg) bool { return m.Kind == "reset" })
			if i < 0 {
				return false
			}
			m := texts(s.Messages[i+1:])
			return slices.Contains(m, "user: And the harbours?") && slices.Contains(m, "assistant: Two ships are at anchor.") &&
				!slices.Contains(m, "user: Which ships are at anchor?")
		})
	})
}

func TestAdmiralBot_CopilotChatStaysGreyedWhenAProbeFails(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"shell-ran", "login-failed", "plugin-loaded"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := newCopilotBotWorld(t, mode)
			w := c.world
			w.start()
			if code, body := w.check("chat", "copilot"); code != http.StatusOK {
				t.Fatalf("POST check = %d, want 200: %s", code, body)
			}
			h := w.capsOf("chat", "copilot")
			failed := ""
			for _, id := range []string{"bot.mcp_fence", "bot.config_home"} {
				if h.status(id) == "failed" {
					failed = id
				}
			}
			if failed == "" {
				t.Fatalf("with FAKE_COPILOT_PROBE=%s neither probe failed: %+v", mode, h)
			}
			if code, body := copilotChatStart(c); code == http.StatusOK {
				t.Errorf("the chat started with %s failed: %s", failed, body)
			}
			if code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/chat/chat/session", nil); code == http.StatusOK && strings.Contains(body, `"running":true`) {
				t.Errorf("the session is running with %s failed: %s", failed, body)
			}
			home := launcher.ChatCopilotHome(w.home, "chat")
			if home == "" {
				t.Fatal("launcher.ChatCopilotHome is empty")
			}
			if _, err := os.Stat(home); err == nil {
				t.Errorf("a refused start created the chat's COPILOT_HOME")
			}
		})
	}
}
