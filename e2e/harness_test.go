// Package e2e drives the real bin/admiral binary over HTTP and WebSocket.
// Every instance gets a temporary ADMIRAL_HOME and its own port.
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
	homeid "github.com/jbuffin/admiral/internal/home"
)

var (
	localLine = regexp.MustCompile(`^Admiral is running at http://127\.0\.0\.1:(\d+)/$`)
	lanLine   = regexp.MustCompile(`^On other devices: http://([0-9.]+):(\d+)/$`)
)

type instance struct {
	Home      string // ADMIRAL_HOME
	listen    string
	Port      string
	LocalURL  string // first printed line's URL
	LANLine   string // second printed line, verbatim
	LANIP     string // empty when the second line says there is no LAN address
	LANPort   string // the test build's stand-in for the LAN address: see lanEnv
	PairLine  string // third printed line, which only a LAN address brings
	Token     string // a paired device's token, set by requireLAN
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

// Admiral starts on host:0 and reports the port the kernel gave it, which no other process holds. freePort is for a
// port that must be known before the start: a tailscale rig's serve config, a Restart that keeps its port, and a
// port nothing listens on. Parallel tests each draw one, so a port is handed out once per run.
var (
	portsMu sync.Mutex
	ports   = map[string]bool{}
)

func freePort(t *testing.T) string {
	t.Helper()
	portsMu.Lock()
	defer portsMu.Unlock()
	for {
		port := drawPort(t)
		if !ports[port] {
			ports[port] = true
			return port
		}
	}
}

// drawPort is where a port comes from; a test of the harness's own port handling replaces it.
var drawPort = func(t *testing.T) string {
	// The wildcard address, the one Admiral binds for a LAN listen: a loopback
	// probe says nothing about a socket another process holds on 0.0.0.0.
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	return port
}

// loopbackOwner says whether the server answering on in's loopback port is the Admiral just started. A process
// that holds 127.0.0.1:P while Admiral binds 0.0.0.0:P wins the loopback traffic (BSD lets the more specific
// bind in) and no bind error says so; it answers 404, or JSON with another home id. Only such an answer is a
// stranger. Any other failure comes back as unclear, with what was seen, so a broken /api/ships is not read as a port problem.
func loopbackOwner(in *instance) (owned bool, unclear string) {
	c := &http.Client{Transport: noProxy(), Timeout: 5 * time.Second}
	resp, err := c.Get(in.LocalURL + "/api/ships")
	if err != nil {
		return false, "GET /api/ships: " + err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return false, ""
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("GET /api/ships = %d %s", resp.StatusCode, b)
	}
	var body struct {
		Home string `json:"home"`
	}
	if json.Unmarshal(b, &body) != nil {
		return false, ""
	}
	return body.Home == homeid.ID(in.Home), ""
}

// requireOwned reports true when the loopback port answers as another server, so the start is tried again.
func requireOwned(t *testing.T, in *instance) (stranger bool) {
	t.Helper()
	owned, unclear := loopbackOwner(in)
	if unclear != "" {
		in.Stop()
		t.Fatalf("admiral started on %s but its loopback answer is unclear: %s", in.LocalURL, unclear)
	}
	if !owned {
		in.Stop()
	} else {
		track(t, in)
	}
	return !owned
}

// lanPortFrom is the LAN port the test build bound for ADMIRAL_E2E_LAN_PORT=0, which it reports on stderr before
// it prints its URLs. A loopback listen has no LAN listener.
func lanPortFrom(t *testing.T, listen string, stderr *syncBuffer) string {
	t.Helper()
	if host, _, _ := net.SplitHostPort(listen); host != "" && !net.ParseIP(host).IsUnspecified() {
		return ""
	}
	var port string
	waitUntil(t, "Admiral reports its LAN port", func() bool {
		m := lanPortLine.FindStringSubmatch(stderr.String())
		if m != nil {
			port = m[1]
		}
		return m != nil
	})
	return port
}

var lanPortLine = regexp.MustCompile(`e2e LAN port: (\d+)`)

const bindTries = 5

// startWithRetry runs once until Admiral owns its port. once reports true when Admiral exited with "address
// already in use" or answered on its loopback port as another server: a port that is being released, or one a stranger
// holds. A port Admiral picks itself (host:0) can't be taken; this is for the one a Restart keeps.
func startWithRetry[T any](t *testing.T, listen string, once func(listen string) (T, bool)) T {
	t.Helper()
	for try := 1; ; try++ {
		in, inUse := once(listen)
		if !inUse {
			return in
		}
		if try == bindTries {
			t.Fatalf("admiral could not bind %s in %d tries", listen, bindTries)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// hold is how long a test watches for something that must not happen: three
// of the server's 500 ms polls. Anything that does happen is polled for instead.
const hold = 1500 * time.Millisecond

func holdThat(t *testing.T, msg string, check func() bool) {
	t.Helper()
	holdWindow(t, hold, msg, check)
}

// holdWindow is holdThat for a window of the test's own, such as one that must end inside a linger.
func holdWindow(t *testing.T, d time.Duration, msg string, check func() bool) {
	t.Helper()
	holdClean(t, d, func() string {
		if !check() {
			return msg
		}
		return ""
	})
}

// holdClean polls check for d and fails with what it returns the first time that is not empty.
func holdClean(t *testing.T, d time.Duration, check func() string) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if why := check(); why != "" {
			t.Fatalf("%s", why)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The last pass ran up to a step before the window closed; look once at its end, as a sleep then a check did.
	if why := check(); why != "" {
		t.Fatalf("%s", why)
	}
}

// sailOff is the SailOffDelay every Admiral in the suite runs with (3 s in
// production). fastDelays carries it; a test sets its own through world.extra.
const sailOff = time.Second

// shortCall is the plugin call timeout (10 s in production) a test of a hung
// plugin sets through world.extra.
const shortCall = 3 * time.Second

var fastDelays = []string{"ADMIRAL_SAIL_OFF_DELAY=" + sailOff.String()}

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
	return append(env, fastDelays...)
}

func startAdmiral(t *testing.T, home, listen string) *instance {
	t.Helper()
	expectNoMigration(t, home)
	return startWithRetry(t, listen, func(listen string) (*instance, bool) { return startAdmiralOnce(t, home, listen) })
}

func startAdmiralOnce(t *testing.T, home, listen string) (*instance, bool) {
	t.Helper()
	bin := binaryPath(t)
	cmd := exec.Command(bin, "--listen", listen)
	cmd.Env = append(hermeticEnv(t), append(lanEnv("0"), "ADMIRAL_HOME="+home)...)
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
				<-in.done
				if strings.Contains(stderr.String(), "address already in use") {
					return nil, true
				}
				t.Fatalf("admiral exited before printing two URL lines (got %q); stderr: %s", got, stderr.String())
			}
			got = append(got, l)
		case <-timeout:
			t.Fatalf("admiral printed %d of 2 URL lines within 10s (got %q); stderr: %s", len(got), got, stderr.String())
		}
	}
	// A LAN address brings a third line, read before the drain starts.
	if lanLine.MatchString(got[1]) {
		select {
		case l, ok := <-lines:
			if ok {
				in.PairLine = l
			}
		case <-time.After(5 * time.Second):
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
		in.LANIP = m[1]
	} else if !strings.HasPrefix(got[1], "On other devices:") {
		t.Fatalf("second line must start with `On other devices:`, got %q", got[1])
	}
	in.LANPort = lanPortFrom(t, listen, stderr)
	if requireOwned(t, in) {
		return nil, true
	}
	return in, false
}

// Stop is safe to repeat.
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
// port, so the paired devices and the cookie name carry over.
func (in *instance) Restart(t *testing.T) *instance {
	t.Helper()
	in.Stop()
	host, _, _ := net.SplitHostPort(in.listen)
	again := startAdmiral(t, in.Home, net.JoinHostPort(host, in.Port))
	again.Token = in.Token // devices persist across a restart
	return again
}

// startLAN starts an instance on 0.0.0.0 with a device paired over the LAN.
func startLAN(t *testing.T) *instance {
	t.Helper()
	in := startAdmiral(t, newHome(t), "0.0.0.0:0")
	requireLAN(t, in)
	return in
}

func requireLAN(t *testing.T, in *instance) {
	t.Helper()
	if in.LANIP != fakeLAN {
		t.Fatalf("Admiral did not offer the LAN address %s: second line was %q", fakeLAN, in.LANLine)
	}
	if in.Token == "" {
		in.Token = in.pair(t)
	}
}

// pair mints a code over loopback and redeems it from the LAN address, the way a second device does.
func (in *instance) pair(t *testing.T) string {
	t.Helper()
	post := func(url, body string) []byte {
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s = %d %s", url, resp.StatusCode, b)
		}
		return b
	}
	var minted struct{ Code string }
	if err := json.Unmarshal(post(in.LocalURL+"/api/v1/pairing-codes", ""), &minted); err != nil || minted.Code == "" {
		t.Fatalf("mint a pairing code: %v", err)
	}
	var paired struct{ Token string }
	if err := json.Unmarshal(post(in.lanURL()+"/api/v1/pair", `{"code":"`+minted.Code+`","name":"e2e"}`), &paired); err != nil || paired.Token == "" {
		t.Fatalf("redeem the code: %v", err)
	}
	return paired.Token
}

// fakeLAN is the TEST-NET address Admiral offers other devices in these tests; nothing dials it.
const fakeLAN = "192.0.2.10"

// lanEnv gives the test build a LAN with no network: while Admiral listens on a wildcard address it also listens on
// 127.0.0.1:lanPort, and treats those connections as a LAN peer reaching fakeLAN (cmd/admiral/listen_tcp_e2e.go).
func lanEnv(lanPort string) []string {
	return []string{"ADMIRAL_LAN_IP=" + fakeLAN, "ADMIRAL_E2E_LAN_PORT=" + lanPort}
}

// lanURL is the base URL a non-local client uses: Admiral sees its peer as a LAN address.
func (in *instance) lanURL() string { return "http://127.0.0.1:" + in.LANPort }

// advertisedLAN is the LAN URL Admiral gives other devices.
func (in *instance) advertisedLAN() string { return "http://" + net.JoinHostPort(fakeLAN, in.Port) }

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
		Transport:     stallDump{noProxy()},
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

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
	return wsDialAt(t, base, "/ws", query, hdr, host)
}

func wsDialAt(t *testing.T, base, path, query string, hdr http.Header, host string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme = "ws"
	u.Path = path
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

// killTmux ends every tmux process on sock. A client still starting when Admiral exits forks its server after the socket
// stops answering, so it kills again until no process names the socket.
func killTmux(sock string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = exec.Command("tmux", "-S", sock, "kill-server").Run()
		if tmuxOn(sock) == "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = exec.Command("pkill", "-9", "-f", "--", tmuxOnPattern(sock)).Run()
}

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

// expectNoMigration fails a test whose config was migrated at startup: a
// fixture without a fleet would otherwise migrate silently and still pass. The
// migration's own tests (TestMigrate...) are exempt.
func expectNoMigration(t *testing.T, home string) {
	t.Helper()
	if strings.HasPrefix(t.Name(), "TestMigrate") {
		return
	}
	t.Cleanup(func() {
		if m, _ := filepath.Glob(filepath.Join(home, "config.json.pre-*")); len(m) > 0 {
			t.Errorf("the config was migrated at startup (%s): a fixture is missing a fleet on a source", filepath.Base(m[0]))
		}
	})
}
