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
// installs a global `alert-bell` hook that stamps the ringing pane's @admiral-bell
// option with a new value on every bell, and the poll reports a change of it.
//
// The host never launches anything: the launcher runs the tmux binary with an
// argument list, because control mode is line-based and can't carry a
// multi-line prompt.
package sessionhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
)

// Internal is the hidden session control mode is attached to. No ship name
// slugs to it, and it is never reported.
const Internal = "_admiral"

// SetupName is the session that runs `admiral setup` for the browser. It is
// not a ship.
const SetupName = "_setup"

// ChatName is the session that runs the AdmiralBot chat (ADR 0018, 0019). It is
// not a ship.
const ChatName = launcher.ChatSession

// SetupTerminalEnv is set in the setup session, so `admiral setup` holds an error on screen.
const SetupTerminalEnv = "ADMIRAL_SETUP_TERMINAL"

// Prefix is the start of every option Admiral owns.
const Prefix = "@admiral-"

// PollInterval is how often the sessions are re-read.
const PollInterval = 500 * time.Millisecond

const sep = "\x1f"

// format is what one `list-panes` row holds, after the nonce (see rowFormat).
// Every free text in it (the session name and each @admiral-* option) goes
// through unprintable as a second layer: tmux prints option values raw, so a
// newline or a \x1f an agent put in its own pane's option could end its row
// early and forge another. tmux copies a value that is not valid UTF-8 through
// raw, whatever the substitution says, so the nonce is what holds.
var format = unprintable(rawFormat)

// newNonce is 16 random hex characters, fresh for every read.
func newNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system has no randomness; a fixed nonce would be none
	}
	return hex.EncodeToString(b[:])
}

// rowFormat is the format of one read: the read's nonce is the first field of
// every row. Option text is written before the read and can't know it, so a row
// it forges has no nonce, and ownRows drops it.
func rowFormat(nonce string) string { return nonce + sep + format }

// ownRows keeps the rows that start with the read's nonce, without it. A row
// broken by its own option text keeps its nonce and loses the field count,
// where parseRows holds its ship unreadable; the lines after a raw newline
// don't start with the nonce and are dropped before anything counts fields.
func ownRows(rows []string, nonce string) []string {
	own := make([]string, 0, len(rows))
	for _, r := range rows {
		if rest, ok := strings.CutPrefix(r, nonce+sep); ok {
			own = append(own, rest)
		}
	}
	return own
}

// ctrl matches the ASCII control characters, the newline and the field
// separator among them. The class is spelled with a range of literal bytes: the
// colon in [[:cntrl:]] ends a tmux format modifier.
const ctrl = "[\x01-\x1f\x7f]"

var freeText = regexp.MustCompile(`#\{(session_name|@[a-z-]+)\}`)

// unprintable wraps each free text of a format in a substitution that turns a
// control character into a space.
func unprintable(f string) string {
	return freeText.ReplaceAllString(f, "#{s/"+ctrl+"/ /:$1}")
}

const rawFormat = "#{session_name}" + sep + "#{pane_dead}" + sep + "#{pane_dead_status}" + sep +
	"#{pane_dead_signal}" + sep + "#{pane_dead_time}" + sep + "#{@admiral-bell}" + sep + "#{@admiral-state}" + sep +
	"#{@admiral-reason}" + sep + "#{@admiral-name}" + sep + "#{@admiral-fleet}" + sep + "#{@admiral-fleet-name}" + sep +
	"#{@admiral-fleet-color}" + sep + "#{@admiral-harness}" + sep + "#{@admiral-dir}" + sep +
	"#{@admiral-skill}" + sep + "#{@admiral-source}" + sep + "#{@admiral-order}" + sep +
	"#{@admiral-launched}" + sep + "#{@admiral-title}" + sep + "#{@admiral-url}" + sep + "#{@admiral-panel}" + sep + "#{@admiral-resources}" + sep +
	"#{@admiral-origin-source}" + sep + "#{@admiral-origin-panel}" + sep +
	"#{@admiral-" + OptEnded + "}" + sep + "#{@admiral-" + OptNeedsCount + "}" + sep +
	"#{@admiral-" + OptNeedsSecs + "}" + sep + "#{@admiral-" + OptStopSince + "}" + sep +
	"#{@admiral-crew-name}" + sep + "#{pane_id}" + sep + "#{window_id}" + sep + "#{" + ActivityOption + "}" + sep +
	"#{@admiral-role}" + sep + "#{" + SubagentsOption + "}"

// The session options Admiral keeps for the outcome log (ADR 0012): the marker
// of a ship whose `ended` was written, and the running needs-you tally.
const (
	OptEnded      = "ended"
	OptNeedsCount = "needs-count"
	OptNeedsSecs  = "needs-s"
	OptStopSince  = "stop-since"
)

// fixedFields is how many fields of a row come before the options.
const fixedFields = 8

// tailFields is how many fields of a row come after the options: the crew name
// (a window option; set only on a crew boat's window), the pane id, the window
// id, the pane's last hook time, the window's @admiral-role and the pane's
// @admiral-subagents. The subagents come last and outside optionNames (a change
// of them is no OptionChanged), after the fields parseCrew reads by position.
// tmux prints option values raw. The reader guards against a control character
// in one (see format): the nonce that starts every row of a read drops the lines
// a raw newline leaves, so none is parsed as a row; the substitution turns
// control characters into spaces, but tmux skips it for a value that is not
// valid UTF-8. A row that a value still breaks has the wrong length, and its
// ship is unreadable. `admiral hook` writes JSON, which escapes both.
const tailFields = 6

// optionNames are the session options in the order format lists them. The last
// four are Admiral's outcome bookkeeping (ended marker, needs-you tally).
var optionNames = []string{"name", "fleet", "fleet-name", "fleet-color", "harness", "dir",
	"skill", "source", "order", "launched", "title", "url", "panel", "resources", "origin-source", "origin-panel", OptEnded, OptNeedsCount, OptNeedsSecs, OptStopSince}

// Session is one tmux session as last read.
type Session struct {
	Name       string
	Options    map[string]string // without the @admiral- prefix; every name in optionNames is present
	State      string            // the pane option @admiral-state, "" until a hook sets it
	Reason     string            // the pane option @admiral-reason: why the ship needs you, "" when it does not
	PaneDead   bool
	ExitStatus int       // valid when PaneDead and Signal is empty
	Signal     string    // the signal that killed the pane, e.g. "kill"; then there is no exit status
	DeadTime   time.Time // when the pane died, at tmux's one-second resolution; zero while it lives
	BellStamp  string    // changes on every bell
	PaneID     string    // the ship's own pane, so writes never land on a crew boat's
	// Crew is the ship's crew boats (teammates), by name; Subagents the running
	// subagents of the ship's own pane, empty once that pane is dead. Neither is
	// part of set()'s event diff, and no crew row ever sets another field.
	Crew      []Crew
	Subagents []Subagent
}

// IsShip is true for a session Admiral launched (or that looks like one). The
// setup and chat sessions never are: the chat's Claude can set options on its
// own session, and must not make itself a ship by setting @admiral-name.
func (s Session) IsShip() bool {
	return s.Name != Internal && s.Name != SetupName && s.Name != ChatName && s.Options["name"] != ""
}

// EventKind says what changed.
type EventKind int

const (
	SessionAdded EventKind = iota
	SessionRemoved
	OptionChanged // Option holds the name without the prefix, Value the new value
	PaneDied      // Status is the exit status, or Signal names the signal
	Bell
	Resumed // the first read after a Reset
	Reset   // the connection to tmux was lost: no sessions are known until it is back
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
	clashes  map[string]bool // crew names two windows claim, as last logged
	unread   map[string]bool // sessions seen but unreadable (their row was malformed)
	subs     map[chan Event]struct{}

	cmdMu sync.Mutex // one command at a time on the control connection
	stdin io.Writer
	rep   chan reply
	queue []*inflight // command lists tmux has not finished answering, oldest first

	lost bool // set by a failed connection, until the next successful read

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
	out, _ := h.Snapshot()
	return out
}

// Snapshot is Sessions, and whether the connection to tmux is lost: then the
// list is empty because nothing is known, not because the ships are gone.
func (h *Host) Snapshot() (sessions []Session, lost bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Session, 0, len(h.order))
	for _, n := range h.order {
		out = append(out, h.sessions[n])
	}
	return out, h.lost
}

// Unreadable lists the sessions tmux reports but whose options could not be
// read (a value with a newline, say). They are alive: callers that decide
// what is gone, such as the position pruning, must count them.
func (h *Host) Unreadable() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.unread))
	for n := range h.unread {
		out = append(out, n)
	}
	sort.Strings(out)
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

// Known says the session list can be trusted: a read has succeeded and the
// connection is not lost. Before that, and while it is lost, the list is empty
// because nothing is known.
func (h *Host) Known() bool {
	select {
	case <-h.ready:
	default:
		return false
	}
	_, lost := h.Snapshot()
	return !lost
}

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
		h.set(nil, nil, false)
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
	cmd := exec.CommandContext(ctx, h.tmux, "-f", "/dev/null", "-S", h.sock, "-u", "-C", "new-session", "-A", "-s", Internal,
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
	h.stdin, h.rep, h.queue = stdin, p.replies, nil
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
// that installs the hook. The script stamps the ringing window's pane with the time and its
// own pid, so two bells in a second differ.
func (h *Host) bellHook() (string, error) {
	script := filepath.Join(filepath.Dir(h.sock), "harness", "bell.sh")
	body := "#!/bin/sh\n" + shQuote(h.tmux) + " -S " + shQuote(h.sock) +
		` set-option -p -t "$2" @admiral-bell "$(date +%s)-$$"` + "\n" + crewBell(h.tmux, h.sock)
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return "", err
	}
	run := "run-shell -b " + tmuxQuote(shQuote(script)+" #{q:hook_session_name} #{hook_window}")
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
	names, err := h.command(ctx, "list-sessions -F '#{session_name}'")
	if err != nil {
		return err
	}
	nonce := newNonce() // per command: the format is built for each poll
	rows, err := h.command(ctx, "list-panes -a -F '"+rowFormat(nonce)+"'")
	if err != nil {
		return err
	}
	rows = ownRows(rows, nonce)
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	next, unreadable := parseRows(rows, known)
	h.noteClashes(crewClashes(rows, known))
	h.set(next, unreadable, true)
	h.once.Do(func() { close(h.ready) })
	return nil
}

// inflight is a command list written to tmux and not yet fully answered.
type inflight struct {
	left  int // replies still to come
	lines []string
	err   error
}

// take records the next reply for the list. tmux stops a list at its first
// failing command, so an error reply is the list's last.
func (s *inflight) take(r reply) {
	s.left--
	s.lines = r.lines
	if r.err != nil {
		s.err, s.left = r.err, 0
	}
}

// command sends one command line and waits for its reply.
func (h *Host) command(ctx context.Context, line string) ([]string, error) {
	return h.commandList(ctx, line)
}

// commandList sends the commands as one `;`-joined line and waits for the reply
// to each. tmux runs the list as one unit, so no other client's command lands
// between them. It returns the last reply's lines, and the error of the command
// that failed, after which tmux ran no more of the list. A list that gives up
// (cancelled, or tmux too slow) stays in the queue, and the lists after it read
// and discard its replies, so no list ever reads another's.
func (h *Host) commandList(ctx context.Context, cmds ...string) ([]string, error) {
	if len(cmds) == 0 {
		return nil, fmt.Errorf("no commands to send")
	}
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	if h.stdin == nil {
		return nil, fmt.Errorf("not connected")
	}
	line := strings.Join(cmds, " ; ")
	if _, err := io.WriteString(h.stdin, line+"\n"); err != nil {
		return nil, err
	}
	mine := &inflight{left: len(cmds)}
	h.queue = append(h.queue, mine)
	for mine.left > 0 {
		select {
		case r, ok := <-h.rep:
			if !ok {
				return nil, fmt.Errorf("control connection closed")
			}
			head := h.queue[0]
			if head.take(r); head.left == 0 {
				h.queue = h.queue[1:]
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("tmux did not answer %q", line)
		}
	}
	return mine.lines, mine.err
}

// parseRows folds the rows of list-panes (one per pane) into one session each:
// the first pane that is not a crew boat's stands for the session. A row from a
// crew boat's window (its window options shadow the ship's) never sets a field of
// the session, whatever its order or state: it only joins Crew, read by
// parseCrew's rule. A session with only crew rows is not a session. known holds
// the session names list-sessions reported. A value holding a newline or the
// field separator can split a row or forge one, so a session is unreadable, and
// left out, when a row of it has the wrong number of fields, when its rows
// disagree on the options, or (for a row that starts with some other name)
// never: a row whose first field is no known session is dropped as a fragment.
func parseRows(rows []string, known map[string]bool) (sessions map[string]Session, unreadable []string) {
	sessions = map[string]Session{}
	bad := map[string]bool{}
	for _, row := range rows {
		f := strings.Split(row, sep)
		name := f[0]
		if name == Internal || !known[name] {
			continue
		}
		if len(f) != fixedFields+len(optionNames)+tailFields {
			bad[name] = true
			continue
		}
		if f[fixedFields+len(optionNames)] != "" {
			continue // a crew boat's pane: its window options shadow the ship's
		}
		s := Session{Name: name, PaneDead: f[1] == "1", Signal: f[3], BellStamp: f[5], State: f[6], Reason: f[7],
			Options: map[string]string{}, PaneID: f[fixedFields+len(optionNames)+1]}
		if !s.PaneDead {
			s.Subagents = DecodeSubagents(f[fixedFields+len(optionNames)+5])
		}
		s.ExitStatus, _ = strconv.Atoi(f[2])
		if sec, err := strconv.ParseInt(f[4], 10, 64); err == nil && sec > 0 {
			s.DeadTime = time.Unix(sec, 0)
		}
		for i, n := range optionNames {
			s.Options[n] = f[fixedFields+i]
		}
		if first, dup := sessions[name]; dup {
			if !maps.Equal(first.Options, s.Options) {
				bad[name] = true
			}
			continue
		}
		sessions[name] = s
	}
	crew := parseCrew(rows, known)
	for name, s := range sessions {
		s.Crew = crew[name]
		sessions[name] = s
	}
	for name := range bad {
		delete(sessions, name)
		unreadable = append(unreadable, name)
	}
	sort.Strings(unreadable)
	return sessions, unreadable
}

// noteClashes says once, on stderr, for each crew name that two windows of a
// session claim (the name is read for neither), and again if it clashes anew.
func (h *Host) noteClashes(now []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	next := map[string]bool{}
	for _, c := range now {
		next[c] = true
		if !h.clashes[c] {
			fmt.Fprintf(os.Stderr, "admiral: crew boat %s: two windows claim this name, so neither is listed\n", c)
		}
	}
	h.clashes = next
}

// set replaces the last read and publishes the difference as events. A failed
// connection (ok false) clears the sessions and sends one Reset instead of a
// removal per session, which would look like the ships were closed.
func (h *Host) set(next map[string]Session, unreadable []string, ok bool) {
	h.mu.Lock()
	prev := h.sessions
	h.unread = map[string]bool{}
	for _, name := range unreadable { // keep what we knew, rather than lose the ship
		h.unread[name] = true
		if old, had := prev[name]; had {
			if _, read := next[name]; !read {
				next[name] = old
			}
		}
	}
	var events []Event
	if !ok {
		h.lost = true
	} else if h.lost {
		h.lost = false
		events = append(events, Event{Kind: Resumed})
	}
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

// ReadOnce (tmux -u, like the control client: without a UTF-8 locale tmux would print the
// separator as "_" and every row would be unreadable) lists the sessions of the tmux server on sock with one command and
// no control connection, so it starts nothing. With no server running there
// are no sessions. Like Sessions, it leaves out the internal session.
func ReadOnce(tmux, sock string) ([]Session, error) {
	run := func(args ...string) ([]string, error) {
		out, err := exec.Command(tmux, append([]string{"-f", "/dev/null", "-S", sock, "-u"}, args...)...).Output()
		if err != nil {
			return nil, err
		}
		return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), nil
	}
	names, err := run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, nil // no server on the socket: nothing is running
	}
	nonce := newNonce()
	rows, err := run("list-panes", "-a", "-F", rowFormat(nonce))
	if err != nil {
		return nil, err
	}
	rows = ownRows(rows, nonce)
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	found, _ := parseRows(rows, known)
	out := make([]Session, 0, len(found))
	for _, s := range found {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
