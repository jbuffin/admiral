// Package bridge is the terminal a browser opens on a ship (ADR 0011). One
// tmux control client per watched ship feeds every browser watching it: a
// joining browser gets the screen and scrollback, then the live %output stream;
// keystrokes go back with send-keys; the latest active browser sizes the pane.
//
// Wire protocol: GET /ws/bridge/<the ship's tmux session name>, a WebSocket.
// The first message from the browser is {"type":"join","cols":C,"rows":R}.
// The messages are described in docs/bridge-protocol.md.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// DefaultIdle is how long a ship's control client stays attached after the
// last browser leaves. ADMIRAL_BRIDGE_IDLE overrides it (a Go duration).
const DefaultIdle = 30 * time.Second

// scrollbackLines is how much history a joining browser gets.
const scrollbackLines = 2000

// Ships is what the bridge needs of the ships service.
type Ships interface {
	Get(session string) (ships.Ship, bool)
	InputSeen(session string) (ships.Ship, error)
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
	watches map[string]*watch
	closing bool
}

// New prepares the service. tmux is the binary; sock is Admiral's socket.
func New(tmux, sock string, sh Ships) *Service {
	idle := DefaultIdle
	if d, err := time.ParseDuration(os.Getenv("ADMIRAL_BRIDGE_IDLE")); err == nil && d > 0 {
		idle = d
	}
	return &Service{Tmux: tmux, Socket: sock, Ships: sh, Idle: idle, watches: map[string]*watch{}}
}

// Routes registers the bridge endpoint.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ws/bridge/{session}", s.serve)
	mux.HandleFunc("GET /api/setup", s.serveSetupStatus)
	mux.HandleFunc("POST /api/setup", s.serveSetupStart)
}

// Connected does nothing: the bridge sends nothing on the main socket.
func (s *Service) Connected(*server.Client) {}

// Run keeps the service until ctx ends, then detaches every control client.
func (s *Service) Run(ctx context.Context) {
	<-ctx.Done()
	s.mu.Lock()
	s.closing = true
	all := make([]*watch, 0, len(s.watches))
	for _, w := range s.watches {
		all = append(all, w)
	}
	s.mu.Unlock()
	for _, w := range all {
		w.shutdown()
	}
	for _, w := range all {
		if c := w.attached(); c != nil {
			select {
			case <-c.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

// ShipsChanged tells every open bridge to look at its ship again. It never blocks.
func (s *Service) ShipsChanged() {
	s.mu.Lock()
	all := make([]*watch, 0, len(s.watches))
	for _, w := range s.watches {
		all = append(all, w)
	}
	s.mu.Unlock()
	for _, w := range all {
		w.shipChanged()
	}
}

// watch is the control client of one ship and the browsers sharing it.
type watch struct {
	s       *Service
	session string
	target  string // =session:, exact

	ready  chan struct{} // closed once the client has attached and the pane is known
	client *sessionhost.Client
	pane   string
	err    error

	mu       sync.Mutex
	browsers map[*browser]struct{}
	size     [2]int
	lastShip shipMsg
	linger   *time.Timer
	gone     bool
}

var errGone = errors.New("the ship's bridge has closed")

// enter adds b to the ship's watch, starting the control client when b is the
// first. Browsers are added before the attach finishes so the linger timer
// can't detach under them.
func (s *Service) enter(session string, b *browser) (*watch, error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, errGone
	}
	w := s.watches[session]
	fresh := w == nil
	if fresh {
		w = &watch{s: s, session: session, target: "=" + session + ":", ready: make(chan struct{}),
			browsers: map[*browser]struct{}{}}
		s.watches[session] = w
	}
	w.mu.Lock()
	w.browsers[b] = struct{}{}
	if w.linger != nil {
		w.linger.Stop()
		w.linger = nil
	}
	w.mu.Unlock()
	s.mu.Unlock()

	if fresh {
		w.start()
	}
	<-w.ready
	if w.err != nil {
		w.leave(b)
		return nil, w.err
	}
	return w, nil
}

// start attaches the control client and reads the pane id. Only exact targets
// are used from here on: the session by "=name:" and the pane by its %N id.
func (w *watch) start() {
	defer close(w.ready)
	c, err := sessionhost.Attach(w.s.Tmux, w.s.Socket, w.target, w.notify)
	if err != nil {
		w.fail(err)
		return
	}
	w.mu.Lock()
	w.client = c
	gone := w.gone
	w.mu.Unlock()
	if gone { // the service shut down while this was attaching
		c.Close()
		w.fail(errGone)
		return
	}
	got := make(chan error, 1)
	c.Do([]string{"display-message -p -t " + quoteArg(w.target) + " '#{pane_id}'"}, func(out [][]string, errs []error) {
		if errs[0] != nil || len(out[0]) != 1 || !strings.HasPrefix(out[0][0], "%") {
			got <- fmt.Errorf("no pane for %s: %v %v", w.session, out[0], errs[0])
			return
		}
		w.mu.Lock()
		w.pane = out[0][0]
		w.mu.Unlock()
		got <- nil
	})
	select {
	case err = <-got:
	case <-time.After(10 * time.Second):
		err = errors.New("tmux did not answer")
	}
	if err != nil {
		c.Close()
		w.fail(err)
		return
	}
	go func() {
		<-c.Done()
		w.shutdown()
	}()
}

func (w *watch) fail(err error) {
	w.mu.Lock()
	w.err = err
	w.gone = true // so a browser leaving never arms the linger on a client that isn't there
	w.mu.Unlock()
	w.s.mu.Lock()
	if w.s.watches[w.session] == w {
		delete(w.s.watches, w.session)
	}
	w.s.mu.Unlock()
}

// shutdown ends the watch: the client detaches and every browser is closed.
func (w *watch) shutdown() {
	w.s.mu.Lock()
	if w.s.watches[w.session] == w {
		delete(w.s.watches, w.session)
	}
	w.mu.Lock()
	w.gone = true
	if w.linger != nil {
		w.linger.Stop()
	}
	bs := make([]*browser, 0, len(w.browsers))
	for b := range w.browsers {
		bs = append(bs, b)
	}
	w.mu.Unlock()
	w.s.mu.Unlock()
	if c := w.attached(); c != nil {
		c.Close()
	}
	for _, b := range bs {
		b.end()
	}
}

// attached is the control client, nil until it has attached.
func (w *watch) attached() *sessionhost.Client {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.client
}

// leave removes b; the last one out starts the linger.
func (w *watch) leave(b *browser) {
	w.mu.Lock()
	delete(w.browsers, b)
	if len(w.browsers) == 0 && !w.gone && w.linger == nil {
		w.linger = time.AfterFunc(w.s.Idle, w.idle)
	}
	w.mu.Unlock()
}

// idle detaches the client if nobody came back during the linger.
func (w *watch) idle() {
	w.s.mu.Lock()
	w.mu.Lock()
	if len(w.browsers) > 0 || w.gone {
		w.mu.Unlock()
		w.s.mu.Unlock()
		return
	}
	w.gone = true
	if w.s.watches[w.session] == w {
		delete(w.s.watches, w.session)
	}
	w.mu.Unlock()
	w.s.mu.Unlock()
	if c := w.attached(); c != nil {
		c.Close() // the pane keeps its last size
	}
}

// notify handles one control-mode notification, on the client's reader goroutine.
func (w *watch) notify(line string) {
	switch {
	case strings.HasPrefix(line, "%output "):
		pane, data, ok := strings.Cut(line[len("%output "):], " ")
		w.mu.Lock()
		mine := ok && pane == w.pane
		var bs []*browser
		if mine {
			for b := range w.browsers {
				bs = append(bs, b)
			}
		}
		w.mu.Unlock()
		if mine {
			p := decodeOutput(data)
			for _, b := range bs {
				b.output(p)
			}
		}
	case strings.HasPrefix(line, "%layout-change "):
		w.refreshSize(nil)
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

// shipMsg is the ship's state as a browser sees it.
type shipMsg struct {
	Type       string  `json:"type,omitempty"`
	Status     string  `json:"status"`
	ExitCode   *int    `json:"exit_code"`
	ExitSignal *string `json:"exit_signal"`
}

func shipState(sh ships.Ship) shipMsg {
	return shipMsg{Status: sh.Status, ExitCode: sh.ExitCode, ExitSignal: sh.ExitSignal}
}

// shipChanged sends the ship's state to the browsers when it differs from what
// they last got, and closes them when the ship is gone.
func (w *watch) shipChanged() {
	sh, ok := w.s.get(w.session)
	if !ok {
		w.shutdown()
		return
	}
	st := shipState(sh)
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

// refreshSize reads the pane's size, after the commands in before.
func (w *watch) refreshSize(before []string) {
	w.mu.Lock()
	pane := w.pane
	w.mu.Unlock()
	cmds := append(before, "display-message -p -t "+pane+" '#{window_width}x#{window_height}'")
	w.client.Do(cmds, func(out [][]string, errs []error) {
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

// makeActive lets b size the pane at the size it last reported.
func (w *watch) makeActive(b *browser) {
	cols, rows, hidden := b.dims()
	sh, ok := w.s.get(w.session)
	if hidden || cols == 0 || !ok || !live(sh) {
		return
	}
	w.refreshSize([]string{fmt.Sprintf("refresh-client -C %dx%d", cols, rows)})
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
	sh, ok := w.s.get(w.session)
	if !ok {
		w.shutdown()
		return
	}
	var cmds []string
	if resize && cols > 0 && live(sh) {
		cmds = append(cmds, fmt.Sprintf("refresh-client -C %dx%d", cols, rows))
	}
	cmds = append(cmds,
		"display-message -p -t "+pane+" '"+modeFormat+"'",
		"capture-pane -p -e -t "+pane,
		fmt.Sprintf("capture-pane -p -e -S -%d -E -1 -t %s", scrollbackLines, pane))
	skip := len(cmds) - 3
	w.client.Do(cmds, func(out [][]string, errs []error) {
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
		j.Ship = shipState(sh)
		w.mu.Lock()
		w.lastShip = j.Ship
		w.mu.Unlock()
		b.joinWith(j)
		w.setSize(j.Size.Cols, j.Size.Rows)
	})
}

// input sends keystrokes to the pane. The needs-you state is cleared first so
// anyone who reads the ship after the keys arrived sees it cleared.
func (w *watch) input(b *browser, data string) {
	if data == "" {
		return
	}
	sh, err := w.s.inputSeen(w.session)
	switch {
	case errors.Is(err, ships.ErrExited):
		b.send(map[string]string{"type": "refused", "reason": sh.Status})
		return
	case errors.Is(err, ships.ErrNotFound):
		w.shutdown()
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
	w.client.Do(cmds, nil)
}
