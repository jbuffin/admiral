package tailscale

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	host     = "fake-mac.tailnet-fake.ts.net"
	okStatus = `{"BackendState":"Running","Self":{"DNSName":"` + host + `.","UserID":1},
"CurrentTailnet":{"MagicDNSEnabled":true},"CertDomains":["` + host + `"],
"User":{"1":{"LoginName":"alice@example.com"}}}`
)

func web(p, proxy string) string {
	return `"Web":{"` + host + `:` + p + `":{"Handlers":{"/":{"Proxy":"` + proxy + `"}}}}`
}

func TestEvaluateServeRules(t *testing.T) {
	serveHint := "tailscale serve --bg --https=443 http://127.0.0.1:7420"
	cases := []struct {
		name       string
		serve      string
		wantURL    string
		wantFunnel string // the funnel-off check's full detail when Funnel is on
		wantServe  string // substring of the serve check's detail
	}{
		{"443 proxy", `{` + web("443", "http://127.0.0.1:7420") + `}`, "https://" + host + "/", "", "proxies HTTPS port 443"},
		{"other port in the URL", `{` + web("8443", "http://localhost:7420") + `}`, "https://" + host + ":8443/", "", "proxies HTTPS port 8443"},
		{"proxy to another port", `{` + web("443", "http://127.0.0.1:9999") + `}`, "", "", "run: " + serveHint},
		{"funnel on the same key", `{` + web("443", "http://127.0.0.1:7420") + `,"AllowFunnel":{"` + host + `:443":true}}`, "https://" + host + "/",
			"Funnel is on for Admiral's port, which makes it public; turn it off with: tailscale funnel --https=443 off", ""},
		{"funnel on a different key", `{` + web("443", "http://127.0.0.1:7420") + `,"AllowFunnel":{"` + host + `:8443":true}}`, "https://" + host + "/", "", ""},
		{"foreground funnel", `{"Foreground":{"s1":{` + web("8443", "127.0.0.1:7420") + `,"AllowFunnel":{"` + host + `:8443":true}}}}`, "https://" + host + ":8443/",
			"Funnel is on for Admiral's port, which makes it public; turn it off with: stop the foreground tailscale funnel process that serves it", ""},
		{"tcp funnel", `{"TCP":{"10000":{"TCPForward":"127.0.0.1:7420"}},"AllowFunnel":{"` + host + `:10000":true}}`, "",
			"Funnel is on for Admiral's port, which makes it public; turn it off with: tailscale funnel --tcp=10000 off", "raw TCP forward"},
		{"tls-terminated tcp funnel", `{"TCP":{"8443":{"TCPForward":"127.0.0.1:7420","TerminateTLS":"` + host + `"}},"AllowFunnel":{"` + host + `:8443":true}}`, "",
			"Funnel is on for Admiral's port, which makes it public; turn it off with: tailscale funnel --tls-terminated-tcp=8443 off", "raw TCP forward"},
		{"foreground tcp funnel", `{"Foreground":{"s1":{"TCP":{"10000":{"TCPForward":"localhost:7420"}},"AllowFunnel":{"` + host + `:10000":true}}}}`, "",
			"Funnel is on for Admiral's port, which makes it public; turn it off with: stop the foreground tailscale funnel process that serves it", "raw TCP forward"},
		{"tcp forward without funnel", `{"TCP":{"10000":{"TCPForward":"127.0.0.1:7420"}}}`, "", "",
			"a raw TCP forward to Admiral's port bypasses Admiral's tailnet checks; replace it with: " + serveHint},
		{"tcp forward to another port", `{"TCP":{"10000":{"TCPForward":"127.0.0.1:9999"}},"AllowFunnel":{"` + host + `:10000":true}}`, "", "", "run: " + serveHint},
		{"tcp forward to another host", `{"TCP":{"10000":{"TCPForward":"10.0.0.5:7420"}},"AllowFunnel":{"` + host + `:10000":true}}`, "", "", "run: " + serveHint},
		{"nothing served", `{}`, "", "", "run: " + serveHint},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := Evaluate([]byte(okStatus), nil, []byte(c.serve), nil, "7420")
			if rep.URL != c.wantURL || rep.Funnel != (c.wantFunnel != "") {
				t.Errorf("url %q funnel %t, want %q %t", rep.URL, rep.Funnel, c.wantURL, c.wantFunnel != "")
			}
			if f, _ := rep.Check(CheckFunnel); c.wantFunnel != "" && f.Detail != c.wantFunnel {
				t.Errorf("funnel detail %q, want %q", f.Detail, c.wantFunnel)
			}
			if s, _ := rep.Check(CheckServe); !strings.Contains(s.Detail, c.wantServe) {
				t.Errorf("serve detail %q, want it to contain %q", s.Detail, c.wantServe)
			}
		})
	}
}

func TestEvaluateStatusChecks(t *testing.T) {
	cases := []struct {
		name, status, failID, want string
	}{
		{"logged out", strings.Replace(okStatus, `"Running"`, `"NeedsLogin"`, 1), CheckRunning, "logged out"},
		{"HTTPS off", strings.Replace(okStatus, `["`+host+`"]`, `[]`, 1), CheckDNSHTTPS, "HTTPS certificates are off"},
		{"MagicDNS off", strings.Replace(okStatus, `"MagicDNSEnabled":true`, `"MagicDNSEnabled":false`, 1), CheckDNSHTTPS, "MagicDNS is off"},
	}
	serve := `{` + web("443", "http://127.0.0.1:7420") + `}`
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := Evaluate([]byte(c.status), nil, []byte(serve), nil, "7420")
			var failed []string
			for _, ch := range rep.Checks {
				if !ch.OK {
					failed = append(failed, ch.ID)
				}
			}
			if len(failed) != 1 || failed[0] != c.failID {
				t.Fatalf("failed checks %v, want only %s", failed, c.failID)
			}
			if ch, _ := rep.Check(c.failID); !strings.Contains(ch.Detail, c.want) {
				t.Errorf("detail %q, want %q", ch.Detail, c.want)
			}
			if c.failID == CheckRunning && rep.URL != "" {
				t.Errorf("url %q for a logged-out node", rep.URL)
			}
		})
	}
}

// Funnel is reported even when the status call failed, so the refusal can always name it.
func TestEvaluateFunnelSurvivesAStatusFailure(t *testing.T) {
	serve := `{` + web("443", "http://127.0.0.1:7420") + `,"AllowFunnel":{"` + host + `:443":true}}`
	rep := Evaluate(nil, context.DeadlineExceeded, []byte(serve), nil, "7420")
	if f, _ := rep.Check(CheckFunnel); !rep.Funnel || f.OK {
		t.Errorf("funnel check %+v", f)
	}
	if first, _ := rep.Failed(); first.ID != CheckRunning {
		t.Errorf("first failed check %s, want running (the one the refusal must not print)", first.ID)
	}
}

// fakeOnPath puts the e2e fake tailscale first on PATH and returns its state dir.
func fakeOnPath(t *testing.T) string {
	t.Helper()
	fake, err := filepath.Abs(filepath.Join("..", "..", "e2e", "testdata", "faketailscale"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("PATH", fake+":"+os.Getenv("PATH"))
	t.Setenv("FAKETS_DIR", dir)
	return dir
}

func put(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInspectCLIFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{"non-zero exit", func(t *testing.T, d string) {
			put(t, d, "status.exit", "1")
			put(t, d, "status.err", "failed to connect to local tailscaled\nsecond line")
		}, "tailscale status --json failed: failed to connect to local tailscaled"},
		{"unparsable output", func(t *testing.T, d string) { put(t, d, "status.json", "not json") }, "not JSON"},
		{"timeout", func(t *testing.T, d string) { put(t, d, "sleep", "60") }, "timed out"},
		{"missing binary", func(t *testing.T, d string) { t.Setenv("PATH", t.TempDir()) }, "not installed"},
	}
	old := timeout
	timeout = 300 * time.Millisecond
	t.Cleanup(func() { timeout = old })
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := fakeOnPath(t)
			put(t, dir, "status.json", okStatus)
			put(t, dir, "serve.json", `{}`)
			c.setup(t, dir)
			start := time.Now()
			rep := Inspect(context.Background(), "7420")
			if time.Since(start) > 3*time.Second {
				t.Errorf("Inspect took %s", time.Since(start))
			}
			run, _ := rep.Check(CheckRunning)
			if run.OK || !strings.Contains(run.Detail, c.want) || strings.Contains(run.Detail, "\n") {
				t.Errorf("running check %+v, want a one-line failure containing %q", run, c.want)
			}
		})
	}
}

func TestInspectCancelled(t *testing.T) {
	dir := fakeOnPath(t)
	put(t, dir, "sleep", "60")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep := Inspect(ctx, "7420")
	if run, _ := rep.Check(CheckRunning); !strings.Contains(run.Detail, "cancelled") {
		t.Errorf("running check %+v, want cancelled", run)
	}
}
