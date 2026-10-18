package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Helpers for the bridge scenarios (u11). The wire shapes they assume are
// documented at the top of bridge_test.go.

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func stripSGR(s string) string { return sgr.ReplaceAllString(s, "") }

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
	Ship shipState `json:"ship"`
}

// shipState is the `ship` object of the join payload and the body of a `ship`
// message. The pointers keep null apart from missing.
type shipState struct {
	Status     string  `json:"status"`
	ExitCode   *int    `json:"exit_code"`
	ExitSignal *string `json:"exit_signal"`
}

type bridgeFrame struct {
	binary bool
	data   []byte
}

// bridgeClient is one browser on the bridge. A goroutine keeps every frame it
// receives, in order, so a test can look at what arrived since a mark.
type bridgeClient struct {
	t      *testing.T
	c      *websocket.Conn
	mu     sync.Mutex
	frames []bridgeFrame
	err    error
}

func (b *bridgeClient) pump() {
	for {
		typ, data, err := b.c.Read(context.Background())
		b.mu.Lock()
		if err != nil {
			b.err = err
			b.mu.Unlock()
			return
		}
		b.frames = append(b.frames, bridgeFrame{binary: typ == websocket.MessageBinary, data: data})
		b.mu.Unlock()
	}
}

func (b *bridgeClient) send(v any) {
	b.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		b.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.c.Write(ctx, websocket.MessageText, data); err != nil {
		b.t.Fatalf("sending %s: %v", data, err)
	}
}

func (b *bridgeClient) input(data string) { b.t.Helper(); b.send(map[string]any{"type": "input", "data": data}) }
func (b *bridgeClient) active()           { b.t.Helper(); b.send(map[string]any{"type": "active"}) }
func (b *bridgeClient) visible(v bool) {
	b.t.Helper()
	b.send(map[string]any{"type": "visible", "visible": v})
}
func (b *bridgeClient) resize(cols, rows int) {
	b.t.Helper()
	b.send(map[string]any{"type": "resize", "cols": cols, "rows": rows})
}

func (b *bridgeClient) close() { _ = b.c.Close(websocket.StatusNormalClosure, "") }

// mark is a position in the frames received so far.
func (b *bridgeClient) mark() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.frames)
}

// output is the binary frames received after the mark, joined.
func (b *bridgeClient) output(mark int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb strings.Builder
	for _, f := range b.frames[mark:] {
		if f.binary {
			sb.Write(f.data)
		}
	}
	return sb.String()
}

// texts are the JSON messages of one type received after the mark.
func (b *bridgeClient) texts(mark int, typ string) []json.RawMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []json.RawMessage
	for _, f := range b.frames[mark:] {
		if f.binary {
			continue
		}
		var h struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(f.data, &h) == nil && h.Type == typ {
			out = append(out, json.RawMessage(append([]byte(nil), f.data...)))
		}
	}
	return out
}

func (b *bridgeClient) waitFor(d time.Duration, what string, pred func() bool) {
	b.t.Helper()
	deadline := time.Now().Add(d)
	for !pred() {
		if time.Now().After(deadline) {
			b.mu.Lock()
			err := b.err
			n := len(b.frames)
			b.mu.Unlock()
			b.t.Fatalf("within %s the bridge never delivered %s (%d frames received, connection error: %v)", d, what, n, err)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// waitOutput waits until the bytes streamed after the mark contain want.
func (b *bridgeClient) waitOutput(d time.Duration, mark int, want string) {
	b.t.Helper()
	b.waitFor(d, "output containing "+quote(want)+" (got "+quote(b.output(mark))+")", func() bool {
		return strings.Contains(b.output(mark), want)
	})
}

// waitJoin waits for the nth `join` message (1-based) and decodes it.
func (b *bridgeClient) waitJoin(d time.Duration, n int) joinMsg {
	b.t.Helper()
	b.waitFor(d, "join message number "+itoa(n), func() bool { return len(b.texts(0, "join")) >= n })
	var j joinMsg
	if err := json.Unmarshal(b.texts(0, "join")[n-1], &j); err != nil {
		b.t.Fatalf("join message is not the documented shape: %v", err)
	}
	return j
}

// waitShip waits for a `ship` message after the mark whose state satisfies pred.
func (b *bridgeClient) waitShip(d time.Duration, mark int, what string, pred func(shipState) bool) shipState {
	b.t.Helper()
	var got shipState
	b.waitFor(d, "a `ship` message with "+what, func() bool {
		for _, raw := range b.texts(mark, "ship") {
			var s struct {
				shipState
			}
			if json.Unmarshal(raw, &s) == nil && pred(s.shipState) {
				got = s.shipState
				return true
			}
		}
		return false
	})
	return got
}

// waitSize waits for a `size` message after the mark with the given grid.
func (b *bridgeClient) waitSize(d time.Duration, mark, cols, rows int) {
	b.t.Helper()
	b.waitFor(d, "a `size` message "+itoa(cols)+"x"+itoa(rows), func() bool {
		for _, raw := range b.texts(mark, "size") {
			var s struct{ Cols, Rows int }
			if json.Unmarshal(raw, &s) == nil && s.Cols == cols && s.Rows == rows {
				return true
			}
		}
		return false
	})
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func itoa(n int) string      { b, _ := json.Marshal(n); return string(b) }

// bridgeDial opens /ws/bridge/<session> on base (http://host:port). On refusal
// it returns the handshake response with a nil client.
func bridgeDial(t *testing.T, base, session, query string, hdr http.Header, host string) (*bridgeClient, *http.Response, error) {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme = "ws"
	u.Path = "/ws/bridge/" + session
	u.RawQuery = query
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: noProxy()}, HTTPHeader: hdr, Host: host})
	if err != nil {
		return nil, resp, err
	}
	c.SetReadLimit(16 << 20) // a join carries up to 2,000 lines of scrollback
	b := &bridgeClient{t: t, c: c}
	t.Cleanup(func() { _ = c.CloseNow() })
	go b.pump()
	return b, resp, nil
}

// dialBridge connects to a ship's bridge on the world's instance, failing the
// test when the endpoint does not accept it.
func (w *world) dialBridge(t *testing.T, session string) *bridgeClient {
	t.Helper()
	b, resp, err := bridgeDial(t, w.in.LocalURL, session, "", nil, "")
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("GET /ws/bridge/%s was refused (status %d, no bridge endpoint yet?): %v", session, status, err)
	}
	return b
}

// joinBridge dials, sends `join` with the browser's size and waits for the
// join payload.
func (w *world) joinBridge(t *testing.T, session string, cols, rows int) (*bridgeClient, joinMsg) {
	t.Helper()
	b := w.dialBridge(t, session)
	b.send(map[string]any{"type": "join", "cols": cols, "rows": rows})
	return b, b.waitJoin(5*time.Second, 1)
}

// expectRefused requires the handshake to fail with the status.
func expectBridgeRefused(t *testing.T, base, session, query string, hdr http.Header, host string, want int) {
	t.Helper()
	b, resp, err := bridgeDial(t, base, session, query, hdr, host)
	if err == nil {
		b.close()
		t.Fatalf("/ws/bridge/%s should be refused with %d, but connected", session, want)
	}
	if resp == nil || resp.StatusCode != want {
		t.Fatalf("/ws/bridge/%s refusal = %v (err %v), want status %d", session, resp, err, want)
	}
}

// ---- tmux and the fake harness, as the test sees the pane ----

func (w *world) target(session string) string { return "=" + session + ":" }

// paneSize is #{window_width}x#{window_height}.
func (w *world) paneSize(session string) string {
	w.t.Helper()
	return w.mustTmux("display-message", "-p", "-t", w.target(session), "#{window_width}x#{window_height}")
}

func (w *world) waitPaneSize(t *testing.T, session, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := w.tmux("display-message", "-p", "-t", w.target(session), "#{window_width}x#{window_height}")
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 3s the pane of %s never became %s (it is %s)", session, want, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// controlClients are the tmux clients in control mode attached to the session,
// as "<pid>" strings.
func (w *world) controlClients(session string) []string {
	w.t.Helper()
	out, err := w.tmux("list-clients", "-F", "#{client_session} #{client_control_mode} #{client_pid}")
	if err != nil {
		return nil
	}
	var pids []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 3 && f[0] == session && f[1] == "1" {
			pids = append(pids, f[2])
		}
	}
	return pids
}

// paneWrite writes to the pane's terminal, which is output from the program in
// it as far as tmux and the bridge can tell.
func (w *world) paneWrite(session, data string) {
	w.t.Helper()
	tty := w.mustTmux("display-message", "-p", "-t", w.target(session), "#{pane_tty}")
	f, err := os.OpenFile(tty, os.O_WRONLY, 0)
	if err != nil {
		w.t.Fatalf("opening the pane's terminal %s: %v", tty, err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		w.t.Fatalf("writing to the pane's terminal: %v", err)
	}
}

// stdinOf is what the fake harness has read from its terminal so far.
func (w *world) stdinOf(session string) string {
	data, _ := os.ReadFile(filepath.Join(w.rec, session, "stdin"))
	return string(data)
}

// waitStdin waits until the fake has recorded exactly want.
func (w *world) waitStdin(t *testing.T, session, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for w.stdinOf(session) != want {
		if time.Now().After(deadline) {
			t.Fatalf("within 3s the fake harness of %s never recorded stdin %s (it has %s)", session, quote(want), quote(w.stdinOf(session)))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// release lets a fake that is blocked in a `wait <name>` cue go on.
func (w *world) release(session, name string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.rec, session, name), nil, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// startLAN starts Admiral on 0.0.0.0 with the world's environment and reads
// the token from the printed second line. It skips (fails on CI) when the
// Mac's address does not answer.
func (w *world) startLAN(t *testing.T) *fleetInst {
	t.Helper()
	if _, err := os.Stat(configPath(w.home)); err != nil {
		writeConfig(t, w.home, w.config())
	}
	w.in = launchAdmiral(t, w.home, "0.0.0.0:"+freePort(t), w.baseEnv())
	if m := lanLine.FindStringSubmatch(w.in.LANLine); m != nil {
		w.in.LANIP, w.in.Token = m[1], m[3]
	}
	requireLAN(t, w.in.instance)
	return w.in
}

func readFile(p string) (string, error) {
	data, err := os.ReadFile(p)
	return string(data), err
}
