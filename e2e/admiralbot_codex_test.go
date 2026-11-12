package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/launcher"
)

// AdmiralBot on Codex. The fake codex needs three things the fake does not have yet (see the report):
//   - FAKE_CODEX_PROBE in the dock env picks what a fenced `codex exec` probe run does: pass (default),
//     shell-ran (its shell tool ran), login-failed (exit 1, 401 on stderr), plugin-loaded (a remote plugin's
//     server starts in the throwaway home). A fenced run is one whose argv or $CODEX_HOME/config.toml names the
//     probe server (ADMIRAL_PROBE_DIR); any other `codex exec` stays a judge call.
//   - the cue `mcp <name>` also works for Codex: it takes the server from the -c flags or $CODEX_HOME/config.toml.
//   - the cue `codexreply <text>`: appends the engineer's message and an assistant line holding <text> to a rollout
//     under $CODEX_HOME/sessions, then sets the pane's @admiral-transcript to it, as the SessionStart hook would.
//     It also writes the `usage` cue's token_count line, answers every pasted message like Claude's `reply`, and
//     appends every byte it reads to `stdin`.
//
// codexFenceKeys, the probes' and the launcher's fence tables, and the literals below are one placeholder: the real
// Codex keys change in all three places together.

// codexResetCommand is what New chat types into a running Codex chat: /new, which Codex's TUI lists as starting a new chat.
const codexResetCommand = "/new"

const codexStdinReset = codexResetCommand + "\r"

func newCodexBotWorld(t *testing.T, probe string) (*chatWorld, string) {
	t.Helper()
	w := newWorld(t)
	c := &chatWorld{world: w, cfgDir: filepath.Join(w.root, "cfg")}
	eng := filepath.Join(w.root, "engineer-codex")
	if err := os.MkdirAll(eng, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eng, "auth.json"), []byte("fake-codex-login"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eng, "config.toml"), []byte("[mcp_servers.engineer_only]\ncommand = \"/bin/false\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"FAKE_RECORD_DIR": w.rec, "FAKE_CUE": w.cue, "CODEX_HOME": eng, "FAKE_CODEX_PROBE": probe,
		"ADMIRAL_SHIP": "stray-ship", "ADMIRAL_REPORT": "/stray/report"}
	cfg := map[string]any{"version": 1, "fleets": map[string]any{
		"chat": map[string]any{"name": "Chat", "search_roots": []string{w.work},
			"codex": map[string]any{"executable": w.fakeExe("codex"), "args": []string{"--fa-chat", "--dangerously-bypass-approvals-and-sandbox"}, "env": env},
			"chat":  map[string]any{"harness": "codex", "model": "gpt-6.1-sol", "effort": "high"}}}}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	return c, eng
}

func codexChatStart(c *chatWorld) (int, string) {
	return send(c.t, http.MethodPost, c.in.LocalURL+"/api/chat/chat/start", nil)
}

func TestAdmiralBot_CodexChatStartsOnlyOnceBothProbesPassAndIsFenced(t *testing.T) {
	t.Parallel()
	c, eng := newCodexBotWorld(t, "pass")
	w := c.world
	w.writeCue("codexready", "mcp ships", "codexreply Two ships are at anchor.")
	w.start()

	// Greyed until the engineer checks: nothing is stored.
	if code, body := codexChatStart(c); code == http.StatusOK {
		t.Fatalf("a Codex chat started with its probes unchecked: %s", body)
	}
	if got := w.capsOf("chat", "codex").status("bot.mcp_fence"); got != "not_checked" {
		t.Fatalf("before the check bot.mcp_fence = %q, want not_checked", got)
	}

	// Check again runs both probes.
	if code, body := w.check("chat", "codex"); code != http.StatusOK {
		t.Fatalf("POST check = %d, want 200: %s", code, body)
	}
	h := w.capsOf("chat", "codex")
	for _, id := range []string{"bot.mcp_fence", "bot.config_home"} {
		if s := h.status(id); s != "passed" {
			t.Fatalf("after the check %s = %q, want passed (version %q)", id, s, h.Version)
		}
	}
	if !capabilities.Open(w.home).Proven("chat", "codex", capabilities.BotMCPFence) {
		t.Error("the store does not hold bot.mcp_fence as proven under the detected version")
	}

	c.start()
	r := w.record(chatSessionName)
	if r.harness != "codex" {
		t.Fatalf("the chat ran %q, want codex", r.harness)
	}
	chatHome := launcher.ChatCodexHome(w.home, "chat")
	if chatHome == "" {
		t.Fatal("launcher.ChatCodexHome is empty")
	}

	t.Run("argv", func(t *testing.T) {
		cfg := map[string]string{}
		for i := 0; i+1 < len(r.argv); i++ {
			switch r.argv[i] {
			case "-c", "--config":
				if k, v, ok := strings.Cut(r.argv[i+1], "="); ok {
					cfg[k] = v
				}
			case "--disable":
				cfg["features."+r.argv[i+1]] = "false"
			}
		}
		hasKey := false
		for k := range cfg {
			hasKey = hasKey || strings.HasPrefix(k, "mcp_servers.admiral")
		}
		if !hasKey {
			t.Errorf("argv = %q names no mcp_servers.admiral", r.argv)
		}
		if !slices.Contains(r.argv, "--strict-config") {
			t.Errorf("argv = %q lacks --strict-config", r.argv)
		}
		if cfg["features.shell_tool"] != "false" {
			t.Errorf("argv = %q leaves the shell tool on", r.argv)
		}
		for _, a := range r.argv {
			if a == "--fa-chat" || strings.Contains(a, "dangerously") {
				t.Errorf("argv = %q carries %q: the chat takes no dock args and never bypasses a trust or sandbox", r.argv, a)
			}
		}
		if !slices.Contains(r.argv, "-m") && cfg["model"] == "" {
			t.Errorf("argv = %q carries no model", r.argv)
		}
		// Only the variable naming the token's file may be on argv: the token's own value is in the 0600 file.
		tokenFile := launcher.CodexTokenFile(w.home, "chat")
		fi, err := os.Stat(tokenFile)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("the token file %s: %v (%v), want a 0600 file", tokenFile, err, fi)
		}
		tok, _ := os.ReadFile(tokenFile)
		if strings.TrimSpace(string(tok)) == "" {
			t.Errorf("the token file is empty")
		}
		for _, a := range r.argv {
			if strings.TrimSpace(string(tok)) != "" && strings.Contains(a, strings.TrimSpace(string(tok))) {
				t.Errorf("argv carries the actor token: %q", a)
			}
		}
	})

	t.Run("env and home", func(t *testing.T) {
		if got := r.env["CODEX_HOME"]; got != chatHome {
			t.Errorf("CODEX_HOME = %q, want the chat's own %q", got, chatHome)
		}
		for _, k := range []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX"} {
			if v, ok := r.env[k]; ok {
				t.Errorf("env holds %s=%q", k, v)
			}
		}
		target, err := os.Readlink(filepath.Join(chatHome, "auth.json"))
		if err != nil || target != filepath.Join(eng, "auth.json") {
			t.Errorf("chat auth.json -> %q (%v), want the engineer's", target, err)
		}
		if _, err := os.Lstat(filepath.Join(chatHome, "plugins")); err == nil {
			t.Error("the chat home holds a plugins directory: only the login link is the engineer's")
		}
		if b, _ := os.ReadFile(filepath.Join(eng, "config.toml")); !strings.Contains(string(b), "engineer_only") {
			t.Error("the engineer's config.toml changed")
		}
	})

	t.Run("the first message and its reply show with nothing typed in the terminal", func(t *testing.T) {
		// A fresh trusted Codex chat has no transcript until its first turn: it takes a message once its composer shows.
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
			t.Fatalf("POST /api/chat/chat/messages on a fresh trusted Codex chat = %d, want 200: %s", code, body)
		}
		got := c.mcpCall("ships", "list_ships", map[string]any{})
		if got.IsError {
			t.Fatalf("list_ships over the Codex chat's MCP server was refused: %s", got.text())
		}
		c.waitSession(15*time.Second, "the first message and the reply from the chat home's rollout", func(s chatSession) bool {
			m := texts(s.Messages)
			return slices.Contains(m, "user: Which ships are at anchor?") && slices.Contains(m, "assistant: Two ships are at anchor.")
		})
	})

	t.Run("New chat resets with Codex's own command and records the chat's usage", func(t *testing.T) {
		if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/chat/new", nil); code != http.StatusOK {
			t.Fatalf("POST /api/chat/chat/new = %d, want 200: %s", code, body)
		}
		first := "Which ships are at anchor?\r"
		c.waitStdinOneOf(first+codexStdinReset, first+"\x1b[200~"+codexResetCommand+"\x1b[201~\r")
		got := lastOf(w.waitEvent(chatSessionName, "usage", 1), "usage")
		tokens, _ := got["tokens"].(map[string]any)
		if got["fleet"] != "chat" || got["harness"] != "codex" || tokens == nil || tokens["input"] == 0.0 {
			t.Errorf("the chat's usage record = %v, want fleet chat, harness codex and the rollout's token figures", got)
		}
	})
}

func TestAdmiralBot_CodexChatStaysGreyedWhenAProbeFails(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"shell-ran", "login-failed", "plugin-loaded"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := newCodexBotWorld(t, mode)
			w := c.world
			w.start()
			if code, body := w.check("chat", "codex"); code != http.StatusOK {
				t.Fatalf("POST check = %d, want 200: %s", code, body)
			}
			h := w.capsOf("chat", "codex")
			failed := ""
			for _, id := range []string{"bot.mcp_fence", "bot.config_home"} {
				if h.status(id) == "failed" {
					failed = id
				}
			}
			if failed == "" {
				t.Fatalf("with FAKE_CODEX_PROBE=%s neither probe failed: %+v", mode, h)
			}
			if code, body := codexChatStart(c); code == http.StatusOK {
				t.Errorf("the chat started with %s failed: %s", failed, body)
			}
			// A chat that is off answers its session with a 409; a 200 must not show it running.
			if code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/chat/chat/session", nil); code == http.StatusOK && strings.Contains(body, `"running":true`) {
				t.Errorf("the session is running with %s failed: %s", failed, body)
			}
			home := launcher.ChatCodexHome(w.home, "chat")
			if home == "" {
				t.Fatal("launcher.ChatCodexHome is empty")
			}
			if _, err := os.Stat(home); err == nil {
				t.Errorf("a refused start created the chat's CODEX_HOME")
			}
		})
	}
}
