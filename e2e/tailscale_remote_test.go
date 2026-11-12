package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Tailscale mode at startup, the ts.net URL, the pairing base and GET /api/remote, all against a fake tailscale CLI.

const (
	tsName  = "fake-mac.tailnet-fake.ts.net"
	tsLogin = "alice@example.com"
)

type tsRig struct {
	t    *testing.T
	dir  string // FAKETS_DIR
	home string
	port string
}

func newTSRig(t *testing.T) *tsRig {
	t.Helper()
	r := &tsRig{t: t, dir: t.TempDir(), home: newHome(t), port: freePort(t)}
	r.writeStatus(tsStatus(nil))
	r.writeServe(r.serve(443, false))
	return r
}

func (r *tsRig) put(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *tsRig) writeJSON(name string, v any) {
	r.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		r.t.Fatal(err)
	}
	r.put(name, string(b))
}

func (r *tsRig) writeStatus(v any) { r.writeJSON("status.json", v) }
func (r *tsRig) writeServe(v any)  { r.writeJSON("serve.json", v) }

// tsStatus is `tailscale status --json` of a logged-in node with HTTPS and MagicDNS on; edit changes it.
func tsStatus(edit func(m map[string]any)) map[string]any {
	m := map[string]any{
		"BackendState": "Running",
		"Self": map[string]any{
			"DNSName":      tsName + ".",
			"UserID":       1001,
			"Capabilities": []string{"https"},
		},
		"CurrentTailnet": map[string]any{"Name": "fake-tailnet", "MagicDNSEnabled": true, "MagicDNSSuffix": "tailnet-fake.ts.net"},
		"CertDomains":    []string{tsName},
		"User":           map[string]any{"1001": map[string]any{"ID": 1001, "LoginName": tsLogin}},
	}
	if edit != nil {
		edit(m)
	}
	return m
}

func tsHostPort(httpsPort int) string { return fmt.Sprintf("%s:%d", tsName, httpsPort) }

// proxyTo is a serve config with an HTTPS handler on httpsPort proxying to a loopback port.
func proxyTo(httpsPort int, adm string, funnel bool) map[string]any {
	m := map[string]any{
		"TCP": map[string]any{fmt.Sprint(httpsPort): map[string]any{"HTTPS": true}},
		"Web": map[string]any{tsHostPort(httpsPort): map[string]any{
			"Handlers": map[string]any{"/": map[string]any{"Proxy": "http://127.0.0.1:" + adm}},
		}},
	}
	if funnel {
		m["AllowFunnel"] = map[string]any{tsHostPort(httpsPort): true}
	}
	return m
}

// serve is a serve config pointing at this rig's Admiral.
func (r *tsRig) serve(httpsPort int, funnel bool) map[string]any {
	return proxyTo(httpsPort, r.port, funnel)
}

func (r *tsRig) writeConfig(mode bool) {
	r.t.Helper()
	body := fmt.Sprintf(`{"version":1,"fleets":{"w":{"name":"W","claude":{}}},"remote":{"tailscale":%t,"allowedLogins":["%s"]}}`, mode, tsLogin)
	if err := os.MkdirAll(r.home, 0o755); err != nil {
		r.t.Fatal(err)
	}
	tmp := filepath.Join(r.home, ".config.json.tmp")
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(r.home, "config.json")); err != nil {
		r.t.Fatal(err)
	}
}

func (r *tsRig) calls() string {
	b, _ := os.ReadFile(filepath.Join(r.dir, "calls"))
	return string(b)
}

type tsProc struct {
	*instance
	out *syncBuffer
}

const tsStartBound = 20 * time.Second

// start runs Admiral on loopback with the fake tailscale first on PATH. noBinary leaves tailscale off PATH.
func (r *tsRig) start(mode bool, noBinary bool) *tsProc {
	t := r.t
	t.Helper()
	r.writeConfig(mode)
	fakeDir, err := filepath.Abs(filepath.Join("testdata", "faketailscale"))
	if err != nil {
		t.Fatal(err)
	}
	path := fakeDir + ":" + os.Getenv("PATH")
	if noBinary {
		path = t.TempDir() + ":/usr/bin:/bin"
	}
	listen := "127.0.0.1:" + r.port
	cmd := exec.Command(binaryPath(t), "--listen", listen)
	cmd.Env = append(hermeticEnv(t), "ADMIRAL_HOME="+r.home, "FAKETS_DIR="+r.dir, "PATH="+path)
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	in := &instance{Home: r.home, listen: listen, Port: r.port, cmd: cmd, done: make(chan struct{}), stderrBuf: out}
	go func() { _ = cmd.Wait(); close(in.done) }()
	t.Cleanup(func() { killTmux(filepath.Join(r.home, "tmux.sock")) })
	t.Cleanup(in.Stop)
	return &tsProc{instance: in, out: out}
}

func (p *tsProc) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// otherDevices waits for the "On other devices:" line and returns it; it fails if Admiral exits or takes too long.
func (p *tsProc) otherDevices(t *testing.T) string {
	t.Helper()
	end := time.Now().Add(tsStartBound)
	for time.Now().Before(end) {
		for _, l := range strings.Split(p.out.String(), "\n") {
			if strings.HasPrefix(l, "On other devices:") {
				return l
			}
		}
		if p.exited() {
			t.Fatalf("admiral exited before printing the On other devices line:\n%s", p.out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no On other devices line within %s:\n%s", tsStartBound, p.out.String())
	return ""
}

var tsClient = &http.Client{Transport: noProxy(), Timeout: 30 * time.Second}

func (p *tsProc) call(t *testing.T, method, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, p.base()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tsClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v\nadmiral output:\n%s", method, path, err, p.out.String())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (p *tsProc) base() string { return "http://127.0.0.1:" + p.Port }

type remoteView struct {
	Tailscale     bool     `json:"tailscale"`
	AllowedLogins []string `json:"allowedLogins"`
	URL           string   `json:"url"`
	Checks        []struct {
		ID     string `json:"id"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	} `json:"checks"`
}

func (v remoteView) failed() []string {
	var out []string
	for _, c := range v.Checks {
		if !c.OK {
			out = append(out, c.ID)
		}
	}
	return out
}

func (p *tsProc) remote(t *testing.T, path string) remoteView {
	t.Helper()
	code, body := p.call(t, http.MethodGet, path)
	if code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, code, body)
	}
	var v remoteView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	return v
}

// waitStopped waits for Admiral to exit on its own; on u1 it never does, so the wait is bounded.
func (p *tsProc) waitStopped(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(tsStartBound):
		p.Stop()
		t.Fatalf("admiral kept running; it should have refused to start:\n%s", p.out.String())
	}
}

// With Funnel on for Admiral's port, in serve, as a TCP forward or in a foreground serve, Admiral exits non-zero
// with one line saying how to turn it off, even when another check failed too.
func TestTailscaleFunnelOnStopsStartup(t *testing.T) {
	t.Parallel()
	const prefix = "Funnel is on for Admiral's port, which makes it public; turn it off with: "
	cases := []struct {
		name  string
		serve func(r *tsRig) map[string]any
		down  bool // the status call fails as well
		want  string
	}{
		{"serve config", func(r *tsRig) map[string]any { return r.serve(443, true) }, false, prefix + "tailscale funnel --https=443 off"},
		{"foreground serve", func(r *tsRig) map[string]any {
			return map[string]any{"Foreground": map[string]any{"session-1": r.serve(8443, true)}}
		}, false, prefix + "stop the foreground tailscale funnel process that serves it"},
		{"tcp forward", func(r *tsRig) map[string]any {
			return map[string]any{
				"TCP":         map[string]any{"10000": map[string]any{"TCPForward": "127.0.0.1:" + r.port}},
				"AllowFunnel": map[string]any{tsHostPort(10000): true},
			}
		}, false, prefix + "tailscale funnel --tcp=10000 off"},
		{"with the status call failing", func(r *tsRig) map[string]any { return r.serve(443, true) }, true, prefix + "tailscale funnel --https=443 off"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newTSRig(t)
			r.writeServe(c.serve(r))
			if c.down {
				r.put("status.exit", "1")
			}
			p := r.start(true, false)
			p.waitStopped(t)
			if code := p.cmd.ProcessState.ExitCode(); code == 0 {
				t.Errorf("exit code 0, want non-zero:\n%s", p.out.String())
			}
			out := p.out.String()
			var lines []string
			for _, l := range strings.Split(out, "\n") {
				if strings.Contains(strings.ToLower(l), "funnel") {
					lines = append(lines, l)
				}
			}
			if want := "admiral: " + c.want; len(lines) != 1 || lines[0] != want {
				t.Errorf("want the one line %q, got %q in:\n%s", want, lines, out)
			}
			if strings.Contains(out, "Admiral is running") {
				t.Errorf("admiral announced it was running:\n%s", out)
			}
		})
	}
}

// Anything but Funnel that fails a check still starts Admiral, and the On other devices line warns which check failed.
func TestTailscaleFailedCheckWarnsAndStarts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		setup   func(r *tsRig)
		mention string // the failed check's own text
		noBin   bool
	}{
		{"serve missing", func(r *tsRig) { r.writeServe(map[string]any{}) }, "tailscale serve does not proxy to Admiral's port", false},
		{"tcp forward without funnel", func(r *tsRig) {
			r.writeServe(map[string]any{"TCP": map[string]any{"10000": map[string]any{"TCPForward": "127.0.0.1:" + r.port}}})
		}, "raw tcp forward", false},
		{"logged out", func(r *tsRig) {
			r.writeStatus(tsStatus(func(m map[string]any) { m["BackendState"] = "NeedsLogin" }))
		}, "logged out", false},
		{"unparsable output", func(r *tsRig) { r.put("status.json", "not json") }, "not json", false},
		{"slow CLI", func(r *tsRig) { r.put("sleep", "60") }, "timed out", false},
		{"no tailscale binary", func(r *tsRig) {}, "not installed", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newTSRig(t)
			c.setup(r)
			p := r.start(true, c.noBin)
			line := p.otherDevices(t)
			low := strings.ToLower(line)
			if strings.Contains(low, "https://") || strings.Contains(low, "not reachable") || !strings.Contains(low, strings.ToLower(c.mention)) {
				t.Errorf("want a warning containing %q and no URL, got %q", c.mention, line)
			}
			if p.exited() {
				t.Errorf("admiral exited:\n%s", p.out.String())
			}
		})
	}
}

// With serve set up, the startup line and a minted pairing code carry the ts.net URL; with the mode off again the pairing base is not it.
func TestTailscaleStartupLineAndPairingBase(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	p := r.start(true, false)
	if line, want := p.otherDevices(t), "On other devices: https://"+tsName+"/"; line != want {
		t.Errorf("startup line %q, want %q", line, want)
	}
	if !strings.Contains(r.calls(), "status --json") {
		t.Fatalf("the fake tailscale was never run; calls: %q", r.calls())
	}
	mint := func() string {
		code, body := p.call(t, http.MethodPost, "/api/v1/pairing-codes")
		var out struct{ Code, URL string }
		if code != http.StatusOK || json.Unmarshal(body, &out) != nil {
			t.Fatalf("mint: %d %s", code, body)
		}
		return strings.TrimSuffix(out.URL, "/pair?code="+out.Code)
	}
	if base := mint(); base != "https://"+tsName {
		t.Errorf("pairing base %q, want https://%s", base, tsName)
	}

	r.writeConfig(false)
	deadline := time.Now().Add(10 * time.Second)
	for p.remote(t, "/api/v1/remote").Tailscale && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if base := mint(); strings.Contains(base, tsName) {
		t.Errorf("pairing base %q still the ts.net URL with the mode off", base)
	}
}

// A failed check is still reported when the ts.net URL is known: a raw TCP forward beside a working serve bypasses the tailnet checks.
func TestTailscaleStartupWarnsAboutAFailedCheckBesideTheURL(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	cfg := r.serve(443, false)
	cfg["TCP"].(map[string]any)["10000"] = map[string]any{"TCPForward": "127.0.0.1:" + r.port}
	r.writeServe(cfg)
	p := r.start(true, false)
	if line, want := p.otherDevices(t), "On other devices: https://"+tsName+"/"; line != want {
		t.Errorf("startup line %q, want %q", line, want)
	}
	deadline := time.Now().Add(tsStartBound)
	for !strings.Contains(strings.ToLower(p.out.String()), "raw tcp forward") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := strings.ToLower(p.out.String()); !strings.Contains(got, "warning") || !strings.Contains(got, "raw tcp forward") {
		t.Errorf("want a warning about the raw TCP forward beside the URL:\n%s", p.out.String())
	}
}

// A serve HTTPS port other than 443 is in the startup line.
func TestTailscaleStartupLineKeepsANon443Port(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	r.writeServe(r.serve(8443, false))
	p := r.start(true, false)
	if line, want := p.otherDevices(t), "On other devices: https://"+tsName+":8443/"; line != want {
		t.Errorf("startup line %q, want %q", line, want)
	}
}

// The route reports the mode and logins; with the mode off it runs no checks and never runs tailscale.
func TestTailscaleRemoteStatusModeOff(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	p := r.start(false, false)
	p.otherDevices(t)
	for _, path := range []string{"/api/remote", "/api/v1/remote"} {
		v := p.remote(t, path)
		if v.Tailscale || v.URL != "" || len(v.Checks) != 0 || !reflect.DeepEqual(v.AllowedLogins, []string{tsLogin}) {
			t.Errorf("%s with the mode off = %+v, want the logins only", path, v)
		}
	}
	if c := r.calls(); c != "" {
		t.Errorf("tailscale ran with the mode off: %q", c)
	}
}

// /api/remote and /api/v1/remote answer the same; the checks themselves are pinned in internal/tailscale.
func TestTailscaleRemoteStatusAlias(t *testing.T) {
	t.Parallel()
	r := newTSRig(t)
	p := r.start(true, false)
	p.otherDevices(t)
	v1, v := p.remote(t, "/api/v1/remote"), p.remote(t, "/api/remote")
	if !reflect.DeepEqual(v1, v) {
		t.Errorf("/api/remote and /api/v1/remote differ: %+v vs %+v", v, v1)
	}
	if !v1.Tailscale || v1.URL != "https://"+tsName+"/" || len(v1.Checks) != 4 || len(v1.failed()) != 0 {
		t.Errorf("view %+v, want the mode on, the ts.net URL and four ok checks", v1)
	}
}
