package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---- `admiral service`, as these tests assume it ----
//
// install    writes $HOME/Library/LaunchAgents/dev.jasonbuffin.admiral.plist
//            and $ADMIRAL_HOME/logs/, runs `launchctl bootout` and then
//            `launchctl bootstrap gui/<uid> <plist>`, and prints the server's
//            "On other devices: http://<ip>:7420/?token=<token>" line. A
//            bootout that fails (the job isn't loaded) is not an error; a
//            failing bootstrap is: exit 1 with launchctl's stderr shown.
// uninstall  `launchctl bootout`, then removes the plist.
// status     first stdout line "Service: not installed" (no plist),
//            "Service: installed, not loaded" or "Service: installed, loaded"
//            (the exit code of `launchctl print gui/<uid>/<label>`: 0 loaded;
//            its output is never read), then, when installed, the token line.
//
// launchctl and the login shell are fakes: nothing here loads a job.

const fallbackPath = "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

type svc struct {
	t           *testing.T
	home, ah    string // HOME, ADMIRAL_HOME ("" = unset)
	fakes       string // directory with the fake launchctl (and brew)
	record      string // the fake launchctl's argv log
	shell       string
	binary      string
	printExit   string
	printOut    string
	bootoutExit string
	bootstrap   string // exit code of bootstrap
}

func newSvc(t *testing.T) *svc {
	t.Helper()
	s := &svc{t: t, home: t.TempDir(), fakes: t.TempDir(), printExit: "0", bootoutExit: "0", bootstrap: "0", binary: binaryPath(t)}
	s.ah = filepath.Join(t.TempDir(), "admiral-home")
	s.record = filepath.Join(s.fakes, "launchctl.log")
	s.shell = filepath.Join(s.fakes, "fakeshell")
	writeExec(t, s.shell, "#!/bin/sh\n[ \"$1\" = -lc ] || exit 3\nprintf %s '/captured/one:/captured/two'\n")
	return s
}

func (s *svc) writeLaunchctl() {
	// One record per call: the arguments, then a line "--".
	writeExec(s.t, filepath.Join(s.fakes, "launchctl"), `#!/bin/sh
{ printf '%s\n' "$@"; echo --; } >> `+s.record+`
case "$1" in
bootout) echo "Boot-out failed" >&2; exit `+s.bootoutExit+`;;
bootstrap) [ `+s.bootstrap+` = 0 ] || echo "Bootstrap failed: 5: Input/output error" >&2; exit `+s.bootstrap+`;;
print) echo '`+s.printOut+`'; exit `+s.printExit+`;;
esac
exit 0
`)
}

func (s *svc) env() []string {
	s.writeLaunchctl()
	env := []string{"HOME=" + s.home, "SHELL=" + s.shell, "PATH=" + s.fakes + ":/usr/bin:/bin"}
	if s.ah != "" {
		env = append(env, "ADMIRAL_HOME="+s.ah)
	}
	return env
}

func (s *svc) admiral(args ...string) (stdout, stderr string, code int) {
	s.t.Helper()
	return run(s.t, runOpts{dir: s.home, env: s.env()}, s.binary, append([]string{"service"}, args...)...)
}

func (s *svc) plistPath() string {
	return filepath.Join(s.home, "Library", "LaunchAgents", serviceLabel+".plist")
}

// calls reads the fake launchctl's log.
func (s *svc) calls() [][]string {
	b, _ := os.ReadFile(s.record)
	var out [][]string
	var cur []string
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		switch l {
		case "":
		case "--":
			out = append(out, cur)
			cur = nil
		default:
			cur = append(cur, l)
		}
	}
	return out
}

func (s *svc) plist() map[string]any {
	s.t.Helper()
	if out, err := runLint(s.plistPath()); err != nil {
		s.t.Fatalf("plutil -lint: %v\n%s", err, out)
	}
	out, _, code := run(s.t, runOpts{}, "plutil", "-convert", "json", "-o", "-", s.plistPath())
	var m map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil {
		s.t.Fatalf("plutil -convert json exit %d, output %q", code, out)
	}
	return m
}

func runLint(p string) (string, error) {
	out, err := execOutput("plutil", "-lint", p)
	return out, err
}

func same(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return a == b || (errA == nil && errB == nil && ra == rb)
}

func (s *svc) wantBootoutThenBootstrap(calls [][]string) {
	t := s.t
	t.Helper()
	uid := "gui/" + strconv.Itoa(os.Getuid())
	if len(calls) != 2 {
		t.Fatalf("launchctl calls = %v, want a bootout then a bootstrap", calls)
	}
	bo, bs := calls[0], calls[1]
	if bo[0] != "bootout" || len(bo) < 2 || !(bo[1] == uid+"/"+serviceLabel || (bo[1] == uid && len(bo) == 3 && same(t, bo[2], s.plistPath()))) {
		t.Errorf("first call %v, want bootout %s/%s (or bootout %s <plist>)", bo, uid, serviceLabel, uid)
	}
	if len(bs) != 3 || bs[0] != "bootstrap" || bs[1] != uid || !same(t, bs[2], s.plistPath()) {
		t.Errorf("second call %v, want bootstrap %s %s", bs, uid, s.plistPath())
	}
}

// S4: install writes a valid plist with every key the spec lists, creates the
// log folder, boots out then bootstraps, and prints the token URL.
func TestServiceS4_InstallWritesThePlistAndBootstraps(t *testing.T) {
	s := newSvc(t)
	s.bootoutExit = "113" // not loaded yet: install carries on
	out, errOut, code := s.admiral("install")
	if code != 0 {
		t.Fatalf("service install exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	p := s.plist()

	if p["Label"] != serviceLabel {
		t.Errorf("Label = %v, want %s", p["Label"], serviceLabel)
	}
	args, _ := p["ProgramArguments"].([]any)
	if len(args) == 0 {
		t.Fatalf("ProgramArguments = %v", p["ProgramArguments"])
	}
	if prog, _ := args[0].(string); !filepath.IsAbs(prog) || !same(t, prog, s.binary) {
		t.Errorf("program path %v, want the absolute path of %s", args[0], s.binary)
	}
	if p["RunAtLoad"] != true {
		t.Errorf("RunAtLoad = %v, want true", p["RunAtLoad"])
	}
	ka, _ := p["KeepAlive"].(map[string]any)
	if len(ka) != 2 || ka["SuccessfulExit"] != false || ka["Crashed"] != true {
		t.Errorf("KeepAlive = %v, want {SuccessfulExit: false, Crashed: true}", p["KeepAlive"])
	}
	if p["ProcessType"] != "Interactive" {
		t.Errorf("ProcessType = %v, want Interactive", p["ProcessType"])
	}
	wantLog := filepath.Join(s.ah, "logs", "admiral.log")
	for _, k := range []string{"StandardOutPath", "StandardErrorPath"} {
		v, _ := p[k].(string)
		if !filepath.IsAbs(v) || !same(t, filepath.Dir(v), filepath.Dir(wantLog)) || filepath.Base(v) != "admiral.log" {
			t.Errorf("%s = %v, want %s", k, p[k], wantLog)
		}
	}
	if fi, err := os.Stat(filepath.Join(s.ah, "logs")); err != nil || !fi.IsDir() {
		t.Errorf("install must create %s/logs (launchd doesn't): %v", s.ah, err)
	}
	env, _ := p["EnvironmentVariables"].(map[string]any)
	if env["PATH"] != "/captured/one:/captured/two" {
		t.Errorf("PATH = %v, want the login shell's /captured/one:/captured/two", env["PATH"])
	}
	if v, _ := env["ADMIRAL_HOME"].(string); !same(t, v, s.ah) {
		t.Errorf("ADMIRAL_HOME = %v, want %s", env["ADMIRAL_HOME"], s.ah)
	}
	for _, k := range []string{"StandardOutPath", "StandardErrorPath", "WorkingDirectory"} {
		if v, ok := p[k].(string); ok && (strings.HasPrefix(v, "~") || strings.Contains(v, "$")) {
			t.Errorf("%s = %q: launchd doesn't expand ~ or variables", k, v)
		}
	}

	s.wantBootoutThenBootstrap(s.calls())

	token, err := os.ReadFile(filepath.Join(s.ah, "token"))
	if err != nil {
		t.Fatalf("install should leave a token to print: %v", err)
	}
	if !strings.Contains(out, "On other devices: http://") || !strings.Contains(out, "?token="+strings.TrimSpace(string(token))) {
		t.Errorf("install should print the token URL, got %q", out)
	}
}

// S4: a login shell that fails falls back to the fixed PATH; with no
// ADMIRAL_HOME the plist has none and logs under ~/.admiral.
func TestServiceS4_FallbackPathAndDefaultHome(t *testing.T) {
	s := newSvc(t)
	s.ah = ""
	writeExec(t, s.shell, "#!/bin/sh\nexit 1\n")
	if out, errOut, code := s.admiral("install"); code != 0 {
		t.Fatalf("service install exit %d\n%s%s", code, out, errOut)
	}
	p := s.plist()
	env, _ := p["EnvironmentVariables"].(map[string]any)
	if env["PATH"] != fallbackPath {
		t.Errorf("PATH = %v, want the fallback %s", env["PATH"], fallbackPath)
	}
	if _, ok := env["ADMIRAL_HOME"]; ok {
		t.Errorf("ADMIRAL_HOME was unset at install but the plist sets it: %v", env["ADMIRAL_HOME"])
	}
	if v, _ := p["StandardOutPath"].(string); !same(t, v, filepath.Join(s.home, ".admiral", "logs", "admiral.log")) {
		t.Errorf("StandardOutPath = %v, want ~/.admiral/logs/admiral.log under the temp HOME", p["StandardOutPath"])
	}
}

// S4: a second install replaces the first, a failing bootstrap is an error,
// and uninstall boots out and removes the plist.
func TestServiceS4_ReinstallBootstrapFailureAndUninstall(t *testing.T) {
	s := newSvc(t)
	for i := 1; i <= 2; i++ {
		if out, errOut, code := s.admiral("install"); code != 0 {
			t.Fatalf("install #%d exit %d\n%s%s", i, code, out, errOut)
		}
	}
	calls := s.calls()
	if len(calls) != 4 {
		t.Fatalf("two installs made %v, want bootout, bootstrap twice", calls)
	}
	s.wantBootoutThenBootstrap(calls[2:])
	s.plist()

	os.Remove(s.record)
	if out, errOut, code := s.admiral("uninstall"); code != 0 {
		t.Fatalf("uninstall exit %d\n%s%s", code, out, errOut)
	}
	if c := s.calls(); len(c) < 1 || c[0][0] != "bootout" {
		t.Errorf("uninstall should record a bootout, got %v", c)
	}
	if _, err := os.Stat(s.plistPath()); !os.IsNotExist(err) {
		t.Errorf("uninstall left the plist (%v)", err)
	}

	s.bootstrap = "5"
	if out, errOut, code := s.admiral("install"); code == 0 || !strings.Contains(errOut, "Bootstrap failed") {
		t.Errorf("a failing bootstrap must fail install and show launchctl's message: exit %d\n%s%s", code, out, errOut)
	}
}

// S4: under Homebrew the plist points at the stable prefix path, never the
// Cellar path that brew cleanup deletes.
func TestServiceS4_HomebrewInstallUsesTheStablePath(t *testing.T) {
	s := newSvc(t)
	prefix := t.TempDir()
	cellar := filepath.Join(prefix, "Cellar", "admiral", "1.0.0", "bin")
	b, err := os.ReadFile(s.binary)
	if err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(cellar, "admiral"), string(b))
	for dir, target := range map[string]string{
		filepath.Join(prefix, "bin"):            cellar,
		filepath.Join(prefix, "opt", "admiral"): filepath.Join(prefix, "Cellar", "admiral", "1.0.0"),
	} {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if filepath.Base(dir) == "bin" {
			if err := os.Symlink(filepath.Join(cellar, "admiral"), filepath.Join(mustMkdir(t, dir), "admiral")); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink(target, dir); err != nil {
			t.Fatal(err)
		}
	}
	writeExec(t, filepath.Join(s.fakes, "brew"), "#!/bin/sh\nif [ \"$2\" = admiral ]; then echo '"+prefix+"/opt/admiral'; else echo '"+prefix+"'; fi\n")
	s.binary = filepath.Join(cellar, "admiral")
	if out, errOut, code := s.admiral("install"); code != 0 {
		t.Fatalf("install exit %d\n%s%s", code, out, errOut)
	}
	args, _ := s.plist()["ProgramArguments"].([]any)
	prog, _ := args[0].(string)
	if strings.Contains(prog, "/Cellar/") || (prog != filepath.Join(prefix, "bin", "admiral") && prog != filepath.Join(prefix, "opt", "admiral", "bin", "admiral")) {
		t.Errorf("program path %q, want %s/bin/admiral or %s/opt/admiral/bin/admiral, never the Cellar", prog, prefix, prefix)
	}
}

func mustMkdir(t *testing.T, d string) string {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// S4: status reports from the plist and launchctl print's exit code, never its
// output.
func TestServiceS4_StatusUsesTheExitCodeNotTheOutput(t *testing.T) {
	s := newSvc(t)
	first := func() string {
		out, errOut, _ := s.admiral("status")
		return strings.SplitN(out+errOut, "\n", 2)[0]
	}
	if got := first(); got != "Service: not installed" {
		t.Errorf("no plist: first line %q, want \"Service: not installed\"", got)
	}
	if out, errOut, code := s.admiral("install"); code != 0 {
		t.Fatalf("install exit %d\n%s%s", code, out, errOut)
	}

	s.printExit, s.printOut = "113", "state = running" // output that lies
	if got := first(); got != "Service: installed, not loaded" {
		t.Errorf("print exit 113 with 'state = running': first line %q, want \"Service: installed, not loaded\"", got)
	}
	s.printExit, s.printOut = "0", "Could not find service" // output that lies the other way
	out, _, _ := s.admiral("status")
	if got := strings.SplitN(out, "\n", 2)[0]; got != "Service: installed, loaded" {
		t.Errorf("print exit 0: first line %q, want \"Service: installed, loaded\"", got)
	}
	if !strings.Contains(out, "On other devices: http://") || !strings.Contains(out, "?token=") {
		t.Errorf("status should print the token URL, got %q", out)
	}
	var printed bool
	for _, c := range s.calls() {
		if c[0] == "print" && len(c) == 2 && c[1] == "gui/"+strconv.Itoa(os.Getuid())+"/"+serviceLabel {
			printed = true
		}
	}
	if !printed {
		t.Errorf("status never ran launchctl print gui/<uid>/%s: %v", serviceLabel, s.calls())
	}
}
