package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Turning Tailscale mode on or off moves Admiral's listener without a restart and tells the browsers it leaves behind.

// startDerived runs Admiral with no --listen, so its address follows the mode, on this rig's port.
func (r *tsRig) startDerived(mode bool) *tsProc {
	return r.owned(func() *tsProc { return r.startDerivedOnce(mode) })
}

func (r *tsRig) startDerivedOnce(mode bool) *tsProc {
	t := r.t
	t.Helper()
	r.writeConfig(mode)
	fakeDir, err := filepath.Abs(filepath.Join("testdata", "faketailscale"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath(t))
	lanPort := freePort(t)
	cmd.Env = append(append(hermeticEnv(t), lanEnv(lanPort)...), "ADMIRAL_HOME="+r.home, "ADMIRAL_PORT="+r.port, "FAKETS_DIR="+r.dir, "PATH="+fakeDir+":"+os.Getenv("PATH"))
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	in := &instance{Home: r.home, Port: r.port, LocalURL: "http://127.0.0.1:" + r.port, LANIP: fakeLAN, LANPort: lanPort, cmd: cmd, done: make(chan struct{}), stderrBuf: out}
	go func() { _ = cmd.Wait(); close(in.done) }()
	t.Cleanup(func() { killTmux(filepath.Join(r.home, "tmux.sock")) })
	t.Cleanup(in.Stop)
	return &tsProc{instance: in, out: out}
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
	until(t, what, ok)
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

// awaitClosed reads until the socket errors: a LAN socket must not outlive the move.
func awaitClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the LAN socket stayed open after the move")
			}
			return
		}
	}
}

func (r *tsRig) lanBearer(t *testing.T, p *tsProc) http.Header {
	t.Helper()
	return http.Header{"Authorization": {"Bearer " + p.pair(t)}}
}

func (p *tsProc) shipsAnswer(t *testing.T) bool {
	code, _ := p.call(t, http.MethodGet, "/api/ships")
	return code == http.StatusOK
}

func pidOf(p *tsProc) int { return p.cmd.Process.Pid }

// listensOn is the address Admiral's process listens on for its port, as lsof prints it ("*:7420", "127.0.0.1:7420").
func (p *tsProc) listensOn(t *testing.T) string {
	t.Helper()
	out, _ := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(pidOf(p)), "-iTCP:"+p.Port, "-sTCP:LISTEN", "-Fn").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if name, ok := strings.CutPrefix(line, "n"); ok {
			return name
		}
	}
	return ""
}

// Mode on takes Admiral off the LAN with no restart: the LAN browser is sent to the tailnet URL before its socket
// closes, loopback keeps answering with its socket open, and the ships stay listed.
func TestModeOnMovesAdmiralOffTheLAN(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.writeServe(r.serve(443, false))
	p := r.startDerived(false)
	pid := pidOf(p)
	lan := dialShips(t, p.lanURL(), r.lanBearer(t, p))
	local := dialShips(t, p.LocalURL, nil)
	go func() { // a Ping needs a reader to receive its pong
		for {
			if _, _, err := local.Read(context.Background()); err != nil {
				return
			}
		}
	}()

	r.writeConfig(true)

	if got, want := awaitMove(t, lan), "https://"+tsName+"/"; got != want {
		t.Errorf("the LAN browser was sent to %q, want %q", got, want)
	}
	awaitClosed(t, lan)
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

// Mode off sends the tailnet browser to the LAN address before its socket is cut, then Admiral listens on every
// interface again.
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
	if got := p.listensOn(t); got != "127.0.0.1:"+p.Port {
		t.Fatalf("with the mode on Admiral listens on %q, want loopback only", got)
	}

	if code, body := p.post(t, "/api/remote/off", `{"confirm":true}`); code != http.StatusOK {
		t.Fatalf("Turn off: %d %s", code, body)
	}

	if got, want := awaitMove(t, tail), p.advertisedLAN(); got != want {
		t.Errorf("the tailnet browser was sent to %q, want the LAN form %q", got, want)
	}
	waitUntil(t, "Admiral listens on every interface again", func() bool { return p.listensOn(t) == "*:"+p.Port })
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
	awaitClosed(t, lan)
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
	// Holding the test build's LAN port fails the bind that would bring the LAN back.
	blocker, err := net.Listen("tcp", "127.0.0.1:"+p.LANPort)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()

	// A dial refused at any point means the old listener was closed, even if it came back.
	// Several dialers, so one lands in the instant a closed listener would leave.
	stop, refused := make(chan struct{}), make(chan error, 4)
	var stopOnce sync.Once
	stopDialers := func() { stopOnce.Do(func() { close(stop) }) }
	defer stopDialers() // a failed waitUntil must not leave the dialers spinning
	var dialers sync.WaitGroup
	addr := net.JoinHostPort("127.0.0.1", p.Port)
	for range 4 {
		dialers.Add(1)
		go func() {
			defer dialers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, err := net.DialTimeout("tcp", addr, time.Second)
				if errors.Is(err, syscall.ECONNREFUSED) {
					refused <- err
					return
				}
				if err != nil { // a timeout or a full ephemeral range says nothing about the listener
					time.Sleep(10 * time.Millisecond)
					continue
				}
				_ = c.(*net.TCPConn).SetLinger(0) // a reset leaves no TIME_WAIT to use up the ports
				c.Close()
			}
		}()
	}
	r.writeConfig(false)

	waitUntil(t, "the failed bind shows as a check", func() bool {
		for _, id := range p.remote(t, "/api/remote").failed() {
			if id == "listen" {
				return true
			}
		}
		return false
	})
	stopDialers()
	dialers.Wait()
	select {
	case err := <-refused:
		t.Errorf("loopback refused a dial during the failed bind: %v", err)
	default:
	}
	if !p.answers(p.LocalURL) || p.exited() {
		t.Error("Admiral is no longer listening on loopback after the failed bind")
	}
}
