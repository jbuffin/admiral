package launchorders_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commodore"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launchorders"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/state"
)

var identityVars = map[string]string{"claude": "CLAUDE_CONFIG_DIR", "codex": "CODEX_HOME", "copilot": "COPILOT_HOME"}

// cmdrig serves the launch orders routes over fleets whose docks run the fake harness.
type cmdrig struct {
	t      *testing.T
	home   string
	work   string
	root   string
	record string
	srv    *httptest.Server
	env    map[string]string
}

func fakeExe(t *testing.T, harness string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "e2e", "fakeharness", harness))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// newCmdRig has fleet "work" (claude, copilot and codex docks), fleet "nobin" (a claude dock with no executable)
// and fleet "twin" (a claude dock on the same identity as work's).
// Each dock's identity is <root>/id/<fleet>-<harness>.
func newCmdRig(t *testing.T, installer commodore.Installer, dockEnv map[string]string) *cmdrig {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	r := &cmdrig{t: t, root: root, home: filepath.Join(root, "admiral"), work: filepath.Join(root, "work"), record: filepath.Join(root, "record"), env: dockEnv}
	for _, d := range []string{r.home, r.work, r.record} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dock := func(fleet, harness, exe string) string {
		env := map[string]string{identityVars[harness]: r.identity(fleet, harness), "HOME": filepath.Join(root, "home"), "FAKE_RECORD_DIR": r.record}
		for k, v := range dockEnv {
			env[k] = v
		}
		b, _ := json.Marshal(map[string]any{"executable": exe, "env": env})
		return string(b)
	}
	cfg := fmt.Sprintf(`{"version":1,"fleets":{
	  "work":{"name":"Work","search_roots":[%s],"claude":%s,"copilot":%s,"codex":%s},
	  "nobin":{"name":"No binary","search_roots":[%s],"claude":%s},
	  "twin":{"name":"Twin","search_roots":[%s],"claude":%s}}}`,
		quote(r.work), dock("work", "claude", fakeExe(t, "claude")), dock("work", "copilot", fakeExe(t, "copilot")), dock("work", "codex", fakeExe(t, "codex")),
		quote(r.work), dock("nobin", "claude", filepath.Join(root, "no-such-dir", "claude")),
		quote(r.work), dock("work", "claude", fakeExe(t, "claude")))
	if err := os.WriteFile(filepath.Join(r.home, config.FileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cm, err := config.Open(r.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cm.Close)
	st, err := state.Open(r.home)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&launchorders.Service{Home: r.home, Config: cm, State: st, Ships: func() []ships.Ship { return nil }, Installer: installer}).Routes(mux)
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *cmdrig) identity(fleet, harness string) string {
	return filepath.Join(r.root, "id", fleet+"-"+harness)
}

// fake runs the fake harness's plugin command in a dock's identity, to set up what the request starts from.
func (r *cmdrig) fake(harness, version string, args ...string) {
	r.t.Helper()
	cmd := exec.Command(fakeExe(r.t, harness), args...)
	cmd.Env = append(os.Environ(), identityVars[harness]+"="+r.identity("work", harness), "HOME="+filepath.Join(r.root, "home"),
		"FAKE_RECORD_DIR="+filepath.Join(r.root, "setup-record"), "FAKE_PLUGIN_VERSION="+version)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("fake %v: %v\n%s", args, err, out)
	}
}

func (r *cmdrig) installPlugin(harness, version string) {
	r.t.Helper()
	verb := "install"
	if harness == "codex" {
		verb = "add"
	}
	r.fake(harness, version, "plugin", verb, "jasonbuffin@jbuffinskills")
}

func (r *cmdrig) post(body any) (int, map[string]any) {
	r.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(r.srv.URL+"/api/launch-orders/commodore", "application/json", strings.NewReader(string(b)))
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func (r *cmdrig) commodoreField(fleet, harness string) map[string]any {
	r.t.Helper()
	q := url.Values{"fleet": {fleet}, "harness": {harness}, "dir": {r.work}}
	resp, err := http.Get(r.srv.URL + "/api/launch-orders/skills?" + q.Encode())
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Commodore map[string]any `json:"commodore"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != http.StatusOK {
		r.t.Fatalf("skills = %d, %v", resp.StatusCode, err)
	}
	return body.Commodore
}

func (r *cmdrig) writeSettings(harness string, m map[string]any) {
	r.t.Helper()
	b, _ := json.Marshal(m)
	id := r.identity("work", harness)
	if err := os.MkdirAll(id, 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id, "settings.json"), b, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *cmdrig) settings(harness string) map[string]any {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.identity("work", harness), "settings.json"))
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		r.t.Fatal(err)
	}
	return m
}

func (r *cmdrig) autoUpdate(harness string) any {
	m, _ := r.settings(harness)["extraKnownMarketplaces"].(map[string]any)
	mk, _ := m["jbuffinskills"].(map[string]any)
	return mk["autoUpdate"]
}

// fakeInstaller records its calls and answers from its fields; install and update run the fake harness's own
// plugin command in the dock's identity, as a real install would leave it.
type fakeInstaller struct {
	mu      sync.Mutex
	calls   []string
	output  string
	err     error
	version string
	writes  func(dock config.Harness, a commodore.Action)
	entered chan string // when set, a claude call announces itself here and waits for release
	release chan struct{}
}

func (f *fakeInstaller) Run(ctx context.Context, dock config.Harness, a commodore.Action) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, dock.Name+" "+string(a))
	f.mu.Unlock()
	if f.entered != nil && dock.Name == "claude" {
		f.entered <- dock.Name
		<-f.release
	}
	if f.err == nil && f.writes != nil {
		f.writes(dock, a)
	}
	return f.output, f.err
}

func (f *fakeInstaller) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (r *cmdrig) installerWriting(version string) *fakeInstaller {
	return &fakeInstaller{writes: func(dock config.Harness, a commodore.Action) {
		r.installPlugin(dock.Name, version)
	}}
}

func TestSkillsReportsCommodoreMissingWhenNothingIsDetected(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	c := r.commodoreField("work", "claude")
	if c["found"] != false || c["name"] != "" || c["state"] != "missing" {
		t.Errorf("commodore = %v, want found false, name \"\", state missing", c)
	}
}

func TestSkillsReportsCommodoreOkAtOrAboveTheMinimum(t *testing.T) {
	for _, version := range []string{"3.5.0", "3.10.0", "4.2.0"} {
		r := newCmdRig(t, nil, nil)
		r.installPlugin("claude", version)
		c := r.commodoreField("work", "claude")
		if c["found"] != true || c["name"] != "jasonbuffin:commodore" || c["state"] != "ok" {
			t.Errorf("%s: commodore = %v, want found, jasonbuffin:commodore, ok", version, c)
		}
	}
}

func TestSkillsReportsCommodoreOutdatedWithBothVersions(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	r.installPlugin("claude", "3.1.0")
	c := r.commodoreField("work", "claude")
	want := map[string]any{"found": true, "name": "jasonbuffin:commodore", "state": "outdated", "installed": "3.1.0", "minimum": "3.5.0"}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("commodore = %v, want %v", c, want)
	}
}

func TestSkillsReportsALooseCommodoreAsUnknownVersion(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	writeFile(t, filepath.Join(r.identity("work", "claude"), "skills", "commodore", "SKILL.md"), "---\nname: commodore\ndescription: x\n---\n")
	c := r.commodoreField("work", "claude")
	if c["found"] != true || c["name"] != "commodore" || c["state"] != "unknown-version" {
		t.Errorf("commodore = %v, want found, commodore, unknown-version", c)
	}
	if _, has := c["installed"]; has {
		t.Errorf("commodore = %v, want no installed version for a loose skill", c)
	}
}

func TestSkillsReportsAPluginWithNoManifestVersionAsUnknownVersion(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	r.installPlugin("claude", "3.5.0")
	manifest := filepath.Join(r.identity("work", "claude"), "plugins", "cache", "jbuffinskills", "jasonbuffin", "3.5.0", ".claude-plugin", "plugin.json")
	writeFile(t, manifest, `{"name":"jasonbuffin","skills":["./skills/engineering/"]}`)
	if c := r.commodoreField("work", "claude"); c["state"] != "unknown-version" {
		t.Errorf("commodore = %v, want unknown-version", c)
	}
}

func TestAProjectCopyLaunchesButTheStateStaysMissing(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	writeFile(t, filepath.Join(r.work, ".claude", "skills", "commodore", "SKILL.md"), "---\nname: commodore\ndescription: x\n---\n")
	c := r.commodoreField("work", "claude")
	if c["found"] != true || c["name"] != "commodore" {
		t.Errorf("commodore = %v, want the launch lookup to find the project copy", c)
	}
	if c["state"] != "missing" {
		t.Errorf("state = %v, want missing: a project copy does not stop the install offer", c["state"])
	}
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallEndpointAnswersTheDocksNewState(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	inst := r.installerWriting("3.5.0")
	r = newCmdRigWith(t, r, inst)
	code, body := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "install"})
	if code != http.StatusOK || body["state"] != "ok" {
		t.Fatalf("install = %d %v, want 200 and state ok", code, body)
	}
	if _, has := body["found"]; has {
		t.Errorf("body = %v, want only the state fields: the POST has no checkout", body)
	}
	if got := inst.called(); !reflect.DeepEqual(got, []string{"claude install"}) {
		t.Errorf("installer calls = %q, want the claude dock's install", got)
	}
	if c := r.commodoreField("work", "claude"); c["state"] != "ok" {
		t.Errorf("skills after install: %v, want ok", c)
	}
}

// newCmdRigWith serves the same temp tree again with an installer that needs the rig's helpers.
func newCmdRigWith(t *testing.T, r *cmdrig, inst commodore.Installer) *cmdrig {
	t.Helper()
	r.srv.Close()
	cm, err := config.Open(r.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cm.Close)
	st, err := state.Open(r.home)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&launchorders.Service{Home: r.home, Config: cm, State: st, Ships: func() []ships.Ship { return nil }, Installer: inst}).Routes(mux)
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func TestUpdateEndpointFromOutdatedAnswersOk(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	r.installPlugin("claude", "3.1.0")
	inst := r.installerWriting("3.5.0")
	r = newCmdRigWith(t, r, inst)
	code, body := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "update"})
	if code != http.StatusOK || body["state"] != "ok" {
		t.Fatalf("update = %d %v, want 200 and state ok", code, body)
	}
	if got := inst.called(); !reflect.DeepEqual(got, []string{"claude update"}) {
		t.Errorf("installer calls = %q", got)
	}
}

func TestUpdateIsRefusedUnlessTheStateIsOutdated(t *testing.T) {
	for _, c := range []struct{ name, version string }{{"missing", ""}, {"ok", "3.5.0"}, {"unknown-version", "loose"}} {
		r := newCmdRig(t, nil, nil)
		switch c.version {
		case "":
		case "loose":
			writeFile(t, filepath.Join(r.identity("work", "claude"), "skills", "commodore", "SKILL.md"), "---\nname: commodore\ndescription: x\n---\n")
		default:
			r.installPlugin("claude", c.version)
		}
		inst := &fakeInstaller{}
		r = newCmdRigWith(t, r, inst)
		if code, body := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "update"}); code != http.StatusConflict {
			t.Errorf("update from %s = %d %v, want 409", c.name, code, body)
		}
		if got := inst.called(); len(got) != 0 {
			t.Errorf("update from %s ran the installer: %q", c.name, got)
		}
	}
}

func TestCommodoreRouteRefusesABadRequest(t *testing.T) {
	r := newCmdRig(t, &fakeInstaller{}, nil)
	for _, c := range []struct {
		name string
		body any
		want int
	}{
		{"unknown fleet", map[string]string{"fleet": "nope", "harness": "claude", "action": "install"}, http.StatusNotFound},
		{"a fleet with no such dock", map[string]string{"fleet": "nobin", "harness": "codex", "action": "install"}, http.StatusBadRequest},
		{"an unknown action", map[string]string{"fleet": "work", "harness": "claude", "action": "remove"}, http.StatusBadRequest},
		{"no action", map[string]string{"fleet": "work", "harness": "claude"}, http.StatusBadRequest},
	} {
		if code, body := r.post(c.body); code != c.want || body["error"] == nil {
			t.Errorf("%s: %d %v, want %d with an error", c.name, code, body, c.want)
		}
	}
	resp, err := http.Post(r.srv.URL+"/api/launch-orders/commodore", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a body that is not JSON = %d, want 400", resp.StatusCode)
	}
}

func numbered(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestAFailedInstallAnswers502WithOutputCommandsAndTheUnchangedState(t *testing.T) {
	inst := &fakeInstaller{output: numbered(1, 30), err: errors.New("exit status 1")}
	r := newCmdRig(t, inst, nil)
	code, body := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "install"})
	if code != http.StatusBadGateway {
		t.Fatalf("failed install = %d %v, want 502", code, body)
	}
	if e, _ := body["error"].(string); e == "" {
		t.Errorf("error = %v, want a message", body["error"])
	}
	if out, _ := body["output"].(string); strings.TrimRight(out, "\n") != strings.TrimRight(numbered(11, 30), "\n") {
		t.Errorf("output = %q, want the last 20 of 30 lines", body["output"])
	}
	wantCmds := []any{"claude plugin marketplace add jbuffin/skills", "claude plugin install jasonbuffin@jbuffinskills"}
	if !reflect.DeepEqual(body["commands"], wantCmds) {
		t.Errorf("commands = %v, want %v", body["commands"], wantCmds)
	}
	if !reflect.DeepEqual(body["state"], map[string]any{"state": "missing"}) {
		t.Errorf("state = %v, want {state: missing}", body["state"])
	}
}

func TestAShortFailureOutputComesBackWhole(t *testing.T) {
	r := newCmdRig(t, &fakeInstaller{output: "one\ntwo\n", err: errors.New("exit status 1")}, nil)
	_, body := r.post(map[string]string{"fleet": "work", "harness": "codex", "action": "install"})
	if out, _ := body["output"].(string); strings.TrimRight(out, "\n") != "one\ntwo" {
		t.Errorf("output = %q, want the two lines", body["output"])
	}
	wantCmds := []any{"codex plugin marketplace add jbuffin/skills", "codex plugin add jasonbuffin@jbuffinskills"}
	if !reflect.DeepEqual(body["commands"], wantCmds) {
		t.Errorf("commands = %v, want %v", body["commands"], wantCmds)
	}
}

func TestAFailedUpdateKeepsTheOutdatedState(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	r.installPlugin("claude", "3.1.0")
	r = newCmdRigWith(t, r, &fakeInstaller{output: "nope\n", err: errors.New("exit status 1")})
	code, body := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "update"})
	if code != http.StatusBadGateway {
		t.Fatalf("failed update = %d %v, want 502", code, body)
	}
	want := map[string]any{"state": "outdated", "installed": "3.1.0", "minimum": "3.5.0"}
	if !reflect.DeepEqual(body["state"], want) {
		t.Errorf("state = %v, want %v", body["state"], want)
	}
	if !reflect.DeepEqual(body["commands"], []any{"claude plugin update jasonbuffin@jbuffinskills"}) {
		t.Errorf("commands = %v, want the update command", body["commands"])
	}
}

func TestADockCLIThatFailsOrIsNotLoggedInAnswers502WithItsOutput(t *testing.T) {
	for _, c := range []struct{ cue, want string }{{"fail", "boom"}, {"not-logged-in", "not logged in"}} {
		r := newCmdRig(t, nil, map[string]string{"FAKE_PLUGIN": c.cue})
		code, body := r.post(map[string]string{"fleet": "work", "harness": "copilot", "action": "install"})
		if code != http.StatusBadGateway {
			t.Errorf("%s: %d %v, want 502", c.cue, code, body)
			continue
		}
		if out, _ := body["output"].(string); !strings.Contains(out, c.want) {
			t.Errorf("%s: output %q lacks %q", c.cue, out, c.want)
		}
		if !reflect.DeepEqual(body["commands"], []any{"copilot plugin marketplace add jbuffin/skills", "copilot plugin install jasonbuffin@jbuffinskills"}) {
			t.Errorf("%s: commands = %v", c.cue, body["commands"])
		}
		if !reflect.DeepEqual(body["state"], map[string]any{"state": "missing"}) {
			t.Errorf("%s: state = %v, want missing", c.cue, body["state"])
		}
	}
}

func TestAMissingBinaryAnswers502WithTheCommandsToRunByHand(t *testing.T) {
	r := newCmdRig(t, nil, nil)
	code, body := r.post(map[string]string{"fleet": "nobin", "harness": "claude", "action": "install"})
	if code != http.StatusBadGateway {
		t.Fatalf("install with no binary = %d %v, want 502", code, body)
	}
	if e, _ := body["error"].(string); e == "" {
		t.Errorf("error = %v, want a message", body["error"])
	}
	if !reflect.DeepEqual(body["commands"], []any{"claude plugin marketplace add jbuffin/skills", "claude plugin install jasonbuffin@jbuffinskills"}) {
		t.Errorf("commands = %v", body["commands"])
	}
	if !reflect.DeepEqual(body["state"], map[string]any{"state": "missing"}) {
		t.Errorf("state = %v, want missing", body["state"])
	}
}

func TestASecondRequestForTheSameDockWhileOneRunsIs409(t *testing.T) {
	inst := &fakeInstaller{entered: make(chan string), release: make(chan struct{}), err: errors.New("exit status 1")}
	r := newCmdRig(t, inst, nil)
	first := make(chan int, 1)
	go func() {
		code, _ := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "install"})
		first <- code
	}()
	select {
	case <-inst.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first request never reached the installer")
	}
	if code, body := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "install"}); code != http.StatusConflict {
		t.Errorf("same fleet and harness while one runs = %d %v, want 409", code, body)
	}
	if code, body := r.post(map[string]string{"fleet": "work", "harness": "copilot", "action": "install"}); code == http.StatusConflict {
		t.Errorf("another harness of the fleet = %d %v, want it to run", code, body)
	}
	close(inst.release)
	if code := <-first; code != http.StatusBadGateway {
		t.Errorf("the first request = %d, want its own 502", code)
	}
	go func() { <-inst.entered }() // the fake announces every claude call and nobody else reads it
	if code, body := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "install"}); code == http.StatusConflict {
		t.Errorf("after the first finished = %d %v, want the dock free again", code, body)
	}
}

func TestInstallThroughTheRealInstallerTurnsOnAutoUpdate(t *testing.T) {
	for _, harness := range []string{"copilot", "claude"} {
		r := newCmdRig(t, nil, nil)
		r.writeSettings(harness, map[string]any{"theme": "dark", "env": map[string]any{"A": "1"}})
		code, body := r.post(map[string]string{"fleet": "work", "harness": harness, "action": "install"})
		if code != http.StatusOK || body["state"] != "ok" {
			t.Fatalf("%s install = %d %v, want 200 ok", harness, code, body)
		}
		if got := r.autoUpdate(harness); got != true {
			t.Errorf("%s: autoUpdate = %v, want true", harness, got)
		}
		if s := r.settings(harness); s["theme"] != "dark" || !reflect.DeepEqual(s["env"], map[string]any{"A": "1"}) {
			t.Errorf("%s: other settings changed: %v", harness, s)
		}
		if c := r.commodoreField("work", harness); c["state"] != "ok" || c["found"] != true {
			t.Errorf("%s: skills after install = %v, want found and ok", harness, c)
		}
	}
}

func TestInstallThroughTheRealInstallerLeavesAutoUpdateAloneForCommodoreAlreadyThere(t *testing.T) {
	for _, harness := range []string{"copilot", "claude"} {
		// marketplace already registered
		r := newCmdRig(t, nil, nil)
		r.fake(harness, "3.5.0", "plugin", "marketplace", "add", "jbuffin/skills")
		if code, body := r.post(map[string]string{"fleet": "work", "harness": harness, "action": "install"}); code != http.StatusOK {
			t.Fatalf("%s install = %d %v", harness, code, body)
		}
		if got := r.autoUpdate(harness); got != nil {
			t.Errorf("%s: marketplace already registered, autoUpdate = %v, want no key", harness, got)
		}
		if c := r.commodoreField("work", harness); c["state"] != "ok" {
			t.Errorf("%s: skills after install = %v, want ok", harness, c)
		}
		// a plugin already detected
		r = newCmdRig(t, nil, nil)
		r.installPlugin(harness, "3.5.0")
		if code, body := r.post(map[string]string{"fleet": "work", "harness": harness, "action": "install"}); code != http.StatusOK {
			t.Fatalf("%s install over a plugin = %d %v", harness, code, body)
		}
		if got := r.autoUpdate(harness); got != nil {
			t.Errorf("%s: plugin already detected, autoUpdate = %v, want no key", harness, got)
		}
	}
}

func TestUpdateThroughTheRealInstallerNeverTurnsOnAutoUpdate(t *testing.T) {
	for _, harness := range []string{"copilot", "claude"} {
		r := newCmdRig(t, nil, map[string]string{"FAKE_PLUGIN_VERSION": "3.6.0"})
		r.installPlugin(harness, "3.1.0")
		code, body := r.post(map[string]string{"fleet": "work", "harness": harness, "action": "update"})
		if code != http.StatusOK || body["state"] != "ok" {
			t.Fatalf("%s update = %d %v, want 200 ok", harness, code, body)
		}
		if got := r.autoUpdate(harness); got != nil {
			t.Errorf("%s: update set autoUpdate = %v", harness, got)
		}
	}
}

func TestTwoFleetsOnOneIdentityShareTheLock(t *testing.T) {
	inst := &fakeInstaller{entered: make(chan string), release: make(chan struct{}), err: errors.New("exit status 1")}
	r := newCmdRig(t, inst, nil)
	first := make(chan int, 1)
	go func() {
		code, _ := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "install"})
		first <- code
	}()
	select {
	case <-inst.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first request never reached the installer")
	}
	if code, body := r.post(map[string]string{"fleet": "twin", "harness": "claude", "action": "install"}); code != http.StatusConflict {
		t.Errorf("another fleet on the same identity = %d %v, want 409", code, body)
	}
	close(inst.release)
	<-first
}

func TestAnAutoUpdateFailureAfterAGoodInstallIs200WithAWarning(t *testing.T) {
	inst := &fakeInstaller{err: fmt.Errorf("%w: settings.json is not JSON", commodore.ErrAutoUpdate)}
	r := newCmdRig(t, inst, nil)
	code, body := r.post(map[string]string{"fleet": "work", "harness": "claude", "action": "install"})
	if code != http.StatusOK {
		t.Fatalf("install = %d %v, want 200", code, body)
	}
	if w, _ := body["warning"].(string); !strings.Contains(w, "auto-update") {
		t.Errorf("warning = %v, want a message about auto-update", body["warning"])
	}
	if body["state"] == nil {
		t.Errorf("body = %v, want the state too", body)
	}
}
