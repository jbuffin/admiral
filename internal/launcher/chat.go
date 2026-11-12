package launcher

import (
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

//go:embed admiralbot/SKILL.md
var skillText []byte

const chatPrompt = "You are AdmiralBot, the engineer's assistant in Admiral. Use the admiralbot skill for everything you do. " +
	"You only add orders: you never launch ships and never change files."

// WriteChatSkill writes a fleet's skill into chat/<fleet>/, which is the chat's working
// directory and so where its project settings are read from. It writes a temp file and renames
// it, so a link planted at the path is replaced and never followed. A directory on the way
// that is a link is refused.
func WriteChatSkill(home, fleet string) error {
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer root.Close()
	dir := ""
	for _, part := range []string{"chat", fleet, ".claude", "skills", "admiralbot"} {
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
	tmp := filepath.Join(dir, ".SKILL.md.tmp")
	_ = root.Remove(tmp)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(skillText)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, filepath.Join(dir, "SKILL.md"))
	}
	if err != nil {
		_ = root.Remove(tmp)
	}
	return err
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
	exe, found := dock.Resolve()
	if !found {
		return refuse(http.StatusBadRequest, "claude executable %s not found for fleet %s", dock.Executable, fleet.Name)
	}
	home, err := filepath.Abs(l.Home)
	if err != nil {
		return refuse(http.StatusInternalServerError, "could not resolve %s: %v", l.Home, err)
	}
	dir := ChatDir(home, fleet.Key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return refuse(http.StatusInternalServerError, "could not create %s: %v", dir, err)
	}
	if err := WriteChatSkill(home, fleet.Key); err != nil {
		return refuse(http.StatusInternalServerError, "could not write the AdmiralBot skill: %v", err)
	}

	// Each value is its own argument: nothing here goes through a shell.
	chat := fleet.Chat
	// The dock's args commonly widen permissions (--allowedTools, --add-dir, --mcp-config), so
	// the chat keeps only the dock's executable and env.
	argv := []string{exe}
	argv = append(argv, "--model", chat.Model)
	if chat.Effort != "" {
		argv = append(argv, "--effort", chat.Effort)
	}
	argv = append(argv, "--permission-mode", "dontAsk", "--setting-sources", "project", "--tools", "Bash,Skill,Read",
		"--append-system-prompt", chatPrompt, "--settings", claudeChatSettingsPath(l.Home))

	// Admiral's environment and the tmux server's may hold a ship's variables (a server that
	// outlived an earlier Admiral, or started from a ship's shell): the chat gets only ADMIRAL_HOME,
	// its port and its fleet.
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
	// `admiral chat ask` finds its fleet here; the model can't pick another.
	env["ADMIRAL_CHAT_FLEET"] = fleet.Key
	if l.Port != "" {
		env["ADMIRAL_PORT"] = l.Port
	}
	for k := range env {
		delete(unset, k)
	}

	session := sessionhost.ChatSession(fleet.Key)
	opts := [][2]string{{sessionhost.ChatModelOpt, chat.Model}, {sessionhost.ChatEffortOpt, chat.Effort}, {sessionhost.ChatFleetOpt, fleet.Key}}
	for _, o := range opts {
		if hasControl(o[1]) {
			return refuse(http.StatusBadRequest, "%s contains a control character", o[0])
		}
	}
	if n := sessionhost.CommandSize(argv); n > sessionhost.MaxMessage {
		return refuse(http.StatusBadRequest, "the chat's command line is %d bytes and tmux carries at most %d", n, sessionhost.MaxMessage)
	}
	// The tmux server's global ADMIRAL_* names: -r hides them from this session
	// (-u would fall back to the server's). A server that isn't up yet has none.
	if globals, err := l.server().GlobalEnvNames(); err == nil {
		for _, name := range globals {
			if chatVar.MatchString(name) && name != "ADMIRAL_HOME" && name != "ADMIRAL_CHAT_FLEET" {
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
