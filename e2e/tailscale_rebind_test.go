package e2e

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Turning Tailscale mode on or off moves Admiral's listener without a restart and tells the browsers it leaves behind.

func lanIPv4(t *testing.T) string {
	t.Helper()
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	lanUnavailable(t, "no LAN IPv4 address")
	return ""
}

// startDerived runs Admiral with no --listen, so its address follows the mode, on this rig's port.
func (r *tsRig) startDerived(mode bool) *tsProc {
	t := r.t
	t.Helper()
	lan := lanIPv4(t)
	r.writeConfig(mode)
	fakeDir, err := filepath.Abs(filepath.Join("testdata", "faketailscale"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath(t))
	cmd.Env = append(hermeticEnv(t), "ADMIRAL_HOME="+r.home, "ADMIRAL_PORT="+r.port, "FAKETS_DIR="+r.dir, "PATH="+fakeDir+":"+os.Getenv("PATH"))
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	in := &instance{Home: r.home, Port: r.port, LocalURL: "http://127.0.0.1:" + r.port, LANIP: lan, cmd: cmd, done: make(chan struct{}), stderrBuf: out}
	go func() { _ = cmd.Wait(); close(in.done) }()
	t.Cleanup(func() { killTmux(filepath.Join(r.home, "tmux.sock")) })
	t.Cleanup(in.Stop)
	p := &tsProc{instance: in, out: out}
	waitUntil(t, "Admiral answers on loopback", func() bool { return p.answers(p.LocalURL) })
	return p
}

func (p *tsProc) answers(base string) bool {
	c := &http.Client{Transport: noProxy(), Timeout: 2 * time.Second}
	resp, err := c.Get(base + "/api/remote")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(tsStartBound)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func dialShips(t *testing.T, base string, header http.Header) *websocket.Conn {
	t.Helper()
	conn, resp, err := wsDialAt(t, base, "/ws/v1", "", header, "")
	if err != nil {
		t.Fatalf("dial %s: %v (%v)", base, err, resp)
	}
	return conn
}

// awaitMove reads until the move message arrives and returns its url; the socket must still be open when it does.
func awaitMove(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("the socket ended without a move message: %v", err)
		}
		var m struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		}
		if json.Unmarshal(data, &m) == nil && m.Type == "move" {
			return m.URL
		}
	}
}

func (r *tsRig) lanBearer(t *testing.T, p *tsProc) http.Header {
	t.Helper()
	return http.Header{"Authorization": {"Bearer " + p.pair(t)}}
}

func (p *tsProc) lanURL() string { return "http://" + net.JoinHostPort(p.LANIP, p.Port) }

func (p *tsProc) shipsAnswer(t *testing.T) bool {
	code, _ := p.call(t, http.MethodGet, "/api/ships")
	return code == http.StatusOK
}

func pidOf(p *tsProc) int { return p.cmd.Process.Pid }

// Mode on takes Admiral off the LAN with no restart: the LAN browser is sent to the tailnet URL before its socket
// closes, loopback keeps answering with its socket open, and the ships stay listed.
func TestModeOnMovesAdmiralOffTheLAN(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.writeServe(r.serve(443, false))
	p := r.startDerived(false)
	pid := pidOf(p)
	if !p.answers(p.lanURL()) {
		lanUnavailable(t, "no answer from %s", p.lanURL())
	}
	lan := dialShips(t, p.lanURL(), r.lanBearer(t, p))
	local := dialShips(t, p.LocalURL, nil)

	r.writeConfig(true)

	if got, want := awaitMove(t, lan), "https://"+tsName+"/"; got != want {
		t.Errorf("the LAN browser was sent to %q, want %q", got, want)
	}
	waitUntil(t, "the LAN address stops answering", func() bool { return !p.answers(p.lanURL()) })
	if !p.answers(p.LocalURL) || !p.shipsAnswer(t) {
		t.Error("loopback stopped answering or lost its ships")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := local.Ping(ctx); err != nil {
		t.Errorf("the loopback socket did not stay open: %v", err)
	}
	if p.exited() || pidOf(p) != pid {
		t.Error("Admiral restarted to move")
	}
}

// Mode off sends the tailnet browser to the LAN address before its socket is cut, then Admiral answers on the LAN again.
func TestModeOffMovesTailnetBrowsersToTheLAN(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	p := r.startDerived(true)
	if !p.otherDevicesBound(t) {
		return
	}
	token := p.pairOverTailnet(t, tsLogin)
	tail := dialShips(t, p.base(), http.Header{"Authorization": {"Bearer " + token}, "Tailscale-User-Login": {tsLogin}, "X-Forwarded-For": {"100.64.0.9"}})
	pid := pidOf(p)

	if code, body := p.post(t, "/api/remote/off", `{"confirm":true}`); code != http.StatusOK {
		t.Fatalf("Turn off: %d %s", code, body)
	}

	if got, want := awaitMove(t, tail), p.lanURL(); got != want {
		t.Errorf("the tailnet browser was sent to %q, want the LAN form %q", got, want)
	}
	waitUntil(t, "Admiral answers on the LAN again", func() bool { return p.answers(p.lanURL()) })
	if p.exited() || pidOf(p) != pid || !p.shipsAnswer(t) {
		t.Error("Admiral restarted to move, or lost its ships")
	}
	if mode, logins := r.savedRemote(); mode || len(logins) != 1 {
		t.Errorf("config.json remote = %v %v, want the mode off and the login kept", mode, logins)
	}
}

func (p *tsProc) otherDevicesBound(t *testing.T) bool {
	t.Helper()
	waitUntil(t, "serve is recognised", func() bool { return len(p.remote(t, "/api/remote").failed()) == 0 })
	return true
}

// With serve not set up there is no tailnet URL: Admiral still moves, and the move message carries none.
func TestModeOnWithoutServeStillMoves(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.writeServe(map[string]any{})
	p := r.startDerived(false)
	lan := dialShips(t, p.lanURL(), r.lanBearer(t, p))

	r.writeConfig(true)

	if got := awaitMove(t, lan); got != "" {
		t.Errorf("move url %q, want none", got)
	}
	waitUntil(t, "the LAN address stops answering", func() bool { return !p.answers(p.lanURL()) })
	if !p.answers(p.LocalURL) {
		t.Error("loopback stopped answering")
	}
}

// An explicit --listen is never rebound: the LAN keeps answering and no browser is told to move.
func TestExplicitListenNeverMoves(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	in := startAdmiral(t, r.home, "0.0.0.0:"+r.port)
	requireLAN(t, in)
	lan := dialShips(t, in.lanURL(), http.Header{"Authorization": {"Bearer " + in.Token}})
	moved := make(chan struct{}, 1)
	go func() {
		for {
			_, data, err := lan.Read(context.Background())
			if err != nil {
				return
			}
			var m struct{ Type string }
			if json.Unmarshal(data, &m) == nil && m.Type == "move" {
				moved <- struct{}{}
			}
		}
	}()

	r.writeConfig(true)
	waitUntil(t, "the mode shows as on", func() bool {
		c := &http.Client{Transport: noProxy(), Timeout: 2 * time.Second}
		resp, err := c.Get(in.LocalURL + "/api/remote")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var v remoteView
		return json.NewDecoder(resp.Body).Decode(&v) == nil && v.Tailscale
	})
	holdThat(t, "a browser was told to move although --listen was given", func() bool { return len(moved) == 0 })
	c := &http.Client{Transport: noProxy(), Timeout: 2 * time.Second}
	if resp, err := c.Get(in.lanURL() + "/"); err != nil {
		t.Errorf("the LAN address stopped answering: %v", err)
	} else {
		resp.Body.Close()
	}
}

// A bind that fails keeps the old listener and shows as a failed "listen" check.
func TestFailedRebindKeepsTheOldListener(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.writeServe(r.serve(443, false))
	p := r.startDerived(true)
	// The LAN address on Admiral's port, held without SO_REUSEADDR, so a bind of 0.0.0.0 on that port fails.
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) { _ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 0) })
	}}
	blocker, err := lc.Listen(context.Background(), "tcp", net.JoinHostPort(p.LANIP, p.Port))
	if err != nil {
		lanUnavailable(t, "cannot hold %s: %v", p.LANIP, err)
	}
	defer blocker.Close()

	r.writeConfig(false)

	waitUntil(t, "the failed bind shows as a check", func() bool {
		for _, id := range p.remote(t, "/api/remote").failed() {
			if id == "listen" {
				return true
			}
		}
		return false
	})
	if !p.answers(p.LocalURL) || p.exited() {
		t.Error("Admiral is no longer listening on loopback after the failed bind")
	}
}
