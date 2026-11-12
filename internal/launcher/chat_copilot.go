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
	"launch_ship", "add_order", "answer_item", "next_prompt", "wake_ship", "stop_ship", "dismiss_ship", "ask_question"}

// CopilotBaseFenceArgs is the fence without a server: none of the built-in MCP servers, no question to the engineer
// and no self-update. A run in a home of its own takes it so that nothing else loads.
func CopilotBaseFenceArgs() []string {
	return []string{"--disable-builtin-mcps", "--no-ask-user", "--no-auto-update"}
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
	return !strings.HasPrefix(name, "COPILOT_")
}

// copilotStaleFiles are what Copilot loads from a trusted working directory: MCP configs, hooks, agents, skills and
// instructions, in its own layout and in the layouts it reads for other tools.
var copilotStaleFiles = []string{".mcp.json", ".github", ".vscode", ".copilot", ".agents", ".claude", "CLAUDE.md", "CLAUDE.local.md",
	"AGENTS.md", "GEMINI.md"}

// copilotHomeRefused are the places in the chat's own home that add a server, plugin, hook, provider, agent or skill,
// which the fence's flags do not name. What Copilot writes itself (config.json, logs, session-state and the like) is left alone.
var copilotHomeRefused = []string{"mcp-config.json", "settings.json", "providers.json", "installed-plugins", "agents", "skills", "hooks"}

// copilotSettingsRefused are the keys of the home's settings.json that load code; the model and the like are left alone.
var copilotSettingsRefused = []string{"hooks", "enabledPlugins", "extraKnownMarketplaces", "mcpServers", "providers"}

// copilotHomeRefusal is the first entry of the chat's Copilot home that adds what the fence does not cover, or "".
func copilotHomeRefusal(root *os.Root, home string) string {
	for _, name := range copilotHomeRefused {
		rel := filepath.Join(home, name)
		fi, err := root.Lstat(rel)
		if err != nil {
			continue
		}
		switch {
		case name == "settings.json":
			if copilotJSONHas(root, rel, copilotSettingsRefused) {
				return name
			}
		case name == "mcp-config.json":
			if copilotJSONHas(root, rel, []string{"mcpServers"}) {
				return name
			}
		case fi.IsDir():
			if es := readDirIn(root, rel); len(es) > 0 {
				return name
			}
		default:
			return name
		}
	}
	return ""
}

func readDirIn(root *os.Root, rel string) []os.DirEntry {
	d, err := root.Open(rel)
	if err != nil {
		return nil
	}
	defer d.Close()
	es, _ := d.ReadDir(-1)
	return es
}

// copilotJSONHas reports whether the JSON object at rel has a non-empty value under any of keys. A file that is not
// an object counts, since the fence cannot tell what Copilot would take from it.
func copilotJSONHas(root *os.Root, rel string, keys []string) bool {
	b, err := root.ReadFile(rel)
	if err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return true
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && !slices.Contains([]string{"null", "{}", "[]", `""`, "false"}, strings.TrimSpace(string(v))) {
			return true
		}
	}
	return false
}

// prepareCopilotChat sets up the chat's own COPILOT_HOME, its instructions, its MCP config and the token file, through
// root. Nothing of the engineer's Copilot config, plugins, hooks or sessions goes in; the login stays in the Keychain.
func prepareCopilotChat(home, fleet, nonce, self string, serverEnv map[string]string) *Error {
	root, err := os.OpenRoot(home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not open %s: %v", home, err)
	}
	defer root.Close()
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
	if err := writeChatFile(root, filepath.Join(homeRel, "copilot-instructions.md"), []byte(chatPrompt+"\n")); err != nil {
		return refuse(http.StatusInternalServerError, "could not write the chat's instructions: %v", err)
	}
	cfg := map[string]MCPServer{ChatServer: {Command: self, Args: []string{"mcp"}, Env: serverEnv}}
	if err := WriteMCPConfig(root, filepath.Join(rel, "copilot-mcp.json"), cfg); err != nil {
		return refuse(http.StatusInternalServerError, "could not write the chat's MCP config: %v", err)
	}
	return nil
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
func copilotChatArgv(exe, model, effort, sessionID, home, fleet string) []string {
	argv := []string{exe, "--model", model}
	if effort != "" {
		argv = append(argv, "--reasoning-effort", effort)
	}
	argv = append(argv, "--session-id", sessionID, "--plugin-dir", copilotPluginDir(home))
	argv = append(argv, CopilotFenceArgs(ChatServer)...)
	argv = append(argv, CopilotToolArgs(ChatServer, ChatTools...)...)
	return append(argv, "--additional-mcp-config", "@"+filepath.Join(ChatDir(home, fleet), "copilot-mcp.json"))
}
