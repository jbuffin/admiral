// Package bridge is the terminal a browser opens on a ship or on one of its
// crew boats (ADR 0011). One tmux control client per watched session feeds every
// browser watching the ship or any of its crew boats: a joining browser gets the
// screen and scrollback of its pane, then the live %output stream; keystrokes go
// back with send-keys; the latest active browser sizes its window.
//
// Wire protocol: GET /ws/bridge/<the ship's tmux session name>, or
// GET /ws/bridge/<session>/crew/<crew name> for a crew boat, a WebSocket. The
// first message from the browser is {"type":"join","cols":C,"rows":R}. The
// messages are described in docs/bridge-protocol.md.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// DefaultIdle is how long a session's control client stays attached after the
// last browser leaves the ship and all its crew boats. ADMIRAL_BRIDGE_IDLE
// overrides it (a Go duration).
const DefaultIdle = 30 * time.Second

// scrollbackLines is how much history a joining browser gets.
const scrollbackLines = 2000

// Ships is what the bridge needs of the ships service.
type Ships interface {
	Get(session string) (ships.Ship, bool)
	InputSeen(session string) (ships.Ship, error)
	Crew(session string) ([]sessionhost.Crew, bool)
	CrewInputSeen(session, windowID string) (sessionhost.Crew, error)
}

// Service serves the bridge WebSocket.
type Service struct {
	Tmux   string
	Socket string
	Ships  Ships
	Idle   time.Duration

	// Host and Setup serve the setup terminal (u12): the `_setup` session that
	// runs `admiral setup`, watched like a ship but never listed as one. Both
	// are optional.
	Host    *sessionhost.Host
	Setup   *SetupCommand
	setupMu sync.Mutex

	mu      sync.Mutex
	conns   map[string]*conn
	closing bool
}

// New prepares the service. tmux is the binary; sock is Admiral's socket.
func New(tmux, sock string, sh Ships) *Service {
	idle := DefaultIdle
	if d, err := time.ParseDuration(os.Getenv("ADMIRAL_BRIDGE_IDLE")); err == nil && d > 0 {
		idle = d
	}
	return &Service{Tmux: tmux, Socket: sock, Ships: sh, Idle: idle, conns: map[string]*conn{}}
}

// Routes registers the bridge endpoints.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ws/bridge/{session}", s.serve)
	mux.HandleFunc("GET /ws/bridge/{session}/crew/{name}", s.serveCrew)
	mux.HandleFunc("GET /api/setup", s.serveSetupStatus)
	mux.HandleFunc("POST /api/setup", s.serveSetupStart)
}

// Connected does nothing: the bridge sends nothing on the main socket.
func (s *Service) Connected(*server.Client) {}

func (s *Service) allConns() []*conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := make([]*conn, 0, len(s.conns))
	for _, c := range s.conns {
		all = append(all, c)
	}
	return all
}

// Run keeps the service until ctx ends, then detaches every control client.
func (s *Service) Run(ctx context.Context) {
	<-ctx.Done()
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	all := s.allConns()
	for _, c := range all {
		c.shutdown()
	}
	for _, c := range all {
		if cl := c.attached(); cl != nil {
			select {
			case <-cl.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

// ShipsChanged tells every open bridge to look at its ship or crew boat again.
// It never blocks.
func (s *Service) ShipsChanged() {
	for _, c := range s.allConns() {
		for _, w := range c.snapshot() {
			w.shipChanged()
		}
	}
}

// conn is the one control client of a session, shared by the ship's watch and
// the watch of each crew boat. A control client receives the %output of every
// pane of its session, so each watch filters on its own pane.
type conn struct {
	s       *Service
	session string

	ready  chan struct{} // closed once the client has attached
	client *sessionhost.Client
	err    error

	mu      sync.Mutex
	watches map[string]*watch // by key: "" for the ship, the window id for a crew boat
	linger  *time.Timer
	gone    bool
}

// watch is one pane and the window it sits in, and the browsers watching them:
// the ship's, or a crew boat's.
type watch struct {
	c    *conn
	key  string // "" for the ship, else the crew boat's window id
	crew bool

	ready chan struct{} // closed once the pane and window are known
	pane  string
	win   string
	err   error

	mu       sync.Mutex
	browsers map[*browser]struct{}
	size     [2]int
	lastShip shipMsg
}

var errGone = errors.New("the ship's bridge has closed")

// target says which pane a browser asks for. The ship's own pane is looked up
// in tmux; a crew boat's ids come from the handshake.
type target struct {
	crew      bool
	pane, win string
}

func (t target) key() string {
	if t.crew {
		return t.win
	}
	return ""
}

// enter adds b to the watch of the target, starting the session's control client
// when b is the first browser on it. Browsers are added before the attach
// finishes so the linger timer can't detach under them.
func (s *Service) enter(session string, t target, b *browser) (*watch, error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, errGone
	}
	c := s.conns[session]
	freshConn := c == nil
	if freshConn {
		c = &conn{s: s, session: session, ready: make(chan struct{}), watches: map[string]*watch{}}
		s.conns[session] = c
	}
	c.mu.Lock()
	w := c.watches[t.key()]
	freshWatch := w == nil
	if freshWatch {
		w = &watch{c: c, key: t.key(), crew: t.crew, pane: t.pane, win: t.win, ready: make(chan struct{}),
			browsers: map[*browser]struct{}{}}
		if t.crew {
			close(w.ready)
		}
		c.watches[w.key] = w
	}
	w.mu.Lock()
	w.browsers[b] = struct{}{}
	w.mu.Unlock()
	if c.linger != nil {
		c.linger.Stop()
		c.linger = nil
	}
	c.mu.Unlock()
	s.mu.Unlock()

	if freshConn {
		c.start()
	}
	<-c.ready
	c.mu.Lock()
	err, gone := c.err, c.gone
	c.mu.Unlock()
	if err == nil && gone { // the session ended while the client attached
		err = errGone
	}
	if err != nil {
		w.leave(b)
		return nil, err
	}
	if freshWatch && !t.crew {
		w.resolve()
	}
	<-w.ready
	if w.err != nil {
		w.leave(b)
		return nil, w.err
	}
	return w, nil
}

// start reads the sizes the session's windows have, attaches the control client
// and pins every window to its size. Attaching a control client sets each window
// it holds no size for to the client's own, 80x24 at first, which would shrink
// the ship's window under a browser that only watches a crew boat. Only exact
// targets are used from here on: the session by "=name:" and a pane or window by
// its %N or @N id.
func (c *conn) start() {
	defer close(c.ready)
	sizes := c.windowSizes()
	cl, err := sessionhost.Attach(c.s.Tmux, c.s.Socket, "="+c.session+":", c.notify)
	if err != nil {
		c.fail(err)
		return
	}
	c.mu.Lock()
	c.client = cl
	gone := c.gone
	c.mu.Unlock()
	if gone { // the service shut down while this was attaching
		cl.Close()
		c.fail(errGone)
		return
	}
	if len(sizes) > 0 {
		cl.Do(sizes, nil)
	}
	go func() {
		<-cl.Done()
		c.shutdown()
	}()
}

// windowSizes are the commands that pin each window of the session to the size
// it has now. They are read before the client attaches: afterwards the windows
// may already have been resized.
func (c *conn) windowSizes() []string {
	out, err := exec.Command(c.s.Tmux, "-f", "/dev/null", "-S", c.s.Socket, "-u", "list-windows", "-t", "="+c.session+":",
		"-F", "#{window_id} #{window_width}x#{window_height}").Output()
	if err != nil {
		return nil
	}
	var cmds []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, size, ok := strings.Cut(l, " ")
		if _, _, sized := parseSize(size); ok && strings.HasPrefix(id, "@") && sized {
			cmds = append(cmds, "refresh-client -C "+id+":"+size)
		}
	}
	return cmds
}

func (c *conn) fail(err error) {
	c.mu.Lock()
	c.err = err
	c.gone = true // so a browser leaving never arms the linger on a client that isn't there
	c.mu.Unlock()
	c.s.mu.Lock()
	if c.s.conns[c.session] == c {
		delete(c.s.conns, c.session)
	}
	c.s.mu.Unlock()
}

// resolve reads the ship's own pane and window: the session's first window, not
// its current one, which moves when anyone attaches to or selects a crew window.
func (w *watch) resolve() {
	defer close(w.ready)
	got := make(chan error, 1)
	w.c.client.Do([]string{"display-message -p -t " + quoteArg("="+w.c.session+":^") + " '#{pane_id} #{window_id}'"}, func(out [][]string, errs []error) {
		var f []string
		if errs[0] == nil && len(out[0]) == 1 {
			f = strings.Fields(out[0][0])
		}
		if len(f) != 2 || !strings.HasPrefix(f[0], "%") || !strings.HasPrefix(f[1], "@") {
			got <- fmt.Errorf("no pane for %s: %v %v", w.c.session, out[0], errs[0])
			return
		}
		w.mu.Lock()
		w.pane, w.win = f[0], f[1]
		w.mu.Unlock()
		got <- nil
	})
	var err error
	select {
	case err = <-got:
	case <-time.After(10 * time.Second):
		err = errors.New("tmux did not answer")
	}
	if err != nil {
		w.err = err
		w.c.mu.Lock()
		if w.c.watches[w.key] == w {
			delete(w.c.watches, w.key)
		}
		w.c.mu.Unlock()
	}
}

// shutdown ends the session's bridge: the client detaches and every browser is closed.
func (c *conn) shutdown() {
	c.s.mu.Lock()
	if c.s.conns[c.session] == c {
		delete(c.s.conns, c.session)
	}
	c.mu.Lock()
	c.gone = true
	if c.linger != nil {
		c.linger.Stop()
	}
	ws := make([]*watch, 0, len(c.watches))
	for _, w := range c.watches {
		ws = append(ws, w)
	}
	c.watches = map[string]*watch{}
	c.mu.Unlock()
	c.s.mu.Unlock()
	if cl := c.attached(); cl != nil {
		cl.Close()
	}
	for _, w := range ws {
		w.endBrowsers()
	}
}

// attached is the control client, nil until it has attached.
func (c *conn) attached() *sessionhost.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

func (c *conn) snapshot() []*watch {
	c.mu.Lock()
	defer c.mu.Unlock()
	ws := make([]*watch, 0, len(c.watches))
	for _, w := range c.watches {
		ws = append(ws, w)
	}
	return ws
}

// endBrowsers closes every browser of the watch.
func (w *watch) endBrowsers() {
	w.mu.Lock()
	bs := make([]*browser, 0, len(w.browsers))
	for b := range w.browsers {
		bs = append(bs, b)
	}
	w.mu.Unlock()
	for _, b := range bs {
		b.end()
	}
}

// drop ends the watch of a crew boat that has gone, leaving the session's other
// watches and its client alone.
func (w *watch) drop() {
	c := w.c
	c.mu.Lock()
	if c.watches[w.key] == w {
		delete(c.watches, w.key)
	}
	c.armIdle()
	c.mu.Unlock()
	w.endBrowsers()
}

// leave removes b; the last one out of the watch removes it, and the last watch
// out starts the linger.
func (w *watch) leave(b *browser) {
	c := w.c
	c.mu.Lock()
	w.mu.Lock()
	delete(w.browsers, b)
	empty := len(w.browsers) == 0
	w.mu.Unlock()
	if empty && c.watches[w.key] == w {
		delete(c.watches, w.key)
	}
	c.armIdle()
	c.mu.Unlock()
}

// armIdle starts the linger when no watch is left. c.mu must be held.
func (c *conn) armIdle() {
	if len(c.watches) == 0 && !c.gone && c.linger == nil {
		c.linger = time.AfterFunc(c.s.Idle, c.idle)
	}
}

// idle detaches the client if nobody came back during the linger.
func (c *conn) idle() {
	c.s.mu.Lock()
	c.mu.Lock()
	if len(c.watches) > 0 || c.gone {
		c.linger = nil
		c.mu.Unlock()
		c.s.mu.Unlock()
		return
	}
	c.gone = true
	if c.s.conns[c.session] == c {
		delete(c.s.conns, c.session)
	}
	c.mu.Unlock()
	c.s.mu.Unlock()
	if cl := c.attached(); cl != nil {
		cl.Close() // each window keeps its last size
	}
}

// notify handles one control-mode notification, on the client's reader goroutine.
func (c *conn) notify(line string) {
	switch {
	case strings.HasPrefix(line, "%output "):
		pane, data, ok := strings.Cut(line[len("%output "):], " ")
		if !ok {
			return
		}
		var payload []byte
		decoded := false
		for _, w := range c.snapshot() {
			w.mu.Lock()
			mine := pane == w.pane
			var bs []*browser
			if mine {
				for b := range w.browsers {
					bs = append(bs, b)
				}
			}
			w.mu.Unlock()
			if !mine {
				continue
			}
			if !decoded {
				payload, decoded = decodeOutput(data), true
			}
			for _, b := range bs {
				b.output(payload)
			}
		}
	case strings.HasPrefix(line, "%window-close "), strings.HasPrefix(line, "%unlinked-window-close "):
		// A crew boat's window closed. This ends its bridge at once, and a boat
		// closed and relaunched under the same name inside one poll leaves the
		// ship's crew signature unchanged, so the push never says so.
		_, win, _ := strings.Cut(line, " ")
		for _, w := range c.snapshot() {
			if w.crew && w.win == strings.TrimSpace(win) {
				w.drop()
			}
		}
	case strings.HasPrefix(line, "%layout-change "):
		win, _, _ := strings.Cut(line[len("%layout-change "):], " ")
		for _, w := range c.snapshot() {
			w.mu.Lock()
			mine := win == w.win
			w.mu.Unlock()
			if mine {
				w.refreshSize(nil)
			}
		}
	}
}

// decodeOutput undoes tmux's escaping of %output: a byte below 0x20 or a
// backslash is sent as \ooo (octal), everything else as it is.
func decodeOutput(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s)+0 && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			out = append(out, (s[i+1]-'0')<<6|(s[i+2]-'0')<<3|(s[i+3]-'0'))
			i += 3
			continue
		}
		out = append(out, s[i])
	}
	return out
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

func quoteArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(s) + `"`
}

// live is true while the ship's harness is running.
func live(sh ships.Ship) bool {
	return sh.Status == ships.StatusUnderway || sh.Status == ships.StatusNeedsYou
}

// shipMsg is the state of what a browser watches, as it sees it: the ship's, or
// on a crew boat's bridge the crew boat's.
type shipMsg struct {
	Type       string  `json:"type,omitempty"`
	Status     string  `json:"status"`
	ExitCode   *int    `json:"exit_code"`
	ExitSignal *string `json:"exit_signal"`
}

func shipState(sh ships.Ship) shipMsg {
	return shipMsg{Status: sh.Status, ExitCode: sh.ExitCode, ExitSignal: sh.ExitSignal}
}

func crewState(c sessionhost.Crew) shipMsg {
	m := shipMsg{Status: c.State}
	m.ExitCode, m.ExitSignal = ships.CrewExit(c)
	return m
}

// crewBoat is the crew boat called name of the ship in session.
func (s *Service) crewBoat(session, name string) (sessionhost.Crew, bool) {
	if session == sessionhost.SetupName {
		return sessionhost.Crew{}, false
	}
	l, _ := s.Ships.Crew(session)
	for _, c := range l {
		if c.Name == name {
			return c, true
		}
	}
	return sessionhost.Crew{}, false
}

// state is the state of what the watch is on, and whether it is live: its harness
// runs and it takes input and a size. ok is false when it is gone. A crew boat
// follows its own status and never the ship's: an anchored ship keeps its live
// crew boats.
func (w *watch) state() (st shipMsg, takesInput, ok bool) {
	if !w.crew {
		sh, ok := w.c.s.get(w.c.session)
		return shipState(sh), live(sh), ok
	}
	l, _ := w.c.s.Ships.Crew(w.c.session)
	for _, c := range l {
		if c.WindowID == w.win {
			return crewState(c), c.State != sessionhost.CrewAnchored, true
		}
	}
	return shipMsg{}, false, false
}

// gone closes what a missing subject leaves behind: the whole session's bridge
// when the ship is gone, else the crew boat's watch alone.
func (w *watch) gone() {
	if _, ok := w.c.s.get(w.c.session); w.crew && ok {
		w.drop()
		return
	}
	w.c.shutdown()
}

// shipChanged sends the state to the browsers when it differs from what they
// last got, and closes them when the ship or crew boat is gone.
func (w *watch) shipChanged() {
	st, _, ok := w.state()
	if !ok {
		w.gone()
		return
	}
	w.mu.Lock()
	same := w.lastShip.Status == st.Status && ptrEq(w.lastShip.ExitCode, st.ExitCode) && ptrEq(w.lastShip.ExitSignal, st.ExitSignal)
	w.lastShip = st
	bs := w.joined()
	w.mu.Unlock()
	if same {
		return
	}
	st.Type = "ship"
	for _, b := range bs {
		b.send(st)
	}
}

func ptrEq[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// joined are the browsers that have their join payload. w.mu must be held.
func (w *watch) joined() []*browser {
	out := make([]*browser, 0, len(w.browsers))
	for b := range w.browsers {
		if b.isJoined() {
			out = append(out, b)
		}
	}
	return out
}

type sizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// setSize records the pane's size and tells the joined browsers when it changed.
func (w *watch) setSize(cols, rows int) {
	w.mu.Lock()
	if w.size == [2]int{cols, rows} {
		w.mu.Unlock()
		return
	}
	w.size = [2]int{cols, rows}
	bs := w.joined()
	w.mu.Unlock()
	for _, b := range bs {
		b.send(sizeMsg{"size", cols, rows})
	}
}

// resizeCmd sizes this watch's window alone: a whole-client size would be
// followed by every window of the session.
func (w *watch) resizeCmd(cols, rows int) string {
	return fmt.Sprintf("refresh-client -C %s:%dx%d", w.win, cols, rows)
}

// refreshSize reads the pane's size, after the commands in before.
func (w *watch) refreshSize(before []string) {
	w.mu.Lock()
	pane := w.pane
	w.mu.Unlock()
	cmds := append(before, "display-message -p -t "+pane+" '#{window_width}x#{window_height}'")
	w.c.client.Do(cmds, func(out [][]string, errs []error) {
		last := len(cmds) - 1
		if errs[last] != nil || len(out[last]) != 1 {
			return
		}
		if c, r, ok := parseSize(out[last][0]); ok {
			w.setSize(c, r)
		}
	})
}

func parseSize(s string) (cols, rows int, ok bool) {
	a, b, found := strings.Cut(s, "x")
	cols, e1 := strconv.Atoi(a)
	rows, e2 := strconv.Atoi(b)
	return cols, rows, found && e1 == nil && e2 == nil && cols > 0 && rows > 0
}

// makeActive lets b size the window at the size it last reported.
func (w *watch) makeActive(b *browser) {
	cols, rows, hidden := b.dims()
	_, lv, ok := w.state()
	if hidden || cols == 0 || !ok || !lv {
		return
	}
	w.refreshSize([]string{w.resizeCmd(cols, rows)})
}

// The join payload. S is the visible screen, Scrollback the history above it.
type joinMsg struct {
	Type       string `json:"type"`
	Screen     string `json:"screen"`
	Scrollback string `json:"scrollback"`
	Cursor     struct {
		X       int  `json:"x"`
		Y       int  `json:"y"`
		Visible bool `json:"visible"`
	} `json:"cursor"`
	Modes struct {
		Alternate         bool `json:"alternate"`
		ApplicationCursor bool `json:"application_cursor"`
		BracketedPaste    bool `json:"bracketed_paste"`
		Mouse             bool `json:"mouse"`
	} `json:"modes"`
	Size struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"size"`
	Ship shipMsg `json:"ship"`
}

const modeFormat = "#{cursor_x}|#{cursor_y}|#{cursor_flag}|#{keypad_cursor_flag}|#{alternate_on}|#{bracket_paste_flag}|#{mouse_any_flag}|#{window_width}|#{window_height}|#{history_size}"

// join sends b the screen and scrollback with colours, the cursor and the
// modes, and from the same point in tmux's stream on, the live output. When
// size is set b's size is applied first, and b becomes the active browser.
func (w *watch) join(b *browser, resize bool) {
	w.mu.Lock()
	pane := w.pane
	w.mu.Unlock()
	cols, rows, _ := b.dims()
	st, lv, ok := w.state()
	if !ok {
		w.gone()
		return
	}
	var cmds []string
	if resize && cols > 0 && lv {
		cmds = append(cmds, w.resizeCmd(cols, rows))
	}
	cmds = append(cmds,
		"display-message -p -t "+pane+" '"+modeFormat+"'",
		"capture-pane -p -e -t "+pane,
		fmt.Sprintf("capture-pane -p -e -S -%d -E -1 -t %s", scrollbackLines, pane))
	skip := len(cmds) - 3
	w.c.client.Do(cmds, func(out [][]string, errs []error) {
		var j joinMsg
		f := strings.Split(strings.Join(out[skip], ""), "|")
		if errs[skip] != nil || errs[skip+1] != nil || len(f) != 10 {
			b.kill()
			return
		}
		n := func(i int) int { v, _ := strconv.Atoi(f[i]); return v }
		j.Type = "join"
		j.Cursor.X, j.Cursor.Y, j.Cursor.Visible = n(0), n(1), f[2] == "1"
		j.Modes.ApplicationCursor, j.Modes.Alternate, j.Modes.BracketedPaste, j.Modes.Mouse = f[3] == "1", f[4] == "1", f[5] == "1", f[6] == "1"
		j.Size.Cols, j.Size.Rows = n(7), n(8)
		history := n(9)
		j.Screen = strings.Join(out[skip+1], "\n")
		// A pane in the alternate screen has no scrollback of its own: what tmux
		// returns for it would be the normal screen's history under a screen the
		// browser draws in its alternate buffer. With no history tmux clamps the
		// range to the screen's first line, which would be sent twice.
		if !j.Modes.Alternate && history > 0 && errs[skip+2] == nil {
			j.Scrollback = strings.Join(out[skip+2], "\n")
		}
		j.Ship = st
		w.mu.Lock()
		w.lastShip = j.Ship
		w.mu.Unlock()
		b.joinWith(j)
		w.setSize(j.Size.Cols, j.Size.Rows)
	})
}

// inputSeen records the input with the ships service, which clears needs you
// on the ship's pane, or on the crew boat's. It returns the state to refuse
// with, and ErrExited or ErrNotFound as the service does.
func (w *watch) inputSeen() (reason string, err error) {
	if !w.crew {
		sh, err := w.c.s.inputSeen(w.c.session)
		return sh.Status, err
	}
	c, err := w.c.s.Ships.CrewInputSeen(w.c.session, w.win)
	return c.State, err
}

// input sends keystrokes to the pane. The needs-you state is cleared first so
// anyone who reads the ship after the keys arrived sees it cleared.
func (w *watch) input(b *browser, data string) {
	if data == "" {
		return
	}
	reason, err := w.inputSeen()
	switch {
	case errors.Is(err, ships.ErrExited):
		b.send(map[string]string{"type": "refused", "reason": reason})
		return
	case errors.Is(err, ships.ErrNotFound):
		w.gone()
		return
	case err != nil:
		return
	}
	w.mu.Lock()
	pane := w.pane
	w.mu.Unlock()
	const chunk = 256
	var cmds []string
	raw := []byte(data)
	for len(raw) > 0 {
		n := min(chunk, len(raw))
		var sb strings.Builder
		sb.WriteString("send-keys -H -t " + pane)
		for _, c := range raw[:n] {
			fmt.Fprintf(&sb, " %02x", c)
		}
		cmds = append(cmds, sb.String())
		raw = raw[n:]
	}
	w.c.client.Do(cmds, nil)
}
