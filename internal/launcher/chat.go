package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/codexpane"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The chat has no built-in tool, so no skill can load: this prompt carries all its guidance.
const chatPromptTemplate = "You are AdmiralBot, the engineer's assistant in Admiral. You act only through the admiral MCP tools, " +
	"and only on what the engineer asks in this chat. You have no shell and no files. " +
	"Reply in a line or two; the engineer is often on a phone.\n\n" +
	"Read with list_ships, read_conversation, read_outcomes, list_inbox, list_sources and list_campaigns. " +
	"Use add_order to queue an order on a source's panel (take the source and panel from list_sources; with several writable panels and none named, ask which). " +
	"Use launch_ship to start a ship. Admiral refuses a second freeform ship for the same message and names the first; " +
	"When one message asks for two different freeform ships, pass anyway on the second, and only then. " +
	"Use next_prompt, wake_ship, stop_ship and dismiss_ship to move a ship along, and answer_item to answer an Inbox question with the engineer's choice. " +
	"Use ask_question when something is a real choice, instead of guessing; it waits, and if it hands back an id, call it again with wait_id until the engineer answers. " +
	"%s\n\n" +
	"A Commodore launch runs in autopilot unless the engineer says \"with me\" (then pass with_me); say in your reply which mode the ship runs in. " +
	"The text you give a ship is all it knows: say what to do and what done looks like, keep the engineer's branch names and constraints, " +
	"and put the full URL of the repository, issue or pull request in it. Never invent details the engineer did not give. " +
	"When a tool refuses and names a ship or a fleet, tell the engineer what it said as it said it; do not try another route round it."

// What the chat is told about remembering: only a Claude Code chat reads its notes again after a reset.
const (
	rememberClaude = "When the engineer asks you to remember something, call save_note with it and quote the saved note back in your reply; a refusal says why, so tell the engineer."
	rememberOther  = "You cannot carry a fact into a later chat yet: when the engineer asks you to remember something, say so, and do not call save_note."
)

// chatPromptFor is the prompt of a chat on the harness.
func chatPromptFor(harness string) string {
	if harness == "claude" {
		return fmt.Sprintf(chatPromptTemplate, rememberClaude)
	}
	return fmt.Sprintf(chatPromptTemplate, rememberOther)
}

// writeChatFile writes a file of the chat's directory through root as a temp file renamed into
// place, so a link planted at the path is replaced and never followed. A directory on the way
// that is a link is refused.
func writeChatFile(root *os.Root, rel string, data []byte) error {
	if err := mkdirsIn(root, filepath.Dir(rel)); err != nil {
		return err
	}
	tmp := rel + ".tmp"
	_ = root.Remove(tmp)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, rel)
	}
	if err != nil {
		_ = root.Remove(tmp)
	}
	return err
}

func mkdirsIn(root *os.Root, dirPath string) error {
	dir := ""
	for _, part := range strings.Split(filepath.ToSlash(dirPath), "/") {
		if part == "" || part == "." {
			continue
		}
		dir = filepath.Join(dir, part)
		fi, err := root.Lstat(dir)
		switch {
		case os.IsNotExist(err):
			if err := root.Mkdir(dir, 0o700); err != nil {
				return err
			}
		case err != nil:
			return err
		case !fi.IsDir():
			return fmt.Errorf("%s is not a directory", dir)
		}
	}
	return nil
}

// MCPServer is one entry of a Claude Code --mcp-config file.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// WriteMCPConfig writes the config file at rel inside root, 0600: its env may hold the actor token.
func WriteMCPConfig(root *os.Root, rel string, servers map[string]MCPServer) error {
	raw, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err != nil {
		return err
	}
	return writeChatFile(root, rel, append(raw, '\n'))
}

// removeChatStale drops what the chat's directory must not hold: the skill an older Admiral wrote (it cannot
// load under an empty tool list) and any project settings or memory file, which `--setting-sources project` would
// read from this directory. RemoveAll unlinks a link, never follows it.
func removeChatStale(root *os.Root, fleet string) error {
	dir := filepath.Join("chat", fleet)
	for _, rel := range []string{
		filepath.Join(".claude", "skills", "admiralbot"),
		filepath.Join(".claude", "settings.json"),
		filepath.Join(".claude", "settings.local.json"),
		filepath.Join(".claude", "CLAUDE.md"),
		"CLAUDE.md",
		"CLAUDE.local.md",
	} {
		if err := root.RemoveAll(filepath.Join(dir, rel)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ChatDir is a fleet's chat working directory; the Read fence keeps the chat out of the others.
func ChatDir(home, fleet string) string { return filepath.Join(home, "chat", fleet) }

// ChatDock is the fleet's chat settings and the dock it runs on. A fleet that is not there is a 404,
// one whose chat is off a 409 with the reason.
func ChatDock(cfg *config.Config, key string) (config.Fleet, config.Harness, *Error) {
	fleet, off, ok := cfg.ChatFleet(key)
	switch {
	case !ok:
		return config.Fleet{}, config.Harness{}, refuse(http.StatusNotFound, "no fleet %s", key)
	case off != "":
		return config.Fleet{}, config.Harness{}, refuse(http.StatusConflict, "the chat is off for fleet %s: %s", fleet.Name, off)
	}
	dock, _ := fleet.Harness(fleet.Chat.Harness)
	return fleet, dock, nil
}

// chatOwned are the variables Admiral hands a ship and the chat must not have,
// besides every other ADMIRAL_* name but ADMIRAL_HOME.
var chatOwned = []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX", "ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES", "ADMIRAL_AUTO_MERGE"}

// chatDropped are Claude Code's own switches that would move or switch off the chat's compaction backstop; a
// fleet's env or Admiral's can set them for ships, and the chat never gets them.
var chatDropped = []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "DISABLE_AUTO_COMPACT", "DISABLE_COMPACT"}

var chatVar = regexp.MustCompile(`^ADMIRAL_[A-Za-z0-9_]+$`)

// The caller has checked that the fleet has no chat session. Unlike a ship's, the session has no
// remain-on-exit, so it ends with Claude, and none of a ship's variables.
func (l *Launcher) StartChat(key string) error {
	if l.Tmux == "" {
		return refuse(http.StatusServiceUnavailable, "tmux not found on PATH, so the chat cannot run")
	}
	cfg := l.Config()
	fleet, dock, rerr := ChatDock(cfg, key)
	if rerr != nil {
		return rerr
	}
	if dock.Name != "claude" && dock.Name != "codex" && dock.Name != "copilot" {
		return refuse(http.StatusNotImplemented, "the chat cannot launch on %s yet", config.HarnessName(dock.Name))
	}
	exe, found := dock.Resolve()
	if !found {
		return refuse(http.StatusBadRequest, "%s executable %s not found for fleet %s", dock.Name, dock.Executable, fleet.Name)
	}
	codexAuth := ""
	if dock.Name == "codex" {
		var aerr *Error
		if codexAuth, aerr = codexLogin(dock); aerr != nil {
			return aerr
		}
	}
	home, err := filepath.Abs(l.Home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not resolve %s: %v", l.Home, err)
	}
	dir := ChatDir(home, fleet.Key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return refuse(http.StatusInternalServerError, "could not create %s: %v", dir, err)
	}
	// Each value is its own argument: nothing here goes through a shell.
	chat := fleet.Chat
	// The dock's args commonly widen permissions (--allowedTools, --add-dir, --mcp-config), so
	// the chat keeps only the dock's executable and env.
	var argv []string
	if dock.Name == "claude" {
		argv = []string{exe, "--model", chat.Model}
		if chat.Effort != "" {
			argv = append(argv, "--effort", chat.Effort)
		}
		argv = append(argv, "--append-system-prompt", chatPromptFor("claude"))
		argv = append(argv, FenceArgs(claudeChatSettingsPath(l.Home), filepath.Join(dir, "mcp.json"))...)
	}

	// Admiral's environment and the tmux server's may hold a ship's variables (a server that
	// outlived an earlier Admiral, or started from a ship's shell): the chat gets only ADMIRAL_HOME,
	// its port and its fleet.
	env := l.shipEnv(dock)
	unset := map[string]bool{}
	for _, k := range chatOwned {
		unset[k] = true
	}
	for _, k := range chatDropped {
		unset[k] = true
		delete(env, k)
	}
	for k := range env {
		if strings.HasPrefix(k, "ADMIRAL_") {
			unset[k] = true
			delete(env, k)
		}
	}
	env["ADMIRAL_HOME"] = home
	delete(unset, "ADMIRAL_HOME")
	// The hooks and the status line know the chat's fleet from here; the model can't pick another.
	env["ADMIRAL_CHAT_FLEET"] = fleet.Key
	// The hooks print the brief and digest only on Claude Code, whose context format they speak.
	env["ADMIRAL_CHAT_HARNESS"] = dock.Name
	if l.Port != "" {
		env["ADMIRAL_PORT"] = l.Port
	}
	for k := range env {
		delete(unset, k)
	}

	session := sessionhost.ChatSession(fleet.Key)
	nonce, nerr := actor.NewNonce()
	if nerr != nil {
		return refuse(http.StatusInternalServerError, "could not make the chat's identity: %v", nerr)
	}
	// The token goes only into the MCP config, which Claude Code starts the server from: the session's
	// env passes through tmux's argv and stays readable on its socket. A ship can read $ADMIRAL_HOME
	// anyway; the fence is against the chat model, which has no file or shell tool.
	if dock.Name == "codex" {
		self, serr := os.Executable()
		if serr != nil {
			return refuse(http.StatusInternalServerError, "could not find the admiral binary: %v", serr)
		}
		if err := prepareCodexChat(home, fleet.Key, nonce, codexAuth); err != nil {
			return err
		}
		env["CODEX_HOME"] = ChatCodexHome(home, fleet.Key)
		argv = codexChatArgv(exe, codexpane.HasNoDaemon(exe), chat, self, home, fleet.Key, env["ADMIRAL_PORT"])
	} else if dock.Name == "copilot" {
		self, serr := os.Executable()
		if serr != nil {
			return refuse(http.StatusInternalServerError, "could not find the admiral binary: %v", serr)
		}
		sid, serr := copilotSessionID()
		if serr != nil {
			return refuse(http.StatusInternalServerError, "could not make the chat's session id: %v", serr)
		}
		serverEnv := map[string]string{"ADMIRAL_ACTOR_TOKEN_FILE": CodexTokenFile(home, fleet.Key), "ADMIRAL_HOME": home, "ADMIRAL_CHAT_FLEET": fleet.Key}
		if p := env["ADMIRAL_PORT"]; p != "" {
			serverEnv["ADMIRAL_PORT"] = p
		}
		if err := prepareCopilotChat(home, fleet.Key, nonce); err != nil {
			return err
		}
		// Another login, provider or config in the dock's env or Admiral's own would ride into a home of its own.
		for k := range env {
			if !CopilotEnvKept(k) {
				delete(env, k)
				unset[k] = true
			}
		}
		env["COPILOT_HOME"] = ChatCopilotHome(home, fleet.Key)
		env["COPILOT_PLUGIN_DIR_ONLY"] = "true"
		delete(unset, "COPILOT_HOME")
		delete(unset, "COPILOT_PLUGIN_DIR_ONLY")
		argv = copilotChatArgv(exe, chat.Model, chat.Effort, sid, home, copilotMCPConfig(self, serverEnv))
	} else if err := l.writeChatMCP(home, fleet.Key, nonce, env); err != nil {
		return err
	}
	opts := [][2]string{{sessionhost.ChatModelOpt, chat.Model}, {sessionhost.ChatEffortOpt, chat.Effort}, {sessionhost.ChatFleetOpt, fleet.Key},
		{sessionhost.ChatHarnessOpt, dock.Name}, {sessionhost.ChatNonceOpt, nonce}}
	for _, o := range opts {
		if hasControl(o[1]) {
			return refuse(http.StatusBadRequest, "%s contains a control character", o[0])
		}
	}
	if n := sessionhost.CommandSize(argv); n > sessionhost.MaxMessage {
		return refuse(http.StatusBadRequest, "the chat's command line is %d bytes and tmux carries at most %d", n, sessionhost.MaxMessage)
	}
	// The tmux server's global ADMIRAL_* names (and, for Copilot, the names its env filter drops): -r hides them from this session
	// (-u would fall back to the server's). A server that isn't up yet has none.
	if globals, err := l.server().GlobalEnvNames(); err == nil {
		for _, name := range globals {
			_, own := env[name]
			switch {
			case chatVar.MatchString(name) && name != "ADMIRAL_HOME" && name != "ADMIRAL_CHAT_FLEET":
				unset[name] = true
			// A login, provider or collector set globally in a tmux server that outlived Admiral would ride into the chat's pane.
			case dock.Name == "copilot" && !own && !CopilotEnvKept(name):
				unset[name] = true
			}
		}
	}
	names := make([]string, 0, len(unset))
	for k := range unset {
		names = append(names, k)
	}
	// A fresh session starts with none of the old one's chat questions, once nothing
	// can refuse the start any more.
	if l.BeforeChat != nil {
		l.BeforeChat(fleet.Key)
	}
	if err := l.createSession(session, dir, opts, nil, env, names, argv, false); err != nil {
		return err
	}
	return nil
}

func (l *Launcher) writeChatMCP(home, fleet, nonce string, env map[string]string) *Error {
	self, err := os.Executable()
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not find the admiral binary: %v", err)
	}
	minter, err := actor.NewMinter(home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not make the chat's identity: %v", err)
	}
	serverEnv := map[string]string{"ADMIRAL_ACTOR_TOKEN": minter.Token(fleet, nonce), "ADMIRAL_HOME": home, "ADMIRAL_CHAT_FLEET": fleet}
	if p := env["ADMIRAL_PORT"]; p != "" {
		serverEnv["ADMIRAL_PORT"] = p
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not open %s: %v", home, err)
	}
	defer root.Close()
	if err := removeChatStale(root, fleet); err != nil {
		return refuse(http.StatusInternalServerError, "could not clear the chat's directory: %v", err)
	}
	cfg := map[string]MCPServer{ChatServer: {Command: self, Args: []string{"mcp"}, Env: serverEnv}}
	if err := WriteMCPConfig(root, filepath.Join("chat", fleet, "mcp.json"), cfg); err != nil {
		return refuse(http.StatusInternalServerError, "could not write the chat's MCP config: %v", err)
	}
	return nil
}
