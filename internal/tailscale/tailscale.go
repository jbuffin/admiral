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
)

// Timeout bounds each CLI call: `tailscale serve` has hung for minutes on a tailnet with Serve disabled.
const Timeout = 4 * time.Second

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
	// Funnel is true when Funnel is on for a handler that proxies to Admiral's port.
	Funnel bool
	// Login is this Mac's tailnet login, empty when unknown.
	Login string
}

// Failed returns the first failed check, if any.
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
	TCP map[string]struct{ HTTPS bool }
	Web map[string]struct {
		Handlers map[string]struct{ Proxy string }
	}
	AllowFunnel map[string]bool
	Foreground  map[string]*serveConfig
}

func run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tailscale", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, fmt.Errorf("tailscale %s timed out after %s", strings.Join(args, " "), Timeout)
		case errors.Is(err, exec.ErrNotFound):
			return nil, fmt.Errorf("tailscale is not installed or not on PATH")
		}
		msg := firstLine(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("tailscale %s failed: %s", strings.Join(args, " "), msg)
	}
	return out.Bytes(), nil
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
	go func() { defer wg.Done(); statusOut, statusErr = run(ctx, "status", "--json") }()
	go func() { defer wg.Done(); serve, serveErr = run(ctx, "serve", "status", "--json") }()
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
		rep.add(CheckServe, false, serveWhy)
		rep.add(CheckFunnel, false, "unknown while tailscale serve status is unavailable")
		return rep
	}

	ports, funnel := proxyPorts(&cfg, port)
	if len(ports) == 0 {
		rep.add(CheckServe, false, fmt.Sprintf("tailscale serve does not proxy to Admiral's port; run: tailscale serve --bg --https=443 %s", port))
	} else {
		rep.add(CheckServe, true, fmt.Sprintf("tailscale serve proxies HTTPS port %d to Admiral", ports[0]))
		if name != "" {
			u := "https://" + name
			if ports[0] != 443 {
				u += ":" + strconv.Itoa(ports[0])
			}
			rep.URL = u + "/"
		}
	}
	rep.Funnel = funnel
	if funnel {
		rep.add(CheckFunnel, false, "Funnel is on for Admiral's port, which makes it public; turn it off with: tailscale funnel --https=443 off")
	} else {
		rep.add(CheckFunnel, true, "Funnel is off")
	}
	return rep
}

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

// proxyPorts returns the HTTPS ports whose handlers proxy to Admiral's loopback port (443 first), and
// whether Funnel is on for any of them. AllowFunnel is keyed by the same host:port as Web, and a
// foreground serve carries its own nested config.
func proxyPorts(cfg *serveConfig, port string) ([]int, bool) {
	seen := map[int]bool{}
	funnel := false
	var walk func(c *serveConfig)
	walk = func(c *serveConfig) {
		for key, web := range c.Web {
			proxies := false
			for _, h := range web.Handlers {
				if proxiesTo(h.Proxy, port) {
					proxies = true
				}
			}
			if !proxies {
				continue
			}
			if _, p, err := net.SplitHostPort(key); err == nil {
				if n, err := strconv.Atoi(p); err == nil {
					seen[n] = true
				}
			}
			if c.AllowFunnel[key] {
				funnel = true
			}
		}
		for _, fg := range c.Foreground {
			if fg != nil {
				walk(fg)
			}
		}
	}
	walk(cfg)
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == 443) != (out[j] == 443) {
			return out[i] == 443
		}
		return out[i] < out[j]
	})
	return out, funnel
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
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return false
	}
	return u.Port() == port
}
