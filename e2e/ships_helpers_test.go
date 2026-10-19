package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Helpers for the u4 scenarios (ships, launcher, state). A world is one
// temporary root holding an ADMIRAL_HOME, a TMUX_TMPDIR, a HOME, checkouts and
// the fake harnesses' recordings. The root is under /tmp and kept short: a
// unix socket path is limited to 104 bytes on macOS.

type world struct {
	t       *testing.T
	root    string
	home    string // ADMIRAL_HOME
	userHom string // HOME of Admiral and its ships
	tmuxTmp string // TMUX_TMPDIR
	work    string // the search root; checkouts are directories in it
	rec     string // FAKE_RECORD_DIR
	cue     string // FAKE_CUE
	extra   []string
	in      *fleetInst
}

// fleetInst is an instance whose environment is explicit, so a restart gets
// the same one.
type fleetInst struct {
	*instance
	env []string
}

func fakeDir(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("fakeharness")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newWorld(t *testing.T) *world {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("tmux is required: %v", err)
	}
	root, err := os.MkdirTemp("/tmp", "adm")
	if err != nil {
		t.Fatal(err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, root: root, home: filepath.Join(root, "h"),
		userHom: filepath.Join(root, "u"), tmuxTmp: filepath.Join(root, "t"),
		work: filepath.Join(root, "w"), rec: filepath.Join(root, "r"), cue: filepath.Join(root, "cue")}
	for _, d := range []string{w.userHom, w.tmuxTmp, w.work, w.rec} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	w.installCommodore()
	t.Cleanup(func() {
		// Registered before any Admiral this world starts, so it runs after they stop.
		killTmux(w.sock())
		killTmux(w.defaultSocket())
		_ = os.RemoveAll(root)
	})
	return w
}

// at returns the world with its helpers bound to t. A subtest must use it:
// a helper that fails the parent's t from the subtest's goroutine ends it with
// "subtest may have called FailNow on a parent test" and hides the message.
func (w *world) at(t *testing.T) *world {
	c := *w
	c.t = t
	return &c
}

// defaultSocket is where the "default" tmux server of this world would live.
func (w *world) defaultSocket() string {
	return filepath.Join(w.tmuxTmp, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
}

func (w *world) sock() string { return filepath.Join(w.home, "tmux.sock") }

func (w *world) baseEnv() []string {
	env := []string{}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "ADMIRAL_HOME", "HOME", "TMUX", "TMUX_PANE", "TMUX_TMPDIR", "XDG_CONFIG_HOME",
			"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLAUDE_CODE_PLUGIN_CACHE_DIR":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "ADMIRAL_HOME="+w.home, "HOME="+w.userHom, "XDG_CONFIG_HOME="+filepath.Join(w.userHom, "xdg"), "TMUX_TMPDIR="+w.tmuxTmp,
		"ADMIRAL_BASE=base", "ADMIRAL_OVER=base")
	return append(env, w.extra...)
}

// tmux runs the tmux binary against this world's Admiral socket.
func (w *world) tmux(args ...string) (string, error) {
	cmd := exec.Command("tmux", append([]string{"-S", w.sock()}, args...)...)
	cmd.Env = w.baseEnv()
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

func (w *world) mustTmux(args ...string) string {
	w.t.Helper()
	out, err := w.tmux(args...)
	if err != nil {
		w.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return out
}

// sessions lists the tmux session names on Admiral's socket (none when no
// server runs), without the hidden internal session.
func (w *world) sessions() []string {
	out, err := w.tmux("list-sessions", "-F", "#{session_name}")
	names := []string{}
	if err != nil {
		return names
	}
	for _, n := range strings.Split(out, "\n") {
		if n != "" && n != "_admiral" {
			names = append(names, n)
		}
	}
	return names
}

func (w *world) fakeExe(harness string) string { return filepath.Join(fakeDir(w.t), harness) }

func (w *world) harnessEnv() map[string]string {
	return map[string]string{
		"FAKE_RECORD_DIR": w.rec, "FAKE_CUE": w.cue,
		"FLEET_VAR": "fleet-value", "TILDE_VAR": "~/tilde", "ADMIRAL_OVER": "fleet",
	}
}

func (w *world) dock(harness string, args ...string) map[string]any {
	d := map[string]any{"executable": w.fakeExe(harness), "env": w.harnessEnv()}
	if len(args) > 0 {
		d["args"] = args
	}
	return d
}

// config is the default fleets: work (all three harnesses, with fleet args)
// and home (claude and codex), both searching w.work, plus the fleets the
// refusal scenarios need.
func (w *world) config() string {
	fleets := map[string]any{
		"work": map[string]any{"name": "Work", "color": "#336699", "search_roots": []string{w.work},
			"claude": w.dock("claude", "--fa-claude"), "codex": w.dock("codex", "--fa-codex"),
			"copilot": w.dock("copilot", "--fa-copilot")},
		"home": map[string]any{"name": "Home", "color": "#cc5533", "search_roots": []string{w.work},
			"claude": w.dock("claude"), "codex": w.dock("codex")},
		"claudeonly": map[string]any{"name": "Claude Only", "search_roots": []string{w.work},
			"claude": w.dock("claude")},
		"nosearch": map[string]any{"name": "No Search", "search_roots": []string{},
			"claude": w.dock("claude")},
		"broken": map[string]any{"name": "Broken", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": "/nonexistent/dir/claude"}},
		"vanish": map[string]any{"name": "Vanish", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": filepath.Join(w.root, "vanish-claude"),
				"env": w.harnessEnv()}},
	}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": fleets})
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

// start writes the default config (when there is none) and starts Admiral.
func (w *world) start() *fleetInst {
	w.t.Helper()
	if _, err := os.Stat(configPath(w.home)); err != nil {
		writeConfig(w.t, w.home, w.config())
	}
	w.in = launchAdmiral(w.t, w.home, "127.0.0.1:"+freePort(w.t), w.baseEnv())
	return w.in
}

func (w *world) restart() *fleetInst {
	w.t.Helper()
	w.in.Stop()
	w.in = launchAdmiral(w.t, w.home, "127.0.0.1:"+w.in.Port, w.baseEnv())
	return w.in
}

// launchAdmiral is startAdmiral with an explicit environment.
func launchAdmiral(t *testing.T, home, listen string, env []string) *fleetInst {
	t.Helper()
	cmd := exec.Command(binaryPath(t), "--listen", listen)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start admiral: %v", err)
	}
	in := &instance{Home: home, listen: listen, cmd: cmd, done: make(chan struct{}), stderrBuf: stderr}
	go func() { _ = cmd.Wait(); close(in.done) }()
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
			t.Fatalf("admiral printed %d of 2 URL lines within 10s; stderr: %s", len(got), stderr.String())
		}
	}
	go func() {
		for range lines {
		}
	}()
	m := localLine.FindStringSubmatch(got[0])
	if m == nil {
		t.Fatalf("first line must be `Admiral is running at http://127.0.0.1:<port>/`, got %q", got[0])
	}
	in.Port = m[1]
	in.LocalURL = "http://127.0.0.1:" + in.Port
	in.LANLine = got[1]
	return &fleetInst{instance: in, env: env}
}

// checkout makes a git checkout in the search root, since a launch is refused
// for any other directory. The remotes a test needs are added with git.
func (w *world) checkout(name string) string {
	w.t.Helper()
	d := filepath.Join(w.work, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(d, ".git")); err != nil {
		w.git(d, "init", "-q")
	}
	return d
}

// installCommodore puts a fake commodore skill where each harness looks
// without any identity variable: under the HOME of Admiral and its ships.
func (w *world) installCommodore() {
	w.t.Helper()
	for _, h := range []string{".claude", ".codex", ".copilot"} {
		d := filepath.Join(w.userHom, h, "skills", "commodore")
		if err := os.MkdirAll(d, 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: commodore\ndescription: a fake skill\n---\n"), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
}

func (w *world) writeCue(lines ...string) {
	w.t.Helper()
	if err := os.WriteFile(w.cue, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// ---- the ships API ----

type shipPos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type shipJSON struct {
	Session    string   `json:"session"`
	Name       string   `json:"name"`
	Fleet      string   `json:"fleet"`
	FleetName  string   `json:"fleet_name"`
	FleetColor string   `json:"fleet_color"`
	Harness    string   `json:"harness"`
	Dir        string   `json:"dir"`
	Repo       string   `json:"repo"`
	Skill      *string  `json:"skill"`
	Source     string   `json:"source"`
	OrderID    string   `json:"order_id"`
	Launched   string   `json:"launched"`
	Status     string   `json:"status"`
	Position   *shipPos `json:"position"`
}

func send(t *testing.T, method, url string, body any) (int, string) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// order builds a launch request for a checkout under w.work. It names no
// source, so the server uses the text as sent; a launch that names a source
// must name one that exists (see launchFrom).
func (w *world) order(fleet, harness, repo string, skill map[string]any, text string) map[string]any {
	return map[string]any{"fleet": fleet, "harness": harness, "dir": w.checkout(repo), "skill": skill,
		"order_id": "o-" + repo, "text": text}
}

var commodore = map[string]any{"kind": "commodore"}
var noSkill = map[string]any{"kind": "none"}

func customSkill(name string) map[string]any { return map[string]any{"kind": "skill", "name": name} }

func (w *world) post(req map[string]any) (int, string) {
	w.t.Helper()
	return send(w.t, http.MethodPost, w.in.LocalURL+"/api/ships", req)
}

// launch posts a launch request and requires 201 and a ship in the body.
func (w *world) launch(req map[string]any) shipJSON {
	w.t.Helper()
	code, body := w.post(req)
	if code != http.StatusCreated {
		w.t.Fatalf("POST /api/ships = %d, want 201: %s", code, body)
	}
	return decodeShip(w.t, body)
}

func decodeShip(t *testing.T, raw string) shipJSON {
	t.Helper()
	var s shipJSON
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("launch response is not JSON: %v\n%s", err, raw)
	}
	if s.Session == "" { // tolerate {"ship": {...}}
		var wrap struct {
			Ship shipJSON `json:"ship"`
		}
		_ = json.Unmarshal([]byte(raw), &wrap)
		s = wrap.Ship
	}
	if s.Session == "" {
		t.Fatalf("launch response has no ship: %s", raw)
	}
	return s
}

func decodeShips(t *testing.T, raw string) []shipJSON {
	t.Helper()
	var list []shipJSON
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return list
	}
	var wrap struct {
		Ships []shipJSON `json:"ships"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		t.Fatalf("ship list is not JSON: %v\n%s", err, raw)
	}
	return wrap.Ships
}

func (in *fleetInst) ships(t *testing.T) []shipJSON {
	t.Helper()
	code, body := send(t, http.MethodGet, in.LocalURL+"/api/ships", nil)
	if code != 200 {
		t.Fatalf("GET /api/ships = %d, want 200: %s", code, body)
	}
	return decodeShips(t, body)
}

func byName(ships []shipJSON) map[string]shipJSON {
	m := map[string]shipJSON{}
	for _, s := range ships {
		m[s.Name] = s
	}
	return m
}

// waitShipsList polls GET /api/ships until pred holds.
func (in *fleetInst) waitShipsList(t *testing.T, d time.Duration, what string, pred func([]shipJSON) bool) []shipJSON {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		list := in.ships(t)
		if pred(list) {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("within %s GET /api/ships never showed %s; last list: %+v", d, what, list)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitShipsMsg reads WebSocket messages until a `ships` message satisfies pred.
func waitShipsMsg(t *testing.T, c *websocket.Conn, d time.Duration, what string, pred func([]shipJSON) bool) []shipJSON {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var last string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within %s the websocket never delivered a `ships` message with %s (last ships message: %s): %v", d, what, last, err)
		}
		var m struct {
			Type  string     `json:"type"`
			Ships []shipJSON `json:"ships"`
		}
		if json.Unmarshal(data, &m) != nil || m.Type != "ships" {
			continue
		}
		last = string(data)
		if pred(m.Ships) {
			return m.Ships
		}
	}
}

// ---- what the fake harness recorded ----

type record struct {
	harness string
	argv    []string
	env     map[string]string
	cwd     string
	name    string
	options string
	dir     string
}

func (w *world) waitFile(session, file string) string {
	w.t.Helper()
	p := filepath.Join(w.rec, session, file)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(p); err == nil {
			return string(data)
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("the fake harness of %s never wrote %s within 10s", session, file)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) record(session string) record {
	w.t.Helper()
	w.waitFile(session, "recorded")
	rd := func(f string) string { return w.waitFile(session, f) }
	r := record{harness: strings.TrimSpace(rd("harness")), cwd: strings.TrimSpace(rd("cwd")),
		name: strings.TrimSpace(rd("name")), options: rd("options"), env: map[string]string{},
		dir: filepath.Join(w.rec, session)}
	if raw := rd("argv"); raw != "" {
		r.argv = strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	}
	for _, l := range strings.Split(rd("env"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			r.env[k] = v
		}
	}
	return r
}
