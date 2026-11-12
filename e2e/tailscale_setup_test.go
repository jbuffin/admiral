package e2e

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `admiral setup` turns Tailscale mode on through a fake tailscale CLI: checks, the CT warning, serve, then the config.

const adm = "7420" // the port setup proxies to: Admiral's default

const serveArgv = "serve --bg --https=443 http://127.0.0.1:" + adm

type setupRig struct {
	*world
	ts *tsRig
}

func newSetupRig(t *testing.T) *setupRig {
	t.Helper()
	w := newWorld(t)
	r := &tsRig{t: t, dir: t.TempDir(), home: w.home, port: adm}
	r.writeStatus(tsStatus(nil))
	r.writeServe(map[string]any{}) // serve is not set up yet
	return &setupRig{world: w, ts: r}
}

// run starts `admiral setup` with the fake tailscale first on PATH.
func (s *setupRig) run(stdin string, args ...string) cliResult {
	s.t.Helper()
	fake, err := filepath.Abs(filepath.Join("testdata", "faketailscale"))
	if err != nil {
		s.t.Fatal(err)
	}
	return s.admiralWith(cliOpts{
		path:  []string{fake},
		env:   []string{"FAKETS_DIR=" + s.ts.dir, "ADMIRAL_TAILSCALE_SERVE_TIMEOUT=2s"},
		stdin: stdin,
	}, append([]string{"setup"}, args...)...)
}

// remoteAnswers is an answers file with a top-level remote section and, when fleets is set, the fleets too.
func (s *setupRig) remoteAnswers(remote string, fleets string) string {
	body := `{"remote":` + remote
	if fleets != "" {
		body = `{"fleets":{` + fleets + `},"remote":` + remote
	}
	p := filepath.Join(s.root, "answers-remote.json")
	s.writeRaw(p, body+"}", 0o600)
	return p
}

// serveCalls are the `serve --bg ...` lines the fake tailscale was run with.
func (s *setupRig) serveCalls() []string {
	var out []string
	for _, l := range strings.Split(s.ts.calls(), "\n") {
		if strings.HasPrefix(l, "serve --bg") {
			out = append(out, l)
		}
	}
	return out
}

type remoteSaved struct {
	Tailscale     bool     `json:"tailscale"`
	AllowedLogins []string `json:"allowedLogins"`
}

func (s *setupRig) savedRemote() remoteSaved {
	s.t.Helper()
	var out remoteSaved
	raw, ok := s.readConfig().top["remote"]
	if !ok {
		s.t.Fatalf("config.json has no remote section:\n%s", s.configBytes())
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		s.t.Fatalf("remote: %v: %s", err, raw)
	}
	return out
}

// setupLines are the output lines that mention all the words, case-insensitively.
func setupLines(out string, words ...string) []string {
	var got []string
next:
	for _, l := range strings.Split(out, "\n") {
		low := strings.ToLower(l)
		for _, w := range words {
			if !strings.Contains(low, strings.ToLower(w)) {
				continue next
			}
		}
		got = append(got, l)
	}
	return got
}

const tsOn = `{"tailscale":true}`

// With everything ready, setup prints one line per check in order, the CT warning, runs exactly one serve with the fixed argv,
// then writes the mode and the Mac's own login next to the fleets, all in one diff.
func TestSetupTurnsOnTailscaleMode(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	claude := s.fakeExe2("claude")
	file := s.remoteAnswers(tsOn, `"w":{"name":"W","search_roots":["~/code"],"claude":{"executable":`+quote(claude)+`,"env":{"CLAUDE_CONFIG_DIR":"~/.claude-w"}}}`)

	r := s.run("", "--answers", file, "--yes")
	s.expectExit(r, 0, "setup with remote.tailscale and --yes")

	if got := s.serveCalls(); len(got) != 1 || got[0] != serveArgv {
		t.Errorf("serve calls = %q, want exactly [%q]", got, serveArgv)
	}
	c := s.readConfig()
	if strings.Join(c.fleetOrder, ",") != "w" {
		t.Errorf("fleets = %v, want [w]: the fleets and remote are written together", c.fleetOrder)
	}
	if got := s.savedRemote(); !got.Tailscale || len(got.AllowedLogins) != 1 || got.AllowedLogins[0] != tsLogin {
		t.Errorf("remote = %+v, want tailscale on and %q allowed", got, tsLogin)
	}

	// The checks print as passed lines in the order running, HTTPS and MagicDNS, Funnel.
	pos := func(word string) int {
		off := 0
		for _, l := range strings.Split(r.stdout, "\n") {
			if low := strings.ToLower(l); strings.Contains(low, "passed") && strings.Contains(low, word) {
				return off
			}
			off += len(l) + 1
		}
		return -1
	}
	run, dns, funnel := pos("running"), pos("magicdns"), pos("funnel")
	if run < 0 || dns <= run || funnel <= dns {
		t.Errorf("want passed lines for running, MagicDNS and Funnel in that order (at %d, %d, %d) in:\n%s", run, dns, funnel, r.stdout)
	}

	// The CT warning comes before the diff that setup asks to write.
	low := strings.ToLower(r.stdout)
	ct := strings.Index(low, "certificate transparency")
	diff := strings.Index(low, "changes to config.json:")
	if ct < 0 || diff < 0 || ct > diff {
		t.Errorf("want the Certificate Transparency warning before the diff (at %d and %d) in:\n%s", ct, diff, r.stdout)
	}
	if lines := setupLines(r.stdout, "certificate transparency", "ts.net"); len(lines) == 0 {
		t.Errorf("the warning should say the ts.net name goes into public CT logs:\n%s", r.stdout)
	}
	var fleetAdded, remoteAdded bool
	for _, l := range strings.Split(r.stdout, "\n") {
		fleetAdded = fleetAdded || (strings.HasPrefix(l, "+") && strings.Contains(l, `"w"`))
		remoteAdded = remoteAdded || (strings.HasPrefix(l, "+") && strings.Contains(l, tsLogin))
	}
	if !fleetAdded || !remoteAdded {
		t.Errorf("the one diff should add the fleet and the login (got fleet %t, login %t):\n%s", fleetAdded, remoteAdded, r.stdout)
	}
}

// The setup that asks: the mode defaults to no, and an Enter changes nothing and runs no tailscale.
func TestSetupAsksForTailscaleModeAndDefaultsToNo(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	// No fleet key (Enter ends the fleets), then Enter at the Tailscale question.
	r := s.run("\n\n")
	s.expectExit(r, 0, "interactive setup, Enter at every question")
	if len(setupLines(r.stdout, "tailscale mode", "[y/N]")) == 0 {
		t.Errorf("setup never asked about Tailscale mode:\n%s", r.stdout)
	}
	if c := s.ts.calls(); c != "" {
		t.Errorf("tailscale ran after a default answer: %q", c)
	}
	s.requireNoConfig()
}

// Yes to the mode, yes to go on after the CT warning, yes to write.
func TestSetupInteractiveTurnsOnTailscaleMode(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	r := s.run("\ny\ny\ny\n")
	s.expectExit(r, 0, "interactive setup, yes to the mode")
	if got := s.serveCalls(); len(got) != 1 || got[0] != serveArgv {
		t.Errorf("serve calls = %q, want [%q]", got, serveArgv)
	}
	if got := s.savedRemote(); !got.Tailscale || len(got.AllowedLogins) != 1 || got.AllowedLogins[0] != tsLogin {
		t.Errorf("remote = %+v", got)
	}
}

// Declining at the CT warning stops before serve and the write.
func TestSetupStopsWhenTheCTWarningIsDeclined(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	r := s.run("\ny\nn\n")
	s.expectExit(r, 0, "interactive setup, no after the CT warning")
	if !strings.Contains(strings.ToLower(r.stdout), "certificate transparency") {
		t.Errorf("no CT warning in:\n%s", r.stdout)
	}
	if got := s.serveCalls(); len(got) != 0 {
		t.Errorf("serve ran after the warning was declined: %q", got)
	}
	s.requireNoConfig()
}

// A failed check stops setup with what to fix: nothing is written and serve never runs.
func TestSetupStopsOnAFailedCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(m map[string]any)
		want string // lower case, in the output
	}{
		{"HTTPS off", func(m map[string]any) { m["CertDomains"] = []string{} }, "admin console"},
		{"MagicDNS off", func(m map[string]any) { m["CurrentTailnet"].(map[string]any)["MagicDNSEnabled"] = false }, "admin console"},
		{"logged out", func(m map[string]any) { m["BackendState"] = "NeedsLogin" }, "logged out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := newSetupRig(t)
			s.ts.writeStatus(tsStatus(c.edit))
			file := s.remoteAnswers(tsOn, `"w":{"name":"W","search_roots":["~/code"],"claude":{"env":{"CLAUDE_CONFIG_DIR":"~/.claude-w"}}}`)
			r := s.run("", "--answers", file, "--yes")
			if len(setupLines(r.stdout, "failed")) == 0 {
				t.Errorf("want a `failed` line for the check in:\n%s", r.both())
			}
			if !strings.Contains(strings.ToLower(r.both()), c.want) {
				t.Errorf("output should say %q:\n%s", c.want, r.both())
			}
			if got := s.serveCalls(); len(got) != 0 {
				t.Errorf("serve ran with a failed check: %q", got)
			}
			s.requireNoConfig()
		})
	}
}

// Funnel already on for Admiral's port: setup refuses, says how to turn it off, runs no serve and writes nothing.
func TestSetupRefusesWhenFunnelIsOn(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	s.ts.writeServe(s.ts.serve(443, true))
	file := s.remoteAnswers(tsOn, "")
	r := s.run("", "--answers", file, "--yes")
	if r.code == 0 {
		t.Errorf("exit 0 with Funnel on:\n%s", r.both())
	}
	if lines := setupLines(r.both(), "funnel", "off"); len(lines) == 0 {
		t.Errorf("want a line saying Funnel is on and how to turn it off:\n%s", r.both())
	}
	if got := s.serveCalls(); len(got) != 0 {
		t.Errorf("serve ran: %q", got)
	}
	s.requireNoConfig()
}

// A handler that already proxies to Admiral's port is left as it is, and the config still gets the mode.
func TestSetupLeavesAnExistingHandlerAlone(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	s.ts.writeServe(s.ts.serve(443, false))
	r := s.run("", "--answers", s.remoteAnswers(tsOn, ""), "--yes")
	s.expectExit(r, 0, "setup with serve already set up")
	if got := s.serveCalls(); len(got) != 0 {
		t.Errorf("serve ran over an existing handler: %q", got)
	}
	if !strings.Contains(strings.ToLower(r.stdout), "already") {
		t.Errorf("setup should say serve is already set up:\n%s", r.stdout)
	}
	if got := s.savedRemote(); !got.Tailscale || len(got.AllowedLogins) != 1 || got.AllowedLogins[0] != tsLogin {
		t.Errorf("remote = %+v", got)
	}
}

// 443 held by another service is never replaced: setup stops, names what holds it and prints the command for another port.
func TestSetupDoesNotTouchABusy443(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	s.ts.writeServe(proxyTo(443, "9999", false))
	r := s.run("", "--answers", s.remoteAnswers(tsOn, ""), "--yes")
	if r.code == 0 {
		t.Errorf("exit 0 with 443 taken:\n%s", r.both())
	}
	if got := s.serveCalls(); len(got) != 0 {
		t.Errorf("serve ran with 443 taken: %q", got)
	}
	if !strings.Contains(r.both(), "127.0.0.1:9999") {
		t.Errorf("setup should name what holds 443 (127.0.0.1:9999):\n%s", r.both())
	}
	hint := regexp.MustCompile(`tailscale serve --bg --https=(\d+) http://127\.0\.0\.1:` + adm)
	if m := hint.FindStringSubmatch(r.both()); m == nil || m[1] == "443" {
		t.Errorf("want the command for another HTTPS port, got %v in:\n%s", m, r.both())
	}
	s.requireNoConfig()
}

// A serve that fails or hangs is a failed check: the CLI's message is printed, and not even the fleets are written.
func TestSetupWritesNothingWhenServeFails(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(s *setupRig)
		want  string
	}{
		{"non-zero exit", func(s *setupRig) {
			s.ts.put("servebg.exit", "1")
			s.ts.put("servebg.err", "Serve is not enabled on your tailnet.")
		}, "Serve is not enabled on your tailnet."},
		{"hang", func(s *setupRig) { s.ts.put("servebg.sleep", "30") }, "timed out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := newSetupRig(t)
			c.setup(s)
			file := s.remoteAnswers(tsOn, `"w":{"name":"W","search_roots":["~/code"],"claude":{"env":{"CLAUDE_CONFIG_DIR":"~/.claude-w"}}}`)
			r := s.run("", "--answers", file, "--yes")
			if r.code == 0 {
				t.Errorf("exit 0 after a failed serve:\n%s", r.both())
			}
			if got := s.serveCalls(); len(got) != 1 || got[0] != serveArgv {
				t.Errorf("serve calls = %q, want one with the fixed argv", got)
			}
			if !strings.Contains(r.both(), c.want) {
				t.Errorf("want %q in the output:\n%s", c.want, r.both())
			}
			if len(setupLines(r.stdout, "failed")) == 0 {
				t.Errorf("want a `failed` line:\n%s", r.stdout)
			}
			s.requireNoConfig()
		})
	}
}

// The Mac's login joins the logins already allowed, the answers file's allowedLogins are added, and nobody appears twice.
func TestSetupAddsTheMacsLoginAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	s.writeRaw(configPath(s.home), `{"version":1,"fleets":{},"remote":{"tailscale":false,"allowedLogins":["bob@example.com"]}}`+"\n", 0o600)
	r := s.run("", "--answers", s.remoteAnswers(`{"tailscale":true,"allowedLogins":["carol@example.com","`+tsLogin+`"]}`, ""), "--yes")
	s.expectExit(r, 0, "setup with allowedLogins")
	got := s.savedRemote()
	want := map[string]bool{"bob@example.com": true, "carol@example.com": true, tsLogin: true}
	seen := map[string]int{}
	for _, l := range got.AllowedLogins {
		seen[l]++
	}
	for l := range want {
		if seen[l] != 1 {
			t.Errorf("%s appears %d times in %v, want once", l, seen[l], got.AllowedLogins)
		}
	}
	if !got.Tailscale || len(got.AllowedLogins) != len(want) {
		t.Errorf("remote = %+v, want the mode on and exactly %v", got, want)
	}
}

// --dry-run shows the checks, the serve command and the config diff, and runs no serve and writes nothing.
func TestSetupDryRunRunsNoServe(t *testing.T) {
	t.Parallel()
	s := newSetupRig(t)
	r := s.run("", "--answers", s.remoteAnswers(tsOn, ""), "--dry-run")
	s.expectExit(r, 0, "setup --dry-run")
	if got := s.serveCalls(); len(got) != 0 {
		t.Errorf("--dry-run ran serve: %q", got)
	}
	s.requireNoConfig()
	if !strings.Contains(r.stdout, "tailscale "+serveArgv) {
		t.Errorf("--dry-run should print the command it would run, `tailscale %s`:\n%s", serveArgv, r.stdout)
	}
	if len(setupLines(r.stdout, "passed")) < 3 {
		t.Errorf("--dry-run should print the checks:\n%s", r.stdout)
	}
	var added bool
	for _, l := range strings.Split(r.stdout, "\n") {
		added = added || (strings.HasPrefix(l, "+") && strings.Contains(l, tsLogin))
	}
	if !added {
		t.Errorf("--dry-run should print the config diff with the login:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "Dry run: nothing was changed.") {
		t.Errorf("no dry run notice:\n%s", r.stdout)
	}
}
