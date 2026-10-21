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
	"unicode"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/repofinder"
	"github.com/jbuffin/admiral/internal/skillindex"
)

// maxCommand keeps one tmux invocation under the size tmux's client protocol
// carries in a single message (16 KiB).
const maxCommand = 12000

// maxMessage is what one tmux client message holds; the harness's command line
// goes in a single one (respawn-pane), so a longer one cannot be launched.
const maxMessage = 15000

// Request is what the UI sends to launch a ship.
type Request struct {
	Fleet   string `json:"fleet"`
	Harness string `json:"harness"`
	Dir     string `json:"dir"`
	Skill   Skill  `json:"skill"`
	Source  string `json:"source"`
	OrderID string `json:"order_id"`
	Text    string `json:"text"`
	Note    string `json:"note"` // Just launch only: optional text sent before the order
	// Title and URL are the order's title and web URL, set by the server from
	// the source's order, never by the client.
	Title string `json:"-"`
	URL   string `json:"-"`
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
	if req.dockLaunch() {
		if req.Skill.Kind != "none" {
			return nil, refuse(http.StatusBadRequest, "a launch with no order cannot name a skill")
		}
		req.Text, req.Note = strings.TrimSpace(req.Text), ""
	}
	// The options are tmux's record of the ship, read back over the control
	// connection one line each; a control character would break the framing.
	// The name and the time are ours.
	opts := [][2]string{{"name", ""}, {"fleet", fleet.Key}, {"fleet-name", fleet.Name}, {"fleet-color", fleet.Color},
		{"harness", req.Harness}, {"dir", req.Dir}, {"skill", skill}, {"source", req.Source},
		{"order", req.OrderID}, {"launched", time.Now().UTC().Format(time.RFC3339)}, {"title", orderTitle(req)}, {"url", req.URL}}
	for _, o := range opts {
		if hasControl(o[1]) {
			return nil, refuse(http.StatusBadRequest, "%s contains a control character", o[0])
		}
	}
	st, serr := os.Stat(req.Dir)
	switch {
	case req.Dir == "" || serr != nil:
		return nil, refuse(http.StatusBadRequest, "directory %s not found", req.Dir)
	case !st.IsDir():
		return nil, refuse(http.StatusBadRequest, "%s is not a directory", req.Dir)
	}
	// The list is read again here: a checkout can vanish, or be named in a
	// request that never came from the list (story 84). The cleaned path is
	// the one used from here on.
	dir, listed := repofinder.Contains(repofinder.List(fleet.SearchRoots), req.Dir)
	if !listed {
		return nil, refuse(http.StatusBadRequest, "directory %s is not a checkout or search root of fleet %s", req.Dir, fleet.Name)
	}
	req.Dir = dir
	if req.Skill.Kind == "commodore" {
		name, ok := skillindex.FindCommodore(req.Harness, dock.Environ(), req.Dir)
		ok = ok && skillindex.SafeInvocation(name)
		if !ok {
			return nil, refuse(http.StatusBadRequest, "Commodore not found for %s in fleet %s", req.Harness, fleet.Name)
		}
		skill = name
	}
	// opts was built before the directory was cleaned and Commodore resolved.
	for i := range opts {
		switch opts[i][0] {
		case "dir":
			opts[i][1] = req.Dir
		case "skill":
			opts[i][1] = skill
		}
	}
	exe, found := dock.Resolve()
	if !found {
		return nil, refuse(http.StatusBadRequest, "%s executable %s not found for fleet %s", req.Harness, dock.Executable, fleet.Name)
	}

	argv := append([]string{exe}, dock.Args...)
	prompt := promptFor(req.Harness, skill, req.Skill.Kind, req.Note, req.Text)
	switch req.Harness {
	case "claude":
		argv = append(argv, "--settings", claudeSettingsPath(l.Home))
		argv = appendPrompt(argv, prompt, req.dockLaunch())
	case "codex":
		argv = append(argv, codexFlags()...)
		argv = appendPrompt(argv, prompt, req.dockLaunch())
	case "copilot":
		argv = append(argv, "--plugin-dir", copilotPluginDir(l.Home))
		if prompt != "" {
			argv = append(argv, "--interactive="+prompt)
		}
	default:
		return nil, refuse(http.StatusBadRequest, "unknown harness %q", req.Harness)
	}
	env := l.shipEnv(dock)
	if n := commandSize(argv); n > maxMessage {
		return nil, refuse(http.StatusBadRequest, "the order is too long to launch: its command line is %d bytes and tmux carries at most %d", n, maxMessage)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	name, err := l.pickName()
	if err != nil {
		return nil, err
	}
	session := Slug(name)
	opts[0][1] = name

	if err := l.create(session, req.Dir, opts, env, argv); err != nil {
		return nil, err
	}
	out := &Launched{Session: session, Options: map[string]string{}}
	for _, o := range opts {
		out.Options[o[0]] = o[1]
	}
	return out, nil
}

// dockLaunch is a launch from a dock with no order: the text is the prompt.
func (r Request) dockLaunch() bool { return r.Source == "" && r.OrderID == "" }

// appendPrompt ends argv with the prompt after "--". A dock launch with no
// prompt starts the harness bare, with no "--" either.
func appendPrompt(argv []string, prompt string, dock bool) []string {
	if dock && prompt == "" {
		return argv
	}
	argv = append(argv, "--")
	if prompt != "" {
		argv = append(argv, prompt)
	}
	return argv
}

// orderTitle is the first line of an order's title, or of a dock launch's
// text, with control characters dropped; the title never refuses a launch. A
// launch with an order id but no source has no title.
func orderTitle(req Request) string {
	if req.Source == "" && req.OrderID != "" {
		return ""
	}
	line := req.Text
	if req.Title != "" {
		line = req.Title
	}
	first, _, _ := strings.Cut(strings.TrimLeft(line, " \t\r\n"), "\n")
	first = strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, first)
	if r := []rune(first); len(r) > 200 {
		first = string(r[:200])
	}
	return strings.TrimSpace(first)
}

// hasControl reports a character below 0x20 or 0x7f.
func hasControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

// isControl is a character a title must not carry: C0 and C1 controls, the bidi
// controls and the line and paragraph separators.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x2028 || r == 0x2029
}

func commandSize(argv []string) int {
	n := 0
	for _, a := range argv {
		n += len(a) + 1
	}
	return n
}

// skillName is the skill's name without its / or $ prefix; "" for Just launch
// and for Commodore, whose name Launch looks up in the skill index.
func skillName(s Skill) (string, error) {
	switch s.Kind {
	case "commodore", "none":
		return "", nil
	case "skill":
		name := s.Name
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "$") {
			name = name[1:] // one prefix, not both
		}
		if name == "" {
			return "", refuse(http.StatusBadRequest, "skill name is empty")
		}
		if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
			return "", refuse(http.StatusBadRequest, "skill name %q contains whitespace", s.Name)
		}
		return name, nil
	}
	return "", refuse(http.StatusBadRequest, "skill kind %q is not commodore, skill or none", s.Kind)
}

// promptFor builds the prompt argument: /skill text (Claude Code, Copilot) or
// $skill text (Codex), the text trimmed; Just launch sends the text as written,
// after the note and a blank line when there is one.
func promptFor(harness, skill, kind, note, text string) string {
	if kind == "none" {
		switch note = strings.TrimSpace(note); {
		case note == "":
			return text
		case text == "":
			return note
		}
		return note + "\n\n" + text
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
// the pane gets from its own tmux, with $ADMIRAL_HOME/bin first on PATH (hooks
// run `admiral`) and the tmux directory last.
func (l *Launcher) shipEnv(dock config.Harness) map[string]string {
	env := map[string]string{}
	for _, kv := range dock.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "TMUX" && k != "TMUX_PANE" {
			env[k] = v
		}
	}
	path := filepath.Join(l.Home, "bin")
	if p := env["PATH"]; p != "" {
		path += string(os.PathListSeparator) + p
	}
	env["PATH"] = path + string(os.PathListSeparator) + filepath.Dir(l.Tmux)
	return env
}

// pickName takes a name no session or ship uses, asking tmux directly. No
// tmux server yet means nothing is in use; any other error stops the launch,
// since a name in use would end with a clash.
func (l *Launcher) pickName() (string, error) {
	out, err := l.run("list-sessions", "-F", "#{session_name}\x1f#{@admiral-name}")
	if err != nil && !strings.Contains(out, "no server running") && !strings.Contains(out, "error connecting") {
		return "", refuse(http.StatusInternalServerError, "tmux could not list its sessions: %s", strings.TrimSpace(out))
	}
	sessions, shipNames := map[string]bool{}, map[string]bool{}
	for _, row := range strings.Split(out, "\n") {
		if s, n, ok := strings.Cut(row, "\x1f"); ok {
			sessions[s], shipNames[n] = true, true
		}
	}
	return pickName(func(n string) bool { return shipNames[n] || sessions[Slug(n)] }), nil
}

// create makes the session on a placeholder, sets its options, then replaces
// the placeholder with the harness: new-session starts its command at once, and
// the harness's first hook must find the options already there.
func (l *Launcher) create(session, dir string, opts [][2]string, env map[string]string, argv []string) error {
	target := "=" + session + ":" // "=" matches the name exactly; tmux wants the colon
	pane := target
	cdir := strings.ReplaceAll(dir, "#", "##") // tmux format-expands -c; ## is a literal #
	cmds := [][]string{
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
		c := []string{"set-environment", "-t", target, k, env[k]}
		if n := commandSize(c); n > maxMessage {
			return refuse(http.StatusBadRequest, "environment variable %s is too long to launch: %d bytes and tmux carries at most %d", k, n, maxMessage)
		}
		cmds = append(cmds, c)
	}
	cmds = append(cmds, append([]string{"respawn-pane", "-k", "-t", pane, "-c", cdir, "--"}, argv...))

	// The session is made on its own, so a failure here (the name taken, say)
	// never kills a session that was not ours.
	if out, err := l.run("new-session", "-d", "-s", session, "-c", cdir, "--", "tail", "-f", "/dev/null"); err != nil {
		return refuse(http.StatusInternalServerError, "tmux could not create the ship's session: %s", strings.TrimSpace(out))
	}
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
	cmd := exec.Command(l.Tmux, append([]string{"-f", "/dev/null", "-S", l.Socket}, args...)...)
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
