package launcher

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
)

// ChatSession is the chat's tmux session name; sessionhost.ChatName is this.
const ChatSession = "_chat"

// The names (after @admiral-) of the session options that record how the chat
// started; sessionhost spells them with the prefix.
const (
	ChatModelOpt  = "chat-model"
	ChatEffortOpt = "chat-effort"
	ChatFleetOpt  = "chat-fleet"
)

// ChatDir is the chat session's working directory, $ADMIRAL_HOME/chat: never a
// fleet's checkout.
func ChatDir(home string) string { return filepath.Join(home, "chat") }

// ChatDock is the Claude dock the chat runs, from the fleet config.Chat names. A
// refusal says why there is none.
func ChatDock(cfg *config.Config) (config.Fleet, config.Harness, *Error) {
	fleet, ok := cfg.Fleet(cfg.Chat.Fleet)
	if !ok {
		return config.Fleet{}, config.Harness{}, refuse(http.StatusConflict, "the chat has no fleet to run in: set one in Settings")
	}
	dock, ok := fleet.Harness("claude")
	if !ok {
		return config.Fleet{}, config.Harness{}, refuse(http.StatusConflict, "fleet %s has no Claude Code dock, so the chat cannot run there: pick another fleet in Settings", fleet.Name)
	}
	return fleet, dock, nil
}

// chatOwned are the variables Admiral hands a ship and the chat must not have,
// besides every other ADMIRAL_* name but ADMIRAL_HOME.
var chatOwned = []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX", "ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES", "ADMIRAL_AUTO_MERGE"}

var chatVar = regexp.MustCompile(`^ADMIRAL_[A-Za-z0-9_]+$`)

// StartChat creates the `_chat` session (ADR 0018, 0019): the chat fleet's
// Claude dock, then the chat's model and effort, then the chat hook bundle, in
// $ADMIRAL_HOME/chat. The caller has checked that no `_chat` session exists. The
// session is made as a ship's is (a placeholder, its options, its environment,
// then the harness in its place), but with no remain-on-exit, so it ends with
// Claude, and with none of a ship's variables.
func (l *Launcher) StartChat() error {
	if l.Tmux == "" {
		return refuse(http.StatusServiceUnavailable, "tmux not found on PATH, so the chat cannot run")
	}
	cfg := l.Config()
	fleet, dock, rerr := ChatDock(cfg)
	if rerr != nil {
		return rerr
	}
	exe, found := dock.Resolve()
	if !found {
		return refuse(http.StatusBadRequest, "claude executable %s not found for fleet %s", dock.Executable, fleet.Name)
	}
	home, err := filepath.Abs(l.Home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not resolve %s: %v", l.Home, err)
	}
	dir := ChatDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return refuse(http.StatusInternalServerError, "could not create %s: %v", dir, err)
	}

	// Each value is its own argument: nothing here goes through a shell.
	chat := cfg.Chat
	argv := append([]string{exe}, dock.Args...)
	argv = append(argv, "--model", chat.Model)
	if chat.Effort != "" {
		argv = append(argv, "--effort", chat.Effort)
	}
	argv = append(argv, "--settings", claudeChatSettingsPath(l.Home))

	// Admiral's own environment may hold a ship's variables, and the tmux server
	// may too (one that outlived an earlier Admiral, or started from a ship's
	// shell): the chat gets none of them, only ADMIRAL_HOME.
	env := l.shipEnv(dock)
	unset := map[string]bool{}
	for _, k := range chatOwned {
		unset[k] = true
	}
	for k := range env {
		if strings.HasPrefix(k, "ADMIRAL_") {
			unset[k] = true
			delete(env, k)
		}
	}
	env["ADMIRAL_HOME"] = home
	delete(unset, "ADMIRAL_HOME")
	for k := range env {
		delete(unset, k)
	}

	const session = ChatSession
	opts := [][2]string{{ChatModelOpt, chat.Model}, {ChatEffortOpt, chat.Effort}, {ChatFleetOpt, fleet.Key}}
	for _, o := range opts {
		if hasControl(o[1]) {
			return refuse(http.StatusBadRequest, "%s contains a control character", o[0])
		}
	}
	if n := commandSize(argv); n > maxMessage {
		return refuse(http.StatusBadRequest, "the chat's command line is %d bytes and tmux carries at most %d", n, maxMessage)
	}
	// The tmux server's global ADMIRAL_* names: -r hides them from this session
	// (-u would fall back to the server's). A server that isn't up yet has none.
	if out, err := l.run("show-environment", "-g"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			name := strings.TrimPrefix(strings.SplitN(line, "=", 2)[0], "-")
			if chatVar.MatchString(name) && name != "ADMIRAL_HOME" {
				unset[name] = true
			}
		}
	}
	names := make([]string, 0, len(unset))
	for k := range unset {
		names = append(names, k)
	}
	if err := l.createSession(session, dir, opts, env, names, argv, false); err != nil {
		return err
	}
	return nil
}
