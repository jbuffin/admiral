// Package launcher starts a ship (ADR 0013): it checks the order, builds the
// harness's command line and environment, and creates the tmux session with
// its @admiral-* options already set.
package launcher

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// maxCommand keeps one tmux invocation under the size tmux's client protocol
// carries in a single message (16 KiB).
const maxCommand = 12000

// Request is a launch order from the UI.
type Request struct {
	Fleet   string `json:"fleet"`
	Harness string `json:"harness"`
	Dir     string `json:"dir"`
	Skill   Skill  `json:"skill"`
	Source  string `json:"source"`
	OrderID string `json:"order_id"`
	Text    string `json:"text"`
}

// Skill is the order's skill: Commodore, a named one, or none (Just launch).
type Skill struct {
	Kind string `json:"kind"` // "commodore", "skill" or "none"
	Name string `json:"name"`
}

// Error is a refusal; Status is the HTTP status to answer with.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func refuse(status int, format string, a ...any) *Error {
	return &Error{status, fmt.Sprintf(format, a...)}
}

// Launched is a ship just created: its tmux session and the options set on it.
type Launched struct {
	Session string
	Options map[string]string // without the @admiral- prefix
}

// Launcher creates ships on Admiral's tmux server.
type Launcher struct {
	Tmux   string // the tmux binary
	Socket string
	Home   string // $ADMIRAL_HOME
	Config func() *config.Config

	mu sync.Mutex // one launch at a time, so two don't pick one name
}

// Launch checks req and creates the ship. Nothing is created when it refuses.
func (l *Launcher) Launch(req Request) (*Launched, error) {
	if l.Tmux == "" {
		return nil, refuse(http.StatusServiceUnavailable, "tmux not found on PATH, so ships cannot be launched")
	}
	cfg := l.Config()
	fleet, ok := cfg.Fleet(req.Fleet)
	if !ok {
		return nil, refuse(http.StatusNotFound, "fleet %q not found", req.Fleet)
	}
	dock, ok := fleet.Harness(req.Harness)
	if !ok {
		return nil, refuse(http.StatusBadRequest, "fleet %s has no %s dock", fleet.Name, req.Harness)
	}
	if !fleet.CanLaunch() {
		return nil, refuse(http.StatusBadRequest, "fleet %s has no search roots, so nothing can be launched from it", fleet.Name)
	}
	skill, err := skillName(req.Skill)
	if err != nil {
		return nil, err
	}
	st, serr := os.Stat(req.Dir)
	switch {
	case req.Dir == "" || serr != nil:
		return nil, refuse(http.StatusBadRequest, "directory %s not found", req.Dir)
	case !st.IsDir():
		return nil, refuse(http.StatusBadRequest, "%s is not a directory", req.Dir)
	}
	exe, found := dock.Resolve()
	if !found {
		return nil, refuse(http.StatusBadRequest, "%s executable %s not found for fleet %s", req.Harness, dock.Executable, fleet.Name)
	}

	argv := append([]string{exe}, dock.Args...)
	prompt := promptFor(req.Harness, skill, req.Skill.Kind, req.Text)
	switch req.Harness {
	case "claude":
		argv = append(argv, "--settings", claudeSettingsPath(l.Home))
		argv = append(argv, "--")
		if prompt != "" {
			argv = append(argv, prompt)
		}
	case "codex":
		argv = append(argv, codexFlags()...)
		argv = append(argv, "--")
		if prompt != "" {
			argv = append(argv, prompt)
		}
	case "copilot":
		argv = append(argv, "--plugin-dir", copilotPluginDir(l.Home))
		if prompt != "" {
			argv = append(argv, "--interactive="+prompt)
		}
	default:
		return nil, refuse(http.StatusBadRequest, "unknown harness %q", req.Harness)
	}
	env := l.shipEnv(dock)

	l.mu.Lock()
	defer l.mu.Unlock()
	name := l.pickName()
	session := Slug(name)
	opts := [][2]string{{"name", name}, {"fleet", fleet.Key}, {"fleet-name", fleet.Name}, {"fleet-color", fleet.Color},
		{"harness", req.Harness}, {"dir", req.Dir}, {"skill", skill}, {"source", req.Source},
		{"order", req.OrderID}, {"launched", time.Now().UTC().Format(time.RFC3339)}}

	if err := l.create(session, req.Dir, opts, env, argv); err != nil {
		return nil, err
	}
	out := &Launched{Session: session, Options: map[string]string{}}
	for _, o := range opts {
		out.Options[o[0]] = o[1]
	}
	return out, nil
}

// skillName is the skill's name without its / or $ prefix; "" for Just launch.
func skillName(s Skill) (string, error) {
	switch s.Kind {
	case "commodore":
		return "commodore", nil
	case "none":
		return "", nil
	case "skill":
		name := strings.TrimPrefix(strings.TrimPrefix(s.Name, "/"), "$")
		if name == "" {
			return "", refuse(http.StatusBadRequest, "skill name is empty")
		}
		if strings.ContainsAny(name, " \t\r\n") || strings.TrimSpace(name) != name {
			return "", refuse(http.StatusBadRequest, "skill name %q contains whitespace", s.Name)
		}
		return name, nil
	}
	return "", refuse(http.StatusBadRequest, "skill kind %q is not commodore, skill or none", s.Kind)
}

// promptFor builds the prompt argument: /skill text (Claude Code, Copilot) or
// $skill text (Codex), the text trimmed; Just launch sends the text as written.
func promptFor(harness, skill, kind, text string) string {
	if kind == "none" {
		return text
	}
	mark := "/"
	if harness == "codex" {
		mark = "$"
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return mark + skill
	}
	return mark + skill + " " + text
}

// shipEnv is the fleet's environment over Admiral's, minus the tmux variables
// the pane gets from its own tmux, with the tmux directory last on PATH.
func (l *Launcher) shipEnv(dock config.Harness) map[string]string {
	env := map[string]string{}
	for _, kv := range dock.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "TMUX" && k != "TMUX_PANE" {
			env[k] = v
		}
	}
	dir := filepath.Dir(l.Tmux)
	if p := env["PATH"]; p != "" {
		env["PATH"] = p + string(os.PathListSeparator) + dir
	} else {
		env["PATH"] = dir
	}
	return env
}

// pickName takes a name no session or ship uses, asking tmux directly.
func (l *Launcher) pickName() string {
	out, _ := l.run("list-sessions", "-F", "#{session_name}\x1f#{@admiral-name}")
	sessions, shipNames := map[string]bool{}, map[string]bool{}
	for _, row := range strings.Split(out, "\n") {
		if s, n, ok := strings.Cut(row, "\x1f"); ok {
			sessions[s], shipNames[n] = true, true
		}
	}
	return pickName(func(n string) bool { return shipNames[n] || sessions[Slug(n)] })
}

// create makes the session on a placeholder, sets its options, then replaces
// the placeholder with the harness: new-session starts its command at once, and
// the harness's first hook must find the options already there.
func (l *Launcher) create(session, dir string, opts [][2]string, env map[string]string, argv []string) error {
	target := "=" + session + ":" // "=" matches the name exactly; tmux wants the colon
	pane := target
	cmds := [][]string{
		{"new-session", "-d", "-s", session, "-c", dir, "--", "tail", "-f", "/dev/null"},
		{"set-option", "-t", target, "history-limit", "10000"},
		{"set-option", "-p", "-t", pane, "remain-on-exit", "on"},
		{"set-option", "-w", "-t", pane, "monitor-bell", "on"},
	}
	for _, o := range opts {
		cmds = append(cmds, []string{"set-option", "-t", target, "@admiral-" + o[0], o[1]})
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cmds = append(cmds, []string{"set-environment", "-t", target, k, env[k]})
	}
	cmds = append(cmds, append([]string{"respawn-pane", "-k", "-t", pane, "-c", dir, "--"}, argv...))

	// A pane's PATH is the one of the tmux client that spawns it, whatever the
	// session environment says, so that client runs with the ship's PATH.
	for _, batch := range batches(cmds) {
		if out, err := l.runWith(env["PATH"], batch...); err != nil {
			_, _ = l.run("kill-session", "-t", target)
			return refuse(http.StatusInternalServerError, "tmux could not start the ship: %s", strings.TrimSpace(out))
		}
	}
	return nil
}

// batches joins commands with ";" into tmux invocations that fit maxCommand.
func batches(cmds [][]string) [][]string {
	var out [][]string
	var cur []string
	size := 0
	for _, c := range cmds {
		n := 0
		for _, a := range c {
			n += len(a) + 1
		}
		if len(cur) > 0 && size+n > maxCommand {
			out = append(out, cur)
			cur, size = nil, 0
		}
		if len(cur) > 0 {
			cur = append(cur, ";")
		}
		for _, a := range c {
			// An argument ending in ; would be read as a command separator.
			if strings.HasSuffix(a, ";") {
				a = strings.TrimSuffix(a, ";") + `\;`
			}
			cur = append(cur, a)
		}
		size += n
	}
	return append(out, cur)
}

// run executes the tmux binary on Admiral's socket with an argument list.
func (l *Launcher) run(args ...string) (string, error) { return l.runWith("", args...) }

// runWith is run with PATH replaced, when path is not empty.
func (l *Launcher) runWith(path string, args ...string) (string, error) {
	cmd := exec.Command(l.Tmux, append([]string{"-S", l.Socket}, args...)...)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") || (path != "" && strings.HasPrefix(kv, "PATH=")) {
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	if path != "" {
		cmd.Env = append(cmd.Env, "PATH="+path)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
