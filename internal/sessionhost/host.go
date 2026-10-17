// Package sessionhost is Admiral's one connection to its own tmux server
// (ADR 0002, 0003, 0012): a control-mode client on $ADMIRAL_HOME/tmux.sock that
// lists the sessions, reads their @admiral-* options and reports what changes.
//
// Watching: tmux 3.7c sends control-mode notifications for new and closed
// sessions but none when an option changes on another session, when a pane
// dies or on a bell. So the host re-reads everything with one `list-panes -a`
// command about every half second, and at once on a %sessions-changed
// notification, and turns the difference into events. A subscription format
// (`refresh-client -B`) would report at most once a second; the poll is one
// cheap command whose reply is a few hundred bytes. Pane death (exit status
// or signal) is read from pane_dead*. A bell is the one thing the poll can't
// see: window_bell_flag stays set after the first bell, and stays 0 while a
// control client is attached to the ship (the bridge, later), so the host
// installs a global `alert-bell` hook that stamps the session's @admiral-bell
// option with a new value on every bell, and the poll reports a change of it.
//
// The host never launches anything: the launcher runs the tmux binary with an
// argument list, because control mode is line-based and can't carry a
// multi-line prompt.
package sessionhost

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Internal is the hidden session control mode is attached to. No ship name
// slugs to it, and it is never reported.
const Internal = "_admiral"

// Prefix is the start of every option Admiral owns.
const Prefix = "@admiral-"

// PollInterval is how often the sessions are re-read.
const PollInterval = 500 * time.Millisecond

const sep = "\x1f"

// format is what one `list-panes -a` row holds.
const format = "#{session_name}" + sep + "#{pane_dead}" + sep + "#{pane_dead_status}" + sep +
	"#{pane_dead_signal}" + sep + "#{@admiral-bell}" + sep + "#{@admiral-state}" + sep +
	"#{@admiral-name}" + sep + "#{@admiral-fleet}" + sep + "#{@admiral-fleet-name}" + sep +
	"#{@admiral-fleet-color}" + sep + "#{@admiral-harness}" + sep + "#{@admiral-dir}" + sep +
	"#{@admiral-skill}" + sep + "#{@admiral-source}" + sep + "#{@admiral-order}" + sep +
	"#{@admiral-launched}"

// optionNames are the session options in the order format lists them.
var optionNames = []string{"name", "fleet", "fleet-name", "fleet-color", "harness", "dir",
	"skill", "source", "order", "launched"}

// Session is one tmux session as last read.
type Session struct {
	Name       string
	Options    map[string]string // without the @admiral- prefix; every name in optionNames is present
	State      string            // the pane option @admiral-state, "" until a hook sets it
	PaneDead   bool
	ExitStatus int    // valid when PaneDead and Signal is empty
	Signal     string // the signal that killed the pane, e.g. "kill"; then there is no exit status
	BellStamp  string // changes on every bell
}

// IsShip is true for a session Admiral launched (or that looks like one).
func (s Session) IsShip() bool { return s.Name != Internal && s.Options["name"] != "" }

// EventKind says what changed.
type EventKind int

const (
	SessionAdded EventKind = iota
	SessionRemoved
	OptionChanged // Option holds the name without the prefix, Value the new value
	PaneDied      // Status is the exit status, or Signal names the signal
	Bell
	Reset // the connection to tmux was lost: no sessions are known until it is back
)

// Event is one change seen between two reads.
type Event struct {
	Kind    EventKind
	Session string
	Option  string
	Value   string
	Status  int
	Signal  string // PaneDied: set when a signal killed the pane (Status is then 0)
}

// Host is the connection and the last read.
type Host struct {
	sock string
	tmux string // the tmux binary
	env  []string

	mu       sync.Mutex
	sessions map[string]Session
	order    []string
	subs     map[chan Event]struct{}

	cmdMu sync.Mutex // one command at a time on the control connection
	stdin io.Writer
	rep   chan reply

	pollMu sync.Mutex // a read and publishing it, together, so reads land in order
	wake   chan struct{}
	ready  chan struct{} // closed after the first successful read
	once   sync.Once
}

// New prepares a host for the tmux server on sock. tmux is the binary to run.
func New(tmux, sock string) *Host {
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_PANE=") {
			env = append(env, kv)
		}
	}
	return &Host{sock: sock, tmux: tmux, env: env, sessions: map[string]Session{},
		subs: map[chan Event]struct{}{}, wake: make(chan struct{}, 1), ready: make(chan struct{})}
}

// Socket is the tmux socket path.
func (h *Host) Socket() string { return h.sock }

// Sessions returns the ships' sessions in a stable order (by name).
func (h *Host) Sessions() []Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Session, 0, len(h.order))
	for _, n := range h.order {
		out = append(out, h.sessions[n])
	}
	return out
}

// Subscribe returns a channel of events. A subscriber that doesn't keep up
// loses events rather than stalling the host; it should call Sessions to catch up.
func (h *Host) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// Ready is closed once the sessions have been read for the first time.
func (h *Host) Ready() <-chan struct{} { return h.ready }

// Sync reads the sessions now and returns when the new state is published.
func (h *Host) Sync(ctx context.Context) error { return h.poll(ctx) }

// Refresh asks for a read now (after a launch, say) and returns without waiting.
func (h *Host) Refresh() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Run keeps a control connection up until ctx ends, reconnecting if tmux goes
// away. If the server is gone there are no ships until it is back.
func (h *Host) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := h.connect(ctx)
		h.set(nil, false)
		if err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "admiral: tmux:", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (h *Host) connect(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, h.tmux, "-S", h.sock, "-u", "-C", "new-session", "-A", "-s", Internal,
		"tail", "-f", "/dev/null")
	cmd.Env = h.env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p := &parser{replies: make(chan reply, 16), notify: func(line string) {
		if strings.HasPrefix(line, "%sessions-changed") {
			h.Refresh()
		}
	}}
	parsed := make(chan error, 1)
	go func() { parsed <- p.run(stdout); close(p.replies) }()

	h.cmdMu.Lock()
	h.stdin, h.rep = stdin, p.replies
	h.cmdMu.Unlock()
	defer func() {
		_ = stdin.Close()
		h.cmdMu.Lock()
		h.stdin, h.rep = nil, nil
		h.cmdMu.Unlock()
		_ = cmd.Wait()
	}()

	hook, err := h.bellHook()
	if err != nil {
		return err
	}
	for _, c := range []string{"set-option -g history-limit 10000", hook} {
		if _, err := h.command(ctx, c); err != nil {
			return err
		}
	}
	for ctx.Err() == nil {
		if err := h.poll(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
		case <-h.wake:
		case <-time.After(PollInterval):
		case err := <-parsed:
			return fmt.Errorf("control connection closed: %v", err)
		}
	}
	return ctx.Err()
}

// bellHook writes the script the alert-bell hook runs and returns the command
// that installs the hook. The script stamps the session with the time and its
// own pid, so two bells in a second differ.
func (h *Host) bellHook() (string, error) {
	script := filepath.Join(filepath.Dir(h.sock), "harness", "bell.sh")
	body := "#!/bin/sh\nexec " + shQuote(h.tmux) + " -S " + shQuote(h.sock) +
		` set-option -t "=$1:" @admiral-bell "$(date +%s)-$$"` + "\n"
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return "", err
	}
	run := "run-shell -b " + tmuxQuote(shQuote(script)+" #{hook_session_name}")
	return "set-hook -g alert-bell " + tmuxQuote(run), nil
}

// shQuote quotes s for sh.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// tmuxQuote quotes s as one tmux argument; # is left for format expansion.
func tmuxQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(s) + `"`
}

func (h *Host) poll(ctx context.Context) error {
	h.pollMu.Lock()
	defer h.pollMu.Unlock()
	rows, err := h.command(ctx, "list-panes -a -F '"+format+"'")
	if err != nil {
		return err
	}
	h.set(parseRows(rows), true)
	h.once.Do(func() { close(h.ready) })
	return nil
}

// command sends one command line and waits for its reply.
func (h *Host) command(ctx context.Context, line string) ([]string, error) {
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	if h.stdin == nil {
		return nil, fmt.Errorf("not connected")
	}
	if _, err := io.WriteString(h.stdin, line+"\n"); err != nil {
		return nil, err
	}
	for {
		select {
		case r, ok := <-h.rep:
			if !ok {
				return nil, fmt.Errorf("control connection closed")
			}
			return r.lines, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("tmux did not answer %q", line)
		}
	}
}

// parseRows folds the rows of list-panes (one per pane) into one session each:
// the first pane stands for the session.
func parseRows(rows []string) map[string]Session {
	out := map[string]Session{}
	for _, row := range rows {
		f := strings.Split(row, sep)
		if len(f) != 6+len(optionNames) || f[0] == Internal {
			continue
		}
		if _, dup := out[f[0]]; dup {
			continue
		}
		s := Session{Name: f[0], PaneDead: f[1] == "1", Signal: f[3], BellStamp: f[4], State: f[5],
			Options: map[string]string{}}
		s.ExitStatus, _ = strconv.Atoi(f[2])
		for i, n := range optionNames {
			s.Options[n] = f[6+i]
		}
		out[f[0]] = s
	}
	return out
}

// set replaces the last read and publishes the difference as events. A failed
// connection (ok false) clears the sessions and sends one Reset instead of a
// removal per session, which would look like the ships were closed.
func (h *Host) set(next map[string]Session, ok bool) {
	h.mu.Lock()
	prev := h.sessions
	var events []Event
	for name, s := range next {
		old, had := prev[name]
		if !had {
			events = append(events, Event{Kind: SessionAdded, Session: name})
		}
		for _, n := range optionNames {
			if v := s.Options[n]; v != old.Options[n] {
				events = append(events, Event{Kind: OptionChanged, Session: name, Option: n, Value: v})
			}
		}
		if s.State != old.State {
			events = append(events, Event{Kind: OptionChanged, Session: name, Option: "state", Value: s.State})
		}
		// What happened before Admiral looked is state, not an event.
		if had && s.PaneDead && !old.PaneDead {
			events = append(events, Event{Kind: PaneDied, Session: name, Status: s.ExitStatus, Signal: s.Signal})
		}
		if had && s.BellStamp != "" && s.BellStamp != old.BellStamp {
			events = append(events, Event{Kind: Bell, Session: name})
		}
	}
	for name := range prev {
		if _, there := next[name]; !there && ok {
			events = append(events, Event{Kind: SessionRemoved, Session: name})
		}
	}
	if !ok && len(prev) > 0 {
		events = append(events, Event{Kind: Reset})
	}
	if next == nil {
		next = map[string]Session{}
	}
	h.sessions = next
	h.order = h.order[:0]
	for n := range next {
		h.order = append(h.order, n)
	}
	sort.Strings(h.order)
	subs := make([]chan Event, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ev := range events {
		for _, ch := range subs {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}
