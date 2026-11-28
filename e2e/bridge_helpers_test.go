package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

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
	if err := b.c.Write(testCtx(b.t), websocket.MessageText, data); err != nil {
		failRequest(b.t, "sending "+string(data), err)
	}
}

func (b *bridgeClient) input(data string) {
	b.t.Helper()
	b.send(map[string]any{"type": "input", "data": data})
}
func (b *bridgeClient) active() { b.t.Helper(); b.send(map[string]any{"type": "active"}) }
func (b *bridgeClient) visible(v bool) {
	b.t.Helper()
	b.send(map[string]any{"type": "visible", "visible": v})
}

// sync returns once the server has handled every message sent so far: it reads a connection's messages one
// at a time, and answers a ping only when it reads the ping.
func (b *bridgeClient) sync() {
	b.t.Helper()
	if err := b.c.Ping(testCtx(b.t)); err != nil {
		failRequest(b.t, "pinging the bridge", err)
	}
}
func (b *bridgeClient) resize(cols, rows int) {
	b.t.Helper()
	b.send(map[string]any{"type": "resize", "cols": cols, "rows": rows})
}

func (b *bridgeClient) close() { _ = b.c.Close(websocket.StatusNormalClosure, "") }

func (b *bridgeClient) mark() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.frames)
}

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

func (b *bridgeClient) waitFor(what string, pred func() bool) {
	b.t.Helper()
	b.waitSeen(what, func() (bool, string) { return pred(), "" })
}

// waitSeen is waitFor with a check that says what it saw, for the failure.
func (b *bridgeClient) waitSeen(what string, check func() (bool, string)) {
	b.t.Helper()
	eventually(b.t, "the bridge delivering "+what, func() (bool, string) {
		ok, seen := check()
		if ok {
			return true, ""
		}
		b.mu.Lock()
		err := b.err
		n := len(b.frames)
		b.mu.Unlock()
		if seen != "" {
			seen += "; "
		}
		return false, fmt.Sprintf("%s%d frames received, connection error: %v", seen, n, err)
	})
}

func (b *bridgeClient) waitOutput(mark int, want string) {
	b.t.Helper()
	b.waitSeen("output containing "+quote(want), func() (bool, string) {
		out := b.output(mark)
		return strings.Contains(out, want), "got " + quote(out)
	})
}

// waitJoin waits for the nth `join` message (1-based) and decodes it.
func (b *bridgeClient) waitJoin(n int) joinMsg {
	b.t.Helper()
	b.waitFor("join message number "+itoa(n), func() bool { return len(b.texts(0, "join")) >= n })
	var j joinMsg
	if err := json.Unmarshal(b.texts(0, "join")[n-1], &j); err != nil {
		b.t.Fatalf("join message is not the documented shape: %v", err)
	}
	return j
}

func (b *bridgeClient) waitShip(mark int, what string, pred func(shipState) bool) shipState {
	b.t.Helper()
	var got shipState
	b.waitFor("a `ship` message with "+what, func() bool {
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

func (b *bridgeClient) waitSize(mark, cols, rows int) {
	b.t.Helper()
	b.waitFor("a `size` message "+itoa(cols)+"x"+itoa(rows), func() bool {
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
func itoa(n int) string     { b, _ := json.Marshal(n); return string(b) }

// bridgeDial opens /ws/bridge/<session> on base (http://host:port). On refusal
// it returns the handshake response with a nil client.
func bridgeDial(t *testing.T, base, session, query string, hdr http.Header, host string) (*bridgeClient, *http.Response, error) {
	t.Helper()
	return bridgeDialAt(t, base, "/ws", session, query, hdr, host)
}

// bridgeDialAt is bridgeDial under another socket prefix: "/ws" or "/ws/v1".
func bridgeDialAt(t *testing.T, base, prefix, session, query string, hdr http.Header, host string) (*bridgeClient, *http.Response, error) {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme = "ws"
	u.Path = prefix + "/bridge/" + session
	u.RawQuery = query
	ctx := testCtx(t)
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

// dialBridgeAt connects to a ship's bridge socket under the socket prefix
// ("/ws" or "/ws/v1") without joining.
func (w *world) dialBridgeAt(t *testing.T, prefix, session string) *bridgeClient {
	t.Helper()
	b, resp, err := bridgeDialAt(t, w.in.LocalURL, prefix, session, "", nil, "")
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("GET %s/bridge/%s was refused (status %d): %v", prefix, session, status, err)
	}
	return b
}

func (b *bridgeClient) waitClosed() (websocket.StatusCode, string) {
	b.t.Helper()
	b.waitFor("the server closing the socket", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.err != nil
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	var ce websocket.CloseError
	if errors.As(b.err, &ce) {
		return ce.Code, ce.Reason
	}
	return -1, b.err.Error()
}

// joinBridge dials, sends `join` with the browser's size and waits for the
// join payload.
func (w *world) joinBridge(t *testing.T, session string, cols, rows int) (*bridgeClient, joinMsg) {
	t.Helper()
	b := w.dialBridge(t, session)
	b.send(map[string]any{"type": "join", "cols": cols, "rows": rows})
	return b, b.waitJoin(1)
}

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

func (w *world) target(session string) string { return "=" + session + ":" }

// paneSize is #{window_width}x#{window_height}.
func (w *world) paneSize(session string) string {
	w.t.Helper()
	return w.mustTmux("display-message", "-p", "-t", w.target(session), "#{window_width}x#{window_height}")
}

func (w *world) waitPaneSize(t *testing.T, session, want string) {
	t.Helper()
	eventually(t, "the pane of "+session+" becoming "+want, func() (bool, string) {
		got, err := w.tmux("display-message", "-p", "-t", w.target(session), "#{window_width}x#{window_height}")
		return err == nil && got == want, "it is " + got
	})
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

func (w *world) stdinOf(session string) string {
	data, _ := os.ReadFile(filepath.Join(w.rec, session, "stdin"))
	return string(data)
}

// waitStdin waits until the fake has recorded exactly want.
func (w *world) waitStdin(t *testing.T, session, want string) {
	t.Helper()
	eventually(t, "the fake harness of "+session+" recording stdin "+quote(want), func() (bool, string) {
		got := w.stdinOf(session)
		return got == want, "it has " + quote(got)
	})
}

// release lets a fake that is blocked in a `wait <name>` cue go on.
func (w *world) release(session, name string) {
	w.t.Helper()
	w.waitFile(session, "recorded") // the record directory is the harness's to make
	if err := os.WriteFile(filepath.Join(w.rec, session, name), nil, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// startLAN starts Admiral on 0.0.0.0 with the world's environment and pairs a device over the LAN.
func (w *world) startLAN(t *testing.T) *fleetInst {
	t.Helper()
	if _, err := os.Stat(configPath(w.home)); err != nil {
		writeConfig(t, w.home, w.config())
	}
	w.in = launchAdmiral(t, w.home, "0.0.0.0:0", w.baseEnv())
	w.in.setLANIP()
	requireLAN(t, w.in.instance)
	return w.in
}

func (in *fleetInst) setLANIP() {
	if m := lanLine.FindStringSubmatch(in.LANLine); m != nil {
		in.LANIP = m[1]
	}
}

func readFile(p string) (string, error) {
	data, err := os.ReadFile(p)
	return string(data), err
}
