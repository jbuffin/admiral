package launcher

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/actor"
)

// ChatCopilotHome is a fleet's Copilot chat's own COPILOT_HOME: what Copilot writes, and nothing of the engineer's config.
func ChatCopilotHome(home, fleet string) string {
	return filepath.Join(ChatDir(home, fleet), "copilot-home")
}

// ChatTools are the admiral MCP server's tools, which Copilot names admiral-<tool> and takes only by exact name: an
// allowlist entry it does not know is dropped with a notice, never matched by a glob. internal/cli checks this list
// against the server's own.
var ChatTools = []string{"list_ships", "read_conversation", "read_outcomes", "list_inbox", "list_sources", "list_campaigns",
	"launch_ship", "add_order", "answer_item", "next_prompt", "wake_ship", "stop_ship", "dismiss_ship",
	"list_routines", "get_standing_orders", "turn_routine_off", "hold_fire_on", "read_hold_fire", "ask_question", "save_note", "make_commitment", "work_on_campaign"}

// copilotExcludedTools are every built-in tool of Copilot 1.0.109 that the run's tool list could carry. --available-tools
// already leaves them out; --excluded-tools is a second layer for a name that list somehow matches. A name the CLI does
// not know costs a notice and nothing else, so the list keeps only names 1.0.109 accepts (it warns at start about
// write_bash, write, web_search and report_intent).
var copilotExcludedTools = []string{"bash", "read_bash", "stop_bash", "list_bash", "apply_patch", "view", "web_fetch",
	"fetch_copilot_cli_documentation", "semantic_code_search", "skill", "run_dynamic_workflow", "dynamic_workflows_manage", "sql",
	"session_store_sql", "read_agent", "list_agents", "write_agent", "rg", "grep", "glob", "task", "edit", "create",
	"ask_user", "store_memory"}

// CopilotBaseFenceArgs is the fence without a server: none of the built-in MCP servers, no question to the engineer,
// no self-update, no implicit access to the temp dir, no sync of the session to the engineer's GitHub account (chat
// history holds ship output) and every built-in tool excluded. A run in a home of its own takes it so that nothing else loads.
func CopilotBaseFenceArgs() []string {
	return []string{"--disable-builtin-mcps", "--no-ask-user", "--no-auto-update", "--disallow-temp-dir", "--sync-session=off",
		"--excluded-tools=" + strings.Join(copilotExcludedTools, ",")}
}

// CopilotFenceArgs are the flags that fence a Copilot run to the one MCP server named: the base fence and that
// server's tools approved without a prompt. CopilotToolArgs names the tools that stay. The chat and the fence probe
// both run with exactly these.
func CopilotFenceArgs(server string) []string {
	return append(CopilotBaseFenceArgs(), "--allow-tool="+server)
}

// CopilotToolArgs allow only the named tools of the server, which takes every built-in tool out of the run.
func CopilotToolArgs(server string, tools ...string) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = "--available-tools=" + server + "-" + t
	}
	return out
}

// CopilotEnvKept reports whether a variable of the engineer's environment or dock may reach a Copilot run in a home of
// its own: another login, provider, skill dir or config would otherwise ride in with it.
func CopilotEnvKept(name string) bool {
	switch name {
	case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST":
		return false
	}
	// OTEL_* would point the run's telemetry, which carries prompts, at another collector.
	return !strings.HasPrefix(name, "COPILOT_") && !strings.HasPrefix(name, "OTEL_")
}

// copilotStaleFiles are what Copilot loads from a trusted working directory: MCP configs, hooks, agents, skills and
// instructions, in its own layout and in the layouts it reads for other tools.
var copilotStaleFiles = []string{".mcp.json", ".github", ".vscode", ".copilot", ".agents", ".claude", "CLAUDE.md", "CLAUDE.local.md",
	"AGENTS.md", "GEMINI.md"}

// copilotHomeAllowed are the entries Admiral writes into the chat's own Copilot home and the ones Copilot 1.0.109
// creates there itself. Anything else (an MCP config, a hook, a plugin, a provider, an agent, a skill) would add what
// the fence's flags do not name, so it refuses the start.
var copilotHomeAllowed = map[string]bool{"copilot-instructions.md": true, "config.json": true, "installed-plugins": true, "logs": true,
	"plugin-data": true, "session-state": true, "settings.json": true, "settings.json.lock": true, "ide": true, "open-sessions-state.json": true, "servers": true}

// copilotHomeAllowedPrefix are the entries Copilot creates under a name that varies (SQLite's -wal and -shm files, the sidebar state).
var copilotHomeAllowedPrefix = []string{"session-store.db", "sidebar-sessions-state"}

// copilotConfigKeys are the keys Copilot writes itself into config.json, and copilotSettingsKeys the ones of settings.json
// that load nothing; any other key could add a server, hook, provider or plugin, so it refuses the start.
var (
	copilotConfigKeys = map[string]bool{"appInstallNudgeResponded": true, "appTipShown": true, "askedSetupTerminals": true, "firstLaunchAt": true,
		"lastLoggedInUser": true, "loggedInUsers": true, "reasoningSummariesCleanupDone": true, "recentModelIds": true, "trustedFolders": true}
	copilotSettingsKeys = map[string]bool{"model": true, "renderMarkdown": true}
)

// copilotHomeRefusal is the first entry of the chat's Copilot home that adds what the fence does not cover, with the
// reason, or "".
func copilotHomeRefusal(root *os.Root, home string) string {
	d, err := root.Open(home)
	if err != nil {
		return home + " (" + err.Error() + ")"
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return home + " (" + err.Error() + ")"
	}
	for _, e := range entries {
		name := e.Name()
		rel := filepath.Join(home, name)
		fi, err := root.Lstat(rel)
		switch {
		case err != nil:
			return name + " (" + err.Error() + ")"
		case fi.Mode()&os.ModeSymlink != 0:
			return name + " (a symlink)"
		case !copilotHomeAllowed[name] && !slices.ContainsFunc(copilotHomeAllowedPrefix, func(p string) bool { return strings.HasPrefix(name, p) }):
			return name
		case name == "installed-plugins" && fi.IsDir():
			if es, err := readDirIn(root, rel); err != nil || len(es) > 0 {
				return name + " (a plugin is installed)"
			}
		case name == "config.json" || name == "settings.json":
			keys := copilotConfigKeys
			if name == "settings.json" {
				keys = copilotSettingsKeys
			}
			if key := copilotConfigRefusal(root, rel, keys); key != "" {
				return name + " (" + key + ")"
			}
		}
	}
	return ""
}

// readDirIn lists a directory through root; a directory that cannot be read is an error, never an empty one.
func readDirIn(root *os.Root, rel string) ([]os.DirEntry, error) {
	d, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadDir(-1)
}

// copilotConfigRefusal says what in the JSON object at rel is not Copilot's own: a key outside keys, a file that is not
// an object or one that cannot be read. Copilot heads config.json with // comment lines, which are skipped.
func copilotConfigRefusal(root *os.Root, rel string, keys map[string]bool) string {
	b, err := root.ReadFile(rel)
	if err != nil {
		return err.Error()
	}
	var body []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			body = append(body, l)
		}
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.Join(body, "\n")), &m) != nil {
		return "not a JSON object"
	}
	for k := range m {
		if !keys[k] {
			return "key " + k
		}
	}
	return ""
}

// prepareCopilotChat sets up the chat's own COPILOT_HOME, its instructions and the token file, through
// root. Nothing of the engineer's Copilot config, plugins, hooks or sessions goes in; the login stays in the Keychain.
func prepareCopilotChat(home, fleet, nonce string) *Error {
	root, err := os.OpenRoot(home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not open %s: %v", home, err)
	}
	defer root.Close()
	if dir := gitWorkTreeAbove(ChatDir(home, fleet)); dir != "" {
		return refuse(http.StatusConflict, "the chat's directory %s sits inside a git work tree at %s, and Copilot would load that repo's instructions, MCP servers and hooks: set ADMIRAL_HOME outside any repository", ChatDir(home, fleet), dir)
	}
	rel := filepath.Join("chat", fleet)
	for _, stale := range copilotStaleFiles {
		if err := root.RemoveAll(filepath.Join(rel, stale)); err != nil && !os.IsNotExist(err) {
			return refuse(http.StatusInternalServerError, "could not clear the chat's directory: %v", err)
		}
	}
	homeRel := filepath.Join(rel, "copilot-home")
	if err := mkdirsIn(root, homeRel); err != nil {
		return refuse(http.StatusInternalServerError, "could not create the chat's Copilot home: %v", err)
	}
	if name := copilotHomeRefusal(root, homeRel); name != "" {
		return refuse(http.StatusConflict, "the chat's own Copilot home (%s) holds %s, which the chat's fence does not cover: remove it, then start the chat", ChatCopilotHome(home, fleet), name)
	}
	minter, err := actor.NewMinter(home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not make the chat's identity: %v", err)
	}
	if err := writeChatFile(root, filepath.Join(rel, "admiral-mcp-token"), []byte(minter.Token(fleet, nonce)+"\n")); err != nil {
		return refuse(http.StatusInternalServerError, "could not write the chat's token: %v", err)
	}
	// Copilot reads this from its home whether or not the folder is trusted.
	if err := writeChatFile(root, filepath.Join(homeRel, "copilot-instructions.md"), []byte(CopilotChatInstructions())); err != nil {
		return refuse(http.StatusInternalServerError, "could not write the chat's instructions: %v", err)
	}
	return nil
}

// copilotMCPConfig is the admiral server as --additional-mcp-config takes it inline. Its env names the token's file and
// never the token, so argv shows nothing secret.
func copilotMCPConfig(self string, serverEnv map[string]string) string {
	raw, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{ChatServer: map[string]any{
		"type": "local", "command": self, "args": []string{"mcp"}, "tools": []string{"*"}, "env": serverEnv}}})
	return string(raw)
}

// gitWorkTreeAbove is the nearest directory at or above dir that holds a .git (a directory or a worktree's file), or "".
func gitWorkTreeAbove(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// copilotSessionID is a new version 4 UUID: --session-id makes the transcript's directory known at start.
func copilotSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// copilotChatArgv is the Copilot TUI's argv for the chat: the dock's executable only, never its args. The token reaches
// the MCP server through a file named in the server's env, so neither argv nor the tmux session carries it.
// CopilotPluginDir is Admiral's Copilot plugin (the ship hooks) under an Admiral home that WriteBundle filled.
func CopilotPluginDir(home string) string { return copilotPluginDir(home) }

// CopilotChatInstructions are the chat's instructions as the home's copilot-instructions.md carries them.
func CopilotChatInstructions() string { return chatPromptFor("copilot") + "\n" }

// CopilotHookTypes are the hook events Admiral's own Copilot plugin registers.
func CopilotHookTypes() []string {
	var out []string
	for k := range copilotHooks().(map[string]any)["hooks"].(map[string]any) {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func copilotChatArgv(exe, model, effort, sessionID, home, mcpConfig string) []string {
	argv := []string{exe, "--model", model}
	if effort != "" {
		argv = append(argv, "--reasoning-effort", effort)
	}
	argv = append(argv, "--session-id", sessionID, "--plugin-dir", copilotPluginDir(home))
	argv = append(argv, CopilotFenceArgs(ChatServer)...)
	argv = append(argv, CopilotToolArgs(ChatServer, ChatTools...)...)
	return append(argv, "--additional-mcp-config", mcpConfig)
}
