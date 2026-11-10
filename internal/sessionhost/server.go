package sessionhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Server runs one-shot tmux commands on Admiral's socket (ADR 0002): starting
// and ending sessions and windows, reading and writing what the Host does not
// keep, typing into a pane. It needs no Host and no running server of its own,
// which is what `admiral crew` and the hooks rely on. Each call is its own tmux
// client process; the Host's control connection never carries a launch, since
// control mode is line-based and can't carry a multi-line prompt.
//
// NewServer is the launcher's style: -u first, no user config, TMUX and
// TMUX_PANE dropped from the environment, and a failure that carries the
// command's whole output. The modifiers give the other callers' styles.
type Server struct {
	tmux, socket string
	noUTF8       bool // no leading -u
	keepEnv      bool // TMUX and TMUX_PANE stay in the environment
	split        bool // a failure carries stderr alone
}

// NewServer is the server of tmux, the binary, on socket.
func NewServer(tmux, socket string) Server { return Server{tmux: tmux, socket: socket} }

// WithoutUTF8 drops the leading -u.
func (s Server) WithoutUTF8() Server { s.noUTF8 = true; return s }

// KeepingTMUXEnv leaves TMUX and TMUX_PANE in the environment.
func (s Server) KeepingTMUXEnv() Server { s.keepEnv = true; return s }

// StderrErrors reads a failure's text from stderr only, and the answer of a
// success from stdout only.
func (s Server) StderrErrors() Server { s.split = true; return s }

// FindTmux is the tmux binary on PATH.
func FindTmux() (string, error) { return exec.LookPath("tmux") }

// maxCommand keeps one tmux invocation under the size tmux's client protocol
// carries in a single message (16 KiB).
const maxCommand = 12000

// MaxMessage is what one tmux client message holds; the harness's command line
// goes in a single one (respawn-pane), so a longer one cannot be launched.
const MaxMessage = 15000

// CommandSize is the bytes argv takes in a tmux message.
func CommandSize(argv []string) int {
	n := 0
	for _, a := range argv {
		n += len(a) + 1
	}
	return n
}

// CallError is a tmux command that failed: its name, what it printed and how it
// ended.
type CallError struct {
	Command string
	Output  string
	Err     error
}

func (e *CallError) Error() string {
	return "tmux " + e.Command + ": " + e.Err.Error() + ": " + strings.TrimSpace(e.Output)
}

func (e *CallError) Unwrap() error { return e.Err }

// Output is the text tmux printed for a failed call, trimmed.
func Output(err error) string {
	var ce *CallError
	if errors.As(err, &ce) {
		return strings.TrimSpace(ce.Output)
	}
	return ""
}

// LimitError is a command that cannot be launched because it would not fit the
// size tmux carries.
type LimitError struct{ Msg string }

func (e *LimitError) Error() string { return e.Msg }

// call is one tmux invocation.
type call struct {
	ctx       context.Context
	args      []string
	path      string // replaces PATH in the environment when set
	stdin     string
	utf8After bool // -u after the socket instead of first
}

// run starts tmux with the call and returns what it printed.
func (s Server) run(c call) (string, error) {
	if c.ctx == nil {
		c.ctx = context.Background()
	}
	argv := []string{"-f", "/dev/null", "-S", s.socket}
	switch {
	case c.utf8After:
		argv = append(argv, "-u")
	case !s.noUTF8:
		argv = append([]string{"-u"}, argv...)
	}
	cmd := exec.CommandContext(c.ctx, s.tmux, append(argv, c.args...)...)
	if !s.keepEnv {
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") || (c.path != "" && strings.HasPrefix(kv, "PATH=")) {
				continue
			}
			cmd.Env = append(cmd.Env, kv)
		}
		if c.path != "" {
			cmd.Env = append(cmd.Env, "PATH="+c.path)
		}
	}
	if c.stdin != "" {
		cmd.Stdin = strings.NewReader(c.stdin)
	}
	var out, errOut bytes.Buffer
	if s.split {
		cmd.Stdout, cmd.Stderr = &out, &errOut
	} else {
		cmd.Stdout, cmd.Stderr = &out, &out
	}
	if err := cmd.Run(); err != nil {
		text := out.String()
		if s.split {
			text = errOut.String()
		}
		return out.String(), &CallError{Command: c.args[0], Output: text, Err: err}
	}
	return out.String(), nil
}

// batches joins commands with ";" into tmux invocations that fit maxCommand.
func batches(cmds [][]string) [][]string {
	var out [][]string
	var cur []string
	size := 0
	for _, c := range cmds {
		n := CommandSize(c)
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

// literalDir is dir as -c takes it: tmux format-expands -c, and ## is a literal #.
func literalDir(dir string) string { return strings.ReplaceAll(dir, "#", "##") }

// placeholder is the command a session or window starts with, replaced by the
// harness once its options are set: new-session and new-window start their
// command at once, and the harness's first hook must find the options already there.
var placeholder = []string{"tail", "-f", "/dev/null"}

// SessionRows are the sessions on the server as (name, @admiral-name) pairs.
// A server that is not running has none. Any other failure is an error whose
// text is tmux's.
func (s Server) SessionRows() ([][2]string, error) {
	out, err := s.run(call{args: []string{"list-sessions", "-F", "#{session_name}\x1f#{" + Prefix + "name}"}})
	if err != nil && !strings.Contains(out, "no server running") && !strings.Contains(out, "error connecting") {
		return nil, errors.New(strings.TrimSpace(out))
	}
	var rows [][2]string
	for _, row := range strings.Split(out, "\n") {
		if a, b, ok := strings.Cut(row, "\x1f"); ok {
			rows = append(rows, [2]string{a, b})
		}
	}
	return rows, nil
}

// SessionSpec is a session to start: Options are the @admiral-* options without
// the prefix, Env the session environment, Unset the variables hidden from it,
// and Argv the harness that replaces the placeholder. A session that is not
// Anchored ends with its harness (the chat, ADR 0019); a ship's stays, rings the
// bell flag and keeps its pane after the harness exits (ADR 0008).
type SessionSpec struct {
	Name    string
	Dir     string
	Options [][2]string
	Env     map[string]string
	Unset   []string
	Argv    []string
	Anchor  bool
}

// StartSession makes the session on a placeholder, sets its options, then
// replaces the placeholder with the harness. An environment variable too long
// for tmux is a *LimitError, and nothing is created. Any other failure is an
// error with tmux's own text.
func (s Server) StartSession(spec SessionSpec) error {
	target := "=" + spec.Name + ":" // "=" matches the name exactly; tmux wants the colon
	pane := target
	cdir := literalDir(spec.Dir)
	cmds := [][]string{{"set-option", "-t", target, "history-limit", "10000"}}
	if spec.Anchor {
		cmds = append(cmds, []string{"set-option", "-p", "-t", pane, "remain-on-exit", "on"},
			[]string{"set-option", "-w", "-t", pane, "monitor-bell", "on"})
	}
	for _, o := range spec.Options {
		cmds = append(cmds, []string{"set-option", "-t", target, Prefix + o[0], o[1]})
	}
	for _, k := range spec.Unset {
		// -r, not -u: -u would fall back to the server's own environment.
		cmds = append(cmds, []string{"set-environment", "-r", "-t", target, k})
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := []string{"set-environment", "-t", target, k, spec.Env[k]}
		if n := CommandSize(c); n > MaxMessage {
			return &LimitError{fmt.Sprintf("environment variable %s is too long to launch: %d bytes and tmux carries at most %d", k, n, MaxMessage)}
		}
		cmds = append(cmds, c)
	}
	cmds = append(cmds, append([]string{"respawn-pane", "-k", "-t", pane, "-c", cdir, "--"}, spec.Argv...))

	// The session is made on its own, so a failure here (the name taken, say)
	// never kills a session that was not ours.
	if out, err := s.run(call{args: append([]string{"new-session", "-d", "-s", spec.Name, "-c", cdir, "--"}, placeholder...)}); err != nil {
		return fmt.Errorf("tmux could not create the ship's session: %s", strings.TrimSpace(out))
	}
	// A pane's PATH is the one of the tmux client that spawns it, whatever the
	// session environment says, so that client runs with the ship's PATH.
	for _, batch := range batches(cmds) {
		if out, err := s.run(call{args: batch, path: spec.Env["PATH"]}); err != nil {
			_, _ = s.run(call{args: []string{"kill-session", "-t", target}})
			return fmt.Errorf("tmux could not start the ship: %s", strings.TrimSpace(out))
		}
	}
	return nil
}

// ShipOption is the session option of a ship, without the prefix. The error's
// text is tmux's: there is no such session.
func (s Server) ShipOption(session, name string) (string, error) {
	out, err := s.run(call{args: []string{"show-options", "-qv", "-t", "=" + session + ":", Prefix + name}})
	if err != nil {
		return "", errors.New(strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// CrewNames are the names of the crew boats in the ship's windows. A window with
// none gives "".
func (s Server) CrewNames(session string) ([]string, error) {
	out, err := s.run(call{args: []string{"list-windows", "-t", "=" + session + ":", "-F", CrewNameFormat}})
	if err != nil {
		return nil, errors.New(strings.TrimSpace(out))
	}
	return strings.Split(out, "\n"), nil
}

// CrewSpec is a teammate to start in a new window of the ship's session. Env is
// what the dock sets, Command the harness under `env -u`, and ClientPath the PATH
// of the tmux client that spawns the pane. The window is made on a placeholder,
// given its options, then respawned as the harness.
type CrewSpec struct {
	Session    string
	Name       string
	Harness    string
	Dir        string // absolute
	Role       string // "" when none
	Env        map[string]string
	Command    []string
	ClientPath string
}

// StartCrew starts the crew boat. A variable too long for tmux is a
// *LimitError; the window is killed when it cannot be started.
func (s Server) StartCrew(spec CrewSpec) error {
	target := "=" + spec.Session + ":"
	cdir := literalDir(spec.Dir)
	win, err := s.run(call{args: append([]string{"new-window", "-d", "-P", "-F", "#{window_id}", "-t", target, "-n", spec.Name, "-c", cdir, "--"}, placeholder...)})
	if err != nil {
		return fmt.Errorf("tmux could not create the crew window: %s", strings.TrimSpace(win))
	}
	w := strings.TrimSpace(win)
	kill := func() { _, _ = s.run(call{args: []string{"kill-window", "-t", w}}) }
	cmds := [][]string{
		{"set-option", "-w", "-t", w, "remain-on-exit", "on"},
		{"set-option", "-w", "-t", w, "monitor-bell", "on"},
		{"set-option", "-w", "-t", w, "automatic-rename", "off"},
	}
	opts := [][2]string{{"crew-name", spec.Name}, {"harness", spec.Harness}, {"dir", spec.Dir},
		{"launched", time.Now().UTC().Format(time.RFC3339)}}
	if spec.Role != "" {
		opts = append(opts, [2]string{"role", spec.Role})
	}
	for _, o := range opts {
		cmds = append(cmds, []string{"set-option", "-w", "-t", w, Prefix + o[0], o[1]})
	}
	respawn := []string{"respawn-pane", "-k", "-t", w, "-c", cdir}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		if k != "PATH" { // the window's PATH is the spawning client's
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		kv := k + "=" + spec.Env[k]
		if len(kv) > MaxMessage {
			kill()
			return &LimitError{fmt.Sprintf("environment variable %s is too long to launch", k)}
		}
		respawn = append(respawn, "-e", kv)
	}
	cmds = append(cmds, append(append(respawn, "--"), spec.Command...))
	for _, batch := range batches(cmds) {
		if out, err := s.run(call{args: batch, path: spec.ClientPath}); err != nil {
			kill()
			return fmt.Errorf("tmux could not start the crew boat: %s", strings.TrimSpace(out))
		}
	}
	return nil
}

// ErrWindowGone is KillWindow's answer for a window that is already gone; the
// error's text is tmux's own.
var ErrWindowGone = errors.New("crew window is gone")

type windowGone struct{ msg string }

func (e windowGone) Error() string        { return e.msg }
func (e windowGone) Is(target error) bool { return target == ErrWindowGone }

// KillWindow kills one window by its tmux window id, and with it the process in
// it; ctx bounds the kill. tmux never reuses a window id within one server, so an
// id from a stale read can't close another boat's window.
func (s Server) KillWindow(ctx context.Context, windowID string) error {
	out, err := s.run(call{ctx: ctx, args: []string{"kill-window", "-t", windowID}})
	if err == nil {
		return nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		out = err.Error()
	}
	if strings.Contains(out, "can't find window") {
		return windowGone{out}
	}
	return errors.New(out)
}

// GlobalEnvNames are the names of the server's global environment. A server that
// is not up yet has none, and is an error.
func (s Server) GlobalEnvNames() ([]string, error) {
	out, err := s.run(call{args: []string{"show-environment", "-g"}})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		names = append(names, strings.TrimPrefix(strings.SplitN(line, "=", 2)[0], "-"))
	}
	return names, nil
}

// HasSession says whether the session is on the server.
func (s Server) HasSession(ctx context.Context, name string) bool {
	_, err := s.run(call{ctx: ctx, args: []string{"has-session", "-t", "=" + name}})
	return err == nil
}

// KillSession ends the session by its name.
func (s Server) KillSession(ctx context.Context, name string) error {
	_, err := s.run(call{ctx: ctx, args: []string{"kill-session", "-t", "=" + name}})
	return err
}

// StartDetached starts a session of the given size running argv, with env as its
// session environment (NAME=value). A failure is the exit error alone.
func (s Server) StartDetached(name string, cols, rows int, env, argv []string) error {
	args := []string{"new-session", "-d", "-s", name, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)}
	for _, kv := range env {
		args = append(args, "-e", kv)
	}
	_, err := s.run(call{args: append(append(args, "--"), argv...)})
	return errors.Unwrap(err)
}

// ChatOptions are the chat's options as one read returns them.
type ChatOptions struct {
	Model, Effort, Fleet string
	Transcript           string // "<unix ns> <source> <path>", "" before the first SessionStart
}

// ReadChatOptions reads the chat's options off its pane in one call. The chat can
// write any of them. The transcript is last, so a separator in it can't shift the
// others; one in another option only shifts the fields after it, which makes the
// settings look changed (New chat then restarts the session).
func (s Server) ReadChatOptions(ctx context.Context, pane string) (ChatOptions, error) {
	format := strings.Join([]string{"#{" + ChatModelOption + "}", "#{" + ChatEffortOption + "}",
		"#{" + ChatFleetOption + "}", "#{" + TranscriptOption + "}"}, sep)
	out, err := s.run(call{ctx: ctx, utf8After: true, args: []string{"display-message", "-p", "-t", pane, format}})
	if err != nil {
		return ChatOptions{}, err
	}
	f := strings.SplitN(strings.TrimSuffix(out, "\n"), sep, 4)
	if len(f) != 4 {
		return ChatOptions{}, fmt.Errorf("unexpected options %q", out)
	}
	return ChatOptions{Model: f[0], Effort: f[1], Fleet: f[2], Transcript: f[3]}, nil
}

// WindowSize is a window of a session and the size it has.
type WindowSize struct {
	ID         string // @N
	Cols, Rows int
}

// ParseSize reads "COLSxROWS".
func ParseSize(s string) (cols, rows int, ok bool) {
	a, b, found := strings.Cut(s, "x")
	cols, e1 := strconv.Atoi(a)
	rows, e2 := strconv.Atoi(b)
	return cols, rows, found && e1 == nil && e2 == nil && cols > 0 && rows > 0
}

// WindowSizes are the sizes the session's windows have now. A session that
// cannot be read has none.
func (s Server) WindowSizes(session string) []WindowSize {
	out, err := s.run(call{utf8After: true, args: []string{"list-windows", "-t", "=" + session + ":", "-F", "#{window_id} #{window_width}x#{window_height}"}})
	if err != nil {
		return nil
	}
	var sizes []WindowSize
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		id, size, ok := strings.Cut(l, " ")
		if cols, rows, sized := ParseSize(size); ok && strings.HasPrefix(id, "@") && sized {
			sizes = append(sizes, WindowSize{id, cols, rows})
		}
	}
	return sizes
}

// LoadBuffer puts text in the named paste buffer, on stdin, so it never goes on
// a command line.
func (s Server) LoadBuffer(ctx context.Context, buf, text string) error {
	_, err := s.run(call{ctx: ctx, stdin: text, args: []string{"load-buffer", "-b", buf, "-"}})
	return err
}

// PasteBuffer pastes the buffer into the pane as one bracketed paste and deletes
// it. keepLF keeps LF as LF.
func (s Server) PasteBuffer(ctx context.Context, pane, buf string, keepLF bool) error {
	args := []string{"paste-buffer", "-p"}
	if keepLF {
		args = append(args, "-r")
	}
	_, err := s.run(call{ctx: ctx, args: append(args, "-d", "-b", buf, "-t", pane)})
	return err
}

// DeleteBuffer drops the named paste buffer.
func (s Server) DeleteBuffer(ctx context.Context, buf string) error {
	_, err := s.run(call{ctx: ctx, args: []string{"delete-buffer", "-b", buf}})
	return err
}

// SendLiteral types text into the pane as it is, so a slash command runs.
func (s Server) SendLiteral(ctx context.Context, pane, text string) error {
	_, err := s.run(call{ctx: ctx, args: []string{"send-keys", "-t", pane, "-l", text}})
	return err
}

// SendEnter presses Enter in the pane.
func (s Server) SendEnter(ctx context.Context, pane string) error {
	_, err := s.run(call{ctx: ctx, args: []string{"send-keys", "-t", pane, "Enter"}})
	return err
}

// ShipTarget is the ship's own window, never "=session": that is whichever
// window is current.
func ShipTarget(session string) string { return "=" + session + ":^" }

// CrewTarget is a crew boat's window of the ship's session.
func CrewTarget(session, windowID string) string { return "=" + session + ":" + windowID }

// ExecAttach replaces this process with tmux attached to target on the socket:
// the pane keeps the size the bridge gave it (ignore-size), and readOnly attaches
// with -r. It runs the user's own tmux config, as `tmux attach` does, and needs
// no Host.
func ExecAttach(tmux, socket, target string, readOnly bool) error {
	argv := []string{tmux, "-u", "-S", socket, "attach", "-f", "ignore-size"}
	if readOnly {
		argv = append(argv, "-r")
	}
	return syscall.Exec(tmux, append(argv, "-t", target), os.Environ())
}
