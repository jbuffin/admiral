// Package e2e drives the real bin/admiral binary over HTTP and WebSocket
// (seam 1). Every instance gets a temporary ADMIRAL_HOME and its own port.
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var (
	localLine = regexp.MustCompile(`^Admiral is running at http://127\.0\.0\.1:(\d+)/$`)
	lanLine   = regexp.MustCompile(`^On other devices: http://([0-9.]+):(\d+)/\?token=([A-Za-z0-9_-]+)$`)
)

// instance is one running admiral process.
type instance struct {
	Home      string // ADMIRAL_HOME
	listen    string // the --listen value
	Port      string
	LocalURL  string // first printed line's URL
	LANLine   string // second printed line, verbatim
	LANIP     string // empty when the second line says there is no LAN address
	Token     string // from the second printed line, empty without a LAN address
	cmd       *exec.Cmd
	done      chan struct{}
	stderrBuf *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func binaryPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "bin", "admiral"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("bin/admiral not found: run make build (%v)", err)
	}
	return p
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

// newHome returns an ADMIRAL_HOME path that does not exist yet, so tests can
// see the server create it.
func newHome(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "admiral-home")
}

// The engineer's own values, captured before any test sets its own.
var (
	userHome     = os.Getenv("HOME")
	identityVars = []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME"}
	userIdentity = map[string]string{}
)

func init() {
	for _, k := range identityVars {
		userIdentity[k] = os.Getenv(k)
	}
}

// hermeticEnv is the test process's environment with the engineer's HOME
// replaced by a temp directory and their harness identity variables dropped.
// A value a test set itself (t.Setenv) passes through.
func hermeticEnv(t *testing.T) []string {
	t.Helper()
	drop := map[string]bool{}
	for _, k := range identityVars {
		if os.Getenv(k) == userIdentity[k] {
			drop[k] = true
		}
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] || (k == "HOME" && os.Getenv("HOME") == userHome) {
			continue
		}
		env = append(env, kv)
	}
	if os.Getenv("HOME") == userHome {
		env = append(env, "HOME="+t.TempDir())
	}
	return env
}

// startAdmiral starts bin/admiral --listen <listen> with the given home. It
// waits up to 10 s for the two printed lines and stops the process on cleanup.
func startAdmiral(t *testing.T, home, listen string) *instance {
	t.Helper()
	bin := binaryPath(t)
	cmd := exec.Command(bin, "--listen", listen)
	cmd.Env = append(hermeticEnv(t), "ADMIRAL_HOME="+home)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	in := &instance{Home: home, listen: listen, cmd: cmd, done: make(chan struct{}), stderrBuf: stderr}
	go func() { _ = cmd.Wait(); close(in.done) }()
	// Admiral starts a tmux server that outlives it; end this home's. Cleanups
	// run last-registered-first, so this one is registered first to run after
	// Admiral has stopped (a live Admiral would start a new tmux).
	t.Cleanup(func() { killTmux(filepath.Join(home, "tmux.sock")) })
	t.Cleanup(in.Stop)

	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var got []string
	timeout := time.After(10 * time.Second)
	for len(got) < 2 {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("admiral exited before printing two URL lines (got %q); stderr: %s", got, stderr.String())
			}
			got = append(got, l)
		case <-timeout:
			t.Fatalf("admiral printed %d of 2 URL lines within 10s (got %q); stderr: %s", len(got), got, stderr.String())
		}
	}
	go func() { // keep draining so the pipe never blocks the server
		for range lines {
		}
	}()

	m := localLine.FindStringSubmatch(got[0])
	if m == nil {
		t.Fatalf("first line must be `Admiral is running at http://127.0.0.1:<port>/`, got %q", got[0])
	}
	in.Port = m[1]
	in.LocalURL = fmt.Sprintf("http://127.0.0.1:%s", in.Port)
	in.LANLine = got[1]
	if m := lanLine.FindStringSubmatch(got[1]); m != nil {
		if m[2] != in.Port {
			t.Fatalf("second line port %s differs from first line port %s", m[2], in.Port)
		}
		in.LANIP, in.Token = m[1], m[3]
	} else if !strings.HasPrefix(got[1], "On other devices:") {
		t.Fatalf("second line must start with `On other devices:`, got %q", got[1])
	}
	return in
}

// Stop ends the process with SIGTERM, then SIGKILL after 5 s. Safe to repeat.
func (in *instance) Stop() {
	select {
	case <-in.done:
		return
	default:
	}
	_ = in.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-in.done:
	case <-time.After(5 * time.Second):
		_ = in.cmd.Process.Kill()
		<-in.done
	}
}

// Restart stops the instance and starts a new one on the same home, host and
// port, so the token and the cookie name carry over.
func (in *instance) Restart(t *testing.T) *instance {
	t.Helper()
	in.Stop()
	host, _, _ := net.SplitHostPort(in.listen)
	return startAdmiral(t, in.Home, net.JoinHostPort(host, in.Port))
}

// startLAN starts an instance on 0.0.0.0:<free port> and returns it with a
// non-loopback IPv4 that reaches it. It skips when that address can't be
// reached within 3 s (macOS firewall, no network), but fails under CI.
func startLAN(t *testing.T) *instance {
	t.Helper()
	in := startAdmiral(t, newHome(t), "0.0.0.0:"+freePort(t))
	requireLAN(t, in)
	return in
}

func requireLAN(t *testing.T, in *instance) {
	t.Helper()
	if in.LANIP == "" {
		lanUnavailable(t, "no LAN IPv4 address: second line was %q", in.LANLine)
		return
	}
	// A TCP connect is not enough: a firewall can accept it and then stall the
	// request. Any HTTP answer (even 401) proves the address is reachable.
	c := &http.Client{Transport: noProxy(), Timeout: 3 * time.Second}
	resp, err := c.Get(in.lanURL() + "/")
	if err != nil {
		lanUnavailable(t, "no answer from %s within 3s (macOS firewall dialog? VPN?): %v", in.lanURL(), err)
		return
	}
	resp.Body.Close()
}

func lanUnavailable(t *testing.T, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	if os.Getenv("CI") != "" {
		t.Fatalf("non-local test cannot run on CI: %s", msg)
	}
	t.Skipf("non-local test skipped: %s", msg)
}

// lanURL is the base URL a non-local client uses (peer address is not loopback).
func (in *instance) lanURL() string { return "http://" + net.JoinHostPort(in.LANIP, in.Port) }

func (in *instance) cookieName() string { return "admiral_token_" + in.Port }

// noProxy is a transport that ignores HTTP_PROXY: Go only skips proxies for
// loopback, so LAN-address requests would otherwise go through one.
func noProxy() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}

// client is an HTTP client that never follows redirects, never stores cookies
// and gives up after 5 s. Tests send cookies explicitly.
func client() *http.Client {
	return &http.Client{
		Transport:     noProxy(),
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// get issues a GET with optional extra headers and a Host override.
func get(t *testing.T, rawURL string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	return do(t, http.MethodGet, rawURL, hdr)
}

func do(t *testing.T, method, rawURL string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		if strings.EqualFold(k, "Host") {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// wsDial opens /ws on base (http://host:port) with a 5 s limit. host overrides
// the Host header when non-empty. On refusal it returns the handshake response.
func wsDial(t *testing.T, base, query string, hdr http.Header, host string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme = "ws"
	u.Path = "/ws"
	u.RawQuery = query
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: noProxy()}, HTTPHeader: hdr, Host: host})
	if c != nil {
		t.Cleanup(func() { c.CloseNow() })
	}
	return c, resp, err
}

// killTmux ends the tmux server on sock and waits until the socket stops answering.
func killTmux(sock string) {
	_ = exec.Command("tmux", "-S", sock, "kill-server").Run()
	for i := 0; i < 20; i++ {
		if exec.Command("tmux", "-S", sock, "list-sessions").Run() != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// readJSON reads one text message from c and unmarshals it.
func readJSON(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("websocket message %q is not JSON: %v", data, err)
	}
}
