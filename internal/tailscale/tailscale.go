// Package tailscale reads the local tailscale CLI to tell whether Admiral is reachable over the tailnet.
// It only ever runs `status --json` and `serve status --json`, each under a short timeout.
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
)

// timeout bounds each CLI call: `tailscale serve` has hung for minutes on a tailnet with Serve disabled.
var timeout = 4 * time.Second

// ServeTimeout bounds the call that sets serve up, which can wait on the tailnet.
const ServeTimeout = 60 * time.Second

// ServeTimeoutEnv shortens ServeTimeout for tests.
const ServeTimeoutEnv = "ADMIRAL_TAILSCALE_SERVE_TIMEOUT"

// Check ids, in the order Inspect reports them.
const (
	CheckRunning  = "running"
	CheckDNSHTTPS = "https-magicdns"
	CheckServe    = "serve"
	CheckFunnel   = "funnel-off"
)

type Check struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Report is what the tailscale CLI says about Admiral's port.
type Report struct {
	Checks []Check
	// URL is the ts.net address, empty unless serve proxies to Admiral's port and the MagicDNS name is known.
	URL string
	// Funnel is true when Funnel is on for a handler or TCP forward that reaches Admiral's port.
	Funnel bool
	// Login is this Mac's tailnet login, empty when unknown.
	Login string
	// Holder is what serves HTTPS port 443 when that isn't Admiral, empty when 443 is free or Admiral's.
	Holder string
	// ServeErr is why serve status couldn't be read, empty when it could.
	ServeErr string
}

// Failed returns the first failed check, if any.
// Check returns the check with the id.
func (r Report) Check(id string) (Check, bool) {
	for _, c := range r.Checks {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

func (r Report) Failed() (Check, bool) {
	for _, c := range r.Checks {
		if !c.OK {
			return c, true
		}
	}
	return Check{}, false
}

type status struct {
	BackendState string
	Self         struct {
		DNSName string
		UserID  int64
	}
	CurrentTailnet struct{ MagicDNSEnabled bool }
	CertDomains    []string
	User           map[string]struct{ LoginName string }
}

type serveConfig struct {
	TCP map[string]struct {
		HTTPS        bool
		TCPForward   string
		TerminateTLS string
	}
	Web map[string]struct {
		Handlers map[string]struct{ Proxy, Path, Text string }
	}
	AllowFunnel map[string]bool
	Foreground  map[string]*serveConfig
}

func run(ctx context.Context, limit time.Duration, args ...string) ([]byte, error) {
	return runCLI(ctx, limit, false, args...)
}

// runCLI runs tailscale. With verbose, a failure or timeout carries everything the CLI wrote, every line.
func runCLI(ctx context.Context, limit time.Duration, verbose bool, args ...string) ([]byte, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tailscale", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		switch {
		case parent.Err() != nil:
			return nil, fmt.Errorf("tailscale %s was cancelled", strings.Join(args, " "))
		case ctx.Err() != nil:
			msg := fmt.Sprintf("tailscale %s timed out after %s", strings.Join(args, " "), limit)
			if verbose {
				msg += withOutput(&out, &errOut) + "\nTailscale may still turn serve on later; run setup again to check."
			}
			return nil, errors.New(msg)
		case errors.Is(err, exec.ErrNotFound):
			return nil, fmt.Errorf("tailscale is not installed or not on PATH")
		}
		if verbose {
			return nil, fmt.Errorf("tailscale %s failed: %v%s", strings.Join(args, " "), err, withOutput(&out, &errOut))
		}
		msg := firstLine(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("tailscale %s failed: %s", strings.Join(args, " "), msg)
	}
	return out.Bytes(), nil
}

// withOutput is what the CLI wrote to stdout and stderr, trimmed, on lines of their own.
func withOutput(out, errOut *bytes.Buffer) string {
	var b strings.Builder
	for _, w := range []*bytes.Buffer{out, errOut} {
		if t := strings.TrimSpace(w.String()); t != "" {
			b.WriteString("\n" + t)
		}
	}
	return b.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Inspect runs both CLI calls at once and checks them against Admiral's loopback port.
func Inspect(ctx context.Context, port string) Report {
	var (
		wg               sync.WaitGroup
		statusOut, serve []byte
		statusErr        error
		serveErr         error
	)
	wg.Add(2)
	go func() { defer wg.Done(); statusOut, statusErr = run(ctx, timeout, "status", "--json") }()
	go func() { defer wg.Done(); serve, serveErr = run(ctx, timeout, "serve", "status", "--json") }()
	wg.Wait()
	return Evaluate(statusOut, statusErr, serve, serveErr, port)
}

// Evaluate applies the rules to the CLI outputs; a non-nil error stands for a failed call.
func Evaluate(statusOut []byte, statusErr error, serveOut []byte, serveErr error, port string) Report {
	var rep Report
	var st status
	statusOK := false
	var statusWhy string
	switch {
	case statusErr != nil:
		statusWhy = statusErr.Error()
	case json.Unmarshal(statusOut, &st) != nil:
		statusWhy = "tailscale status --json gave output that is not JSON"
	default:
		statusOK = true
	}
	// A name from a logged-out node can't be reached, so it doesn't count as known.
	name := ""
	if statusOK && st.BackendState == "Running" {
		name = strings.TrimSuffix(st.Self.DNSName, ".")
	}

	switch {
	case !statusOK:
		rep.add(CheckRunning, false, statusWhy)
	case st.BackendState != "Running":
		rep.add(CheckRunning, false, fmt.Sprintf("tailscale is %s; start it and log in", describeState(st.BackendState)))
	case name == "":
		rep.add(CheckRunning, false, "tailscale reports no name for this Mac")
	default:
		rep.Login = st.User[strconv.FormatInt(st.Self.UserID, 10)].LoginName
		detail := "tailscale is running"
		if rep.Login != "" {
			detail += " as " + rep.Login
		}
		rep.add(CheckRunning, true, detail)
	}

	switch {
	case !statusOK:
		rep.add(CheckDNSHTTPS, false, "unknown while tailscale status is unavailable")
	case !st.CurrentTailnet.MagicDNSEnabled:
		rep.add(CheckDNSHTTPS, false, "MagicDNS is off for this tailnet; turn it on in the tailscale admin console")
	case len(st.CertDomains) == 0:
		rep.add(CheckDNSHTTPS, false, "HTTPS certificates are off for this tailnet; turn them on in the tailscale admin console")
	default:
		rep.add(CheckDNSHTTPS, true, "HTTPS and MagicDNS are on")
	}

	var cfg serveConfig
	serveWhy := ""
	switch {
	case serveErr != nil:
		serveWhy = serveErr.Error()
	case json.Unmarshal(serveOut, &cfg) != nil:
		serveWhy = "tailscale serve status --json gave output that is not JSON"
	}
	if serveWhy != "" {
		rep.ServeErr = serveWhy
		rep.add(CheckServe, false, serveWhy)
		rep.add(CheckFunnel, false, "unknown while tailscale serve status is unavailable")
		return rep
	}

	f := findAdmiral(&cfg, port)
	switch {
	case f.rawTCP:
		rep.add(CheckServe, false, fmt.Sprintf("a raw TCP forward to Admiral's port bypasses Admiral's tailnet checks; replace it with: %s", ServeHint(port)))
	case len(f.https) == 0:
		rep.add(CheckServe, false, fmt.Sprintf("tailscale serve does not proxy to Admiral's port; run: %s", ServeHint(port)))
	default:
		rep.add(CheckServe, true, fmt.Sprintf("tailscale serve proxies HTTPS port %d to Admiral", f.https[0]))
	}
	if len(f.https) > 0 && name != "" {
		u := "https://" + name
		if f.https[0] != 443 {
			u += ":" + strconv.Itoa(f.https[0])
		}
		rep.URL = u + "/"
	}
	if len(f.https) == 0 || f.https[0] != 443 {
		rep.Holder = holder443(&cfg)
	}
	if len(f.funnel) > 0 {
		rep.Funnel = true
		rep.add(CheckFunnel, false, "Funnel is on for Admiral's port, which makes it public; turn it off with: "+strings.Join(f.funnel, "; "))
	} else {
		rep.add(CheckFunnel, true, "Funnel is off")
	}
	return rep
}

// ServeArgs is the `tailscale` command line that serves Admiral's port over HTTPS.
func ServeArgs(port string) []string {
	return []string{"serve", "--bg", "--https=443", "http://127.0.0.1:" + port}
}

// ServeHint is ServeArgs as a command to paste.
func ServeHint(port string) string { return "tailscale " + strings.Join(ServeArgs(port), " ") }

func (r *Report) add(id string, ok bool, detail string) {
	r.Checks = append(r.Checks, Check{ID: id, OK: ok, Detail: detail})
}

func describeState(s string) string {
	switch s {
	case "NeedsLogin":
		return "logged out"
	case "":
		return "not running"
	}
	return "not running (" + s + ")"
}

type found struct {
	https  []int    // HTTPS ports whose handlers proxy to Admiral (443 first)
	rawTCP bool     // a TCP forward reaches Admiral's port
	funnel []string // one off-command or instruction per Funnel found, sorted
}

// findAdmiral looks for what reaches Admiral's loopback port. AllowFunnel is keyed by the same host:port as
// Web; a TCP forward has no Web entry, so any AllowFunnel key with its port counts. A foreground serve carries
// its own nested config.
func findAdmiral(cfg *serveConfig, port string) found {
	https := map[int]bool{}
	funnels := map[string]bool{}
	var f found
	var walk func(c *serveConfig, foreground bool)
	walk = func(c *serveConfig, foreground bool) {
		funnelOn := func(p int) bool {
			for key, on := range c.AllowFunnel {
				if _, kp, err := net.SplitHostPort(key); on && err == nil && kp == strconv.Itoa(p) {
					return true
				}
			}
			return false
		}
		addFunnel := func(cmd string) {
			if foreground {
				cmd = "stop the foreground tailscale funnel process that serves it"
			}
			funnels[cmd] = true
		}
		for key, web := range c.Web {
			proxies := false
			for _, h := range web.Handlers {
				if proxiesTo(h.Proxy, port) {
					proxies = true
				}
			}
			_, ps, err := net.SplitHostPort(key)
			p, perr := strconv.Atoi(ps)
			if !proxies || err != nil || perr != nil {
				continue
			}
			https[p] = true
			if c.AllowFunnel[key] {
				addFunnel(fmt.Sprintf("tailscale funnel --https=%d off", p))
			}
		}
		for ps, h := range c.TCP {
			p, err := strconv.Atoi(ps)
			if err != nil || h.TCPForward == "" || !forwardsTo(h.TCPForward, port) {
				continue
			}
			f.rawTCP = true
			if funnelOn(p) {
				flag := "--tcp"
				if h.TerminateTLS != "" {
					flag = "--tls-terminated-tcp"
				}
				addFunnel(fmt.Sprintf("tailscale funnel %s=%d off", flag, p))
			}
		}
		for _, fg := range c.Foreground {
			if fg != nil {
				walk(fg, true)
			}
		}
	}
	walk(cfg, false)
	for p := range https {
		f.https = append(f.https, p)
	}
	sort.Slice(f.https, func(i, j int) bool {
		if (f.https[i] == 443) != (f.https[j] == 443) {
			return f.https[i] == 443
		}
		return f.https[i] < f.https[j]
	})
	for c := range funnels {
		f.funnel = append(f.funnel, c)
	}
	sort.Strings(f.funnel)
	return f
}

// proxiesTo reports whether a serve proxy target is Admiral's port on loopback.
func proxiesTo(target, port string) bool {
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	return loopback(u.Hostname()) && u.Port() == port
}

// forwardsTo is proxiesTo for a TCPForward, an IP:port.
func forwardsTo(forward, port string) bool {
	h, p, err := net.SplitHostPort(forward)
	return err == nil && loopback(h) && p == port
}

func loopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// holder443 describes what holds HTTPS port 443, or returns "" when nothing does.
func holder443(cfg *serveConfig) string {
	for key, web := range cfg.Web {
		if _, p, err := net.SplitHostPort(key); err != nil || p != "443" {
			continue
		}
		var what []string
		for path, h := range web.Handlers {
			switch {
			case h.Proxy != "":
				what = append(what, path+" -> "+h.Proxy)
			case h.Path != "":
				what = append(what, path+" serves "+h.Path)
			default:
				what = append(what, path+" serves text")
			}
		}
		sort.Strings(what)
		return strings.Join(what, ", ")
	}
	if t, ok := cfg.TCP["443"]; ok {
		if t.TCPForward != "" {
			return "TCP forward to " + t.TCPForward
		}
		return "a TCP handler"
	}
	return ""
}

// EnableServe runs the fixed serve command on port 443 under ServeTimeout, which ServeTimeoutEnv can shorten.
func EnableServe(ctx context.Context, port string) error {
	_, err := runCLI(ctx, delay.From(ServeTimeoutEnv, ServeTimeout), true, ServeArgs(port)...)
	return err
}

// Preflight is what setup needs to know before it changes anything.
type Preflight struct {
	// Checks are running, HTTPS and MagicDNS, and Funnel, in that order.
	Checks []Check
	Login  string
	// Serving is true when serve already proxies to Admiral's port, so there is nothing to set up.
	Serving bool
	// Holder is what holds 443 when it isn't Admiral; setup never replaces it.
	Holder string
}

// Failed returns the first failed check, if any.
func (p Preflight) Failed() (Check, bool) { return Report{Checks: p.Checks}.Failed() }

// Prepare reads Tailscale without changing it.
func Prepare(ctx context.Context, port string) Preflight {
	rep := Inspect(ctx, port)
	p := Preflight{Login: rep.Login, Holder: rep.Holder}
	for _, c := range rep.Checks {
		switch c.ID {
		case CheckServe:
			p.Serving = c.OK
			continue
		case CheckFunnel:
			if rep.ServeErr != "" {
				c.Detail = rep.ServeErr
			}
		}
		p.Checks = append(p.Checks, c)
	}
	return p
}
