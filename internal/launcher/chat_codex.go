package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
)

// ChatCodexHome is a fleet's Codex chat's own CODEX_HOME: it holds the link to the engineer's auth.json and what Codex writes.
func ChatCodexHome(home, fleet string) string {
	return filepath.Join(ChatDir(home, fleet), "codex-home")
}

// CodexTokenFile is where the Codex chat's actor token waits for its MCP server to read: Codex's argv and the tmux
// session's env are readable by other processes, so neither carries the token.
func CodexTokenFile(home, fleet string) string {
	return filepath.Join(ChatDir(home, fleet), "admiral-mcp-token")
}

// Every default-on feature of Codex that adds a tool or loads a plugin. `codex features list` shows them on by default,
// and `--disable <name>` is `-c features.<name>=false`. code_mode_host stays on: with it off the model has no route to an
// MCP tool at all, because Codex calls them through its exec tool.
var codexFenceFeatures = []string{"unified_exec", "apps", "plugins", "remote_plugin", "plugin_discovery_prefetch",
	"skill_mcp_dependency_install", "tool_suggest", "browser_use", "browser_use_external", "in_app_browser",
	"computer_use", "image_generation", "view_image", "multi_agent", "sleep_tool", "goals"}

// CodexFenceArgs are the flags that fence a Codex run to the one MCP server named: no shell, no web search, no
// plugin or connector, a read-only sandbox that never asks, and that server's tools approved without a prompt.
// The chat and the fence probe both run with exactly these.
func CodexFenceArgs(server string) []string {
	return append(CodexBaseFenceArgs(), "-c", "mcp_servers."+server+`.default_tools_approval_mode="approve"`)
}

// CodexBaseFenceArgs is the fence without a server: what a run in a home of its own takes so that nothing loads.
func CodexBaseFenceArgs() []string {
	// --strict-config makes Codex refuse a -c key it does not know, which it would otherwise accept and ignore.
	out := []string{"--strict-config", "-c", "features.shell_tool=false", "-c", `web_search="disabled"`,
		"-c", `sandbox_mode="read-only"`, "-c", `approval_policy="never"`}
	for _, f := range codexFenceFeatures {
		out = append(out, "--disable", f)
	}
	return out
}

// CodexLoginPath is the engineer's auth.json for a dock: the one file a Codex home takes from them.
func CodexLoginPath(dock config.Harness) string {
	return filepath.Join(config.IdentityDir("codex", dock.Env), "auth.json")
}

// CodexMCPServerArgs defines one MCP server by flags, so the chat's config.toml stays Codex's alone.
func CodexMCPServerArgs(name, command string, args []string, env map[string]string) []string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = strconv.Quote(a)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = strconv.Quote(k) + "=" + strconv.Quote(env[k])
	}
	p := "mcp_servers." + name
	return []string{"-c", p + ".command=" + strconv.Quote(command), "-c", p + ".args=[" + strings.Join(quoted, ",") + "]",
		"-c", p + ".env={" + strings.Join(pairs, ",") + "}"}
}

// LinkCodexLogin makes the engineer's login the only thing a Codex home takes from them: a link at rel under root to
// target, replaced at every start. A token refresh resolves the link and renames over the engineer's file; the whole policy is here,
// so a copy or no link is a change to this function alone.
func LinkCodexLogin(root *os.Root, rel, target string) error {
	if err := mkdirsIn(root, filepath.Dir(rel)); err != nil {
		return err
	}
	tmp := rel + ".new"
	_ = root.Remove(tmp)
	if err := root.Symlink(target, tmp); err != nil {
		return err
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// codexLogin is the engineer's auth.json for a fleet's Codex dock, or the refusal that names it.
func codexLogin(dock config.Harness) (string, *Error) {
	auth := CodexLoginPath(dock)
	if _, err := os.Stat(auth); err != nil {
		return "", refuse(http.StatusConflict, "the chat needs the Codex login at %s: log in to Codex there, then start the chat", auth)
	}
	return auth, nil
}

// prepareCodexChat sets up the chat's own CODEX_HOME and the token file, through root. Nothing of the engineer's Codex
// config, plugins or sessions goes in.
func prepareCodexChat(home, fleet, nonce, authTarget string) *Error {
	root, err := os.OpenRoot(home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not open %s: %v", home, err)
	}
	defer root.Close()
	rel := filepath.Join("chat", fleet)
	for _, stale := range []string{"AGENTS.md", "AGENTS.override.md", ".codex"} {
		if err := root.RemoveAll(filepath.Join(rel, stale)); err != nil && !os.IsNotExist(err) {
			return refuse(http.StatusInternalServerError, "could not clear the chat's directory: %v", err)
		}
	}
	if key := codexHomeConfigKey(root, filepath.Join(rel, "codex-home", "config.toml")); key != "" {
		return refuse(http.StatusConflict, "the chat's own Codex config (%s) sets %q, which the chat's fence does not cover: remove it, then start the chat", filepath.Join(ChatCodexHome(home, fleet), "config.toml"), key)
	}
	if err := LinkCodexLogin(root, filepath.Join(rel, "codex-home", "auth.json"), authTarget); err != nil {
		return refuse(http.StatusInternalServerError, "could not link the Codex login: %v", err)
	}
	minter, err := actor.NewMinter(home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not make the chat's identity: %v", err)
	}
	if err := writeChatFile(root, filepath.Join(rel, "admiral-mcp-token"), []byte(minter.Token(fleet, nonce)+"\n")); err != nil {
		return refuse(http.StatusInternalServerError, "could not write the chat's token: %v", err)
	}
	return nil
}

// codexConfigKeysRefused are the config.toml keys that load code or reroute the model, which the fence's -c flags do not name.
// hooks.state and the keys Codex writes itself are left alone.
var codexConfigKeysRefused = []string{"mcp_servers", "notify", "model_providers", "profile", "profiles"}

// codexHomeConfigKey is the first refused top-level key the chat home's config.toml holds, or "".
func codexHomeConfigKey(root *os.Root, rel string) string {
	b, err := root.ReadFile(rel)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		name, _, isKey := strings.Cut(line, "=")
		if strings.HasPrefix(line, "[") {
			name, isKey = strings.Trim(line, "[] \t"), true
		}
		if !isKey {
			continue
		}
		first, _, _ := strings.Cut(strings.TrimSpace(name), ".")
		first = strings.Trim(first, `"' `)
		if slices.Contains(codexConfigKeysRefused, first) {
			return first
		}
	}
	return ""
}

// codexChatArgv is the Codex TUI's argv for the chat: the dock's executable only, never its args. It runs without the
// shared daemon so it cannot join an app-server under the engineer's own config.
func codexChatArgv(exe string, noDaemon bool, chat config.ChatSettings, self, home, fleet, port string) []string {
	serverEnv := map[string]string{"ADMIRAL_ACTOR_TOKEN_FILE": CodexTokenFile(home, fleet), "ADMIRAL_HOME": home, "ADMIRAL_CHAT_FLEET": fleet}
	if port != "" {
		serverEnv["ADMIRAL_PORT"] = port
	}
	argv := []string{exe}
	// A Codex that predates --no-daemon would reject it, as codex-pane's fallback checks.
	if noDaemon {
		argv = append(argv, "--no-daemon")
	}
	argv = append(argv, "-m", chat.Model)
	if chat.Effort != "" {
		argv = append(argv, "-c", "model_reasoning_effort="+strconv.Quote(chat.Effort))
	}
	argv = append(argv, "-c", "developer_instructions="+strconv.Quote(chatPromptFor("codex")))
	argv = append(argv, CodexFenceArgs(ChatServer)...)
	argv = append(argv, CodexMCPServerArgs(ChatServer, self, []string{"mcp"}, serverEnv)...)
	return append(argv, codexFlags()...)
}
