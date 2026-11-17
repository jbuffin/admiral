package commodore_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commodore"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/skillindex"
)

var identityVar = map[string]string{"claude": "CLAUDE_CONFIG_DIR", "codex": "CODEX_HOME", "copilot": "COPILOT_HOME"}

// rig is one dock whose CLI is the fake harness, with its identity and recordings in temp dirs.
type rig struct {
	t        *testing.T
	harness  string
	admiral  string // $ADMIRAL_HOME
	home     string // the dock's HOME, so a default identity directory stays in the test
	identity string
	record   string
	env      map[string]string
}

func newRig(t *testing.T, harness string, extraEnv ...string) *rig {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	r := &rig{t: t, harness: harness, admiral: filepath.Join(root, "admiral"), home: filepath.Join(root, "home"),
		identity: filepath.Join(root, "identity"), record: filepath.Join(root, "record")}
	for _, d := range []string{r.admiral, r.home, r.record} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.env = map[string]string{identityVar[harness]: r.identity, "HOME": r.home, "FAKE_RECORD_DIR": r.record,
		"PATH": "/dock-marker/bin" + string(os.PathListSeparator) + os.Getenv("PATH")}
	for i := 0; i+1 < len(extraEnv); i += 2 {
		r.env[extraEnv[i]] = extraEnv[i+1]
	}
	return r
}

func (r *rig) dock() config.Harness {
	exe, err := filepath.Abs(filepath.Join("..", "..", "e2e", "fakeharness", r.harness))
	if err != nil {
		r.t.Fatal(err)
	}
	return config.Harness{Name: r.harness, Executable: exe, Found: true, Env: r.env}
}

func (r *rig) runner() *commodore.Runner {
	return &commodore.Runner{Home: r.admiral, Timeout: 20 * time.Second}
}

func (r *rig) run(a commodore.Action) (string, error) {
	r.t.Helper()
	return r.runner().Run(context.Background(), r.dock(), a)
}

// fake runs the fake harness's own plugin command directly, to set up what a run starts from.
func (r *rig) fake(version string, args ...string) {
	r.t.Helper()
	d := r.dock()
	cmd := exec.Command(d.Executable, args...)
	cmd.Env = append(d.Environ(), "FAKE_PLUGIN_VERSION="+version)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("fake %v: %v\n%s", args, err, out)
	}
	_ = os.Remove(filepath.Join(r.record, "plugin-runs"))
}

func (r *rig) installPluginOnly(version string) {
	verb := "install"
	if r.harness == "codex" {
		verb = "add"
	}
	r.fake(version, "plugin", verb, "jasonbuffin@jbuffinskills")
}

func (r *rig) registerMarketplace() {
	r.fake("3.5.0", "plugin", "marketplace", "add", "jbuffin/skills")
}

func (r *rig) runs() []string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.record, "plugin-runs"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func (r *rig) detected() (skillindex.Commodore, bool) {
	return skillindex.LookupCommodore(r.harness, r.dock().Environ(), "")
}

func (r *rig) settings() map[string]any {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.identity, "settings.json"))
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		r.t.Fatalf("settings.json: %v\n%s", err, b)
	}
	return m
}

func (r *rig) writeSettings(m map[string]any) {
	r.t.Helper()
	b, _ := json.Marshal(m)
	if err := os.MkdirAll(r.identity, 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.identity, "settings.json"), b, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// mustDetect fails unless the identity now holds a plugin Commodore at version.
func (r *rig) mustDetect(version string) {
	r.t.Helper()
	if c, ok := r.detected(); !ok || c.Version != version {
		r.t.Errorf("%s: detected %+v (%v), want the %s plugin", r.harness, c, ok, version)
	}
}

func (r *rig) autoUpdate() any {
	m, _ := r.settings()["extraKnownMarketplaces"].(map[string]any)
	mk, _ := m["jbuffinskills"].(map[string]any)
	return mk["autoUpdate"]
}

func TestStateOfAFoundCommodore(t *testing.T) {
	plugin := func(v string) skillindex.Commodore {
		return skillindex.Commodore{Name: "jasonbuffin:commodore", Plugin: "jasonbuffin@jbuffinskills", Version: v}
	}
	for _, c := range []struct {
		name      string
		found     skillindex.Commodore
		detected  bool
		want      string
		installed string
	}{
		{"nothing detected", skillindex.Commodore{}, false, "missing", ""},
		{"exactly the minimum", plugin("3.5.0"), true, "ok", ""},
		{"newer than Admiral knows", plugin("4.2.0"), true, "ok", ""},
		{"3.10.0 is above 3.5.0, compared by part", plugin("3.10.0"), true, "ok", ""},
		{"3.4.10 is below 3.5.0, compared by part", plugin("3.4.10"), true, "outdated", "3.4.10"},
		{"older", plugin("3.1.0"), true, "outdated", "3.1.0"},
		{"a loose skill", skillindex.Commodore{Name: "commodore", Dir: "/x/commodore"}, true, "unknown-version", ""},
		{"a plugin with no manifest version", plugin(""), true, "unknown-version", ""},
	} {
		got := commodore.StateOf(c.found, c.detected)
		if got.State != c.want || got.Installed != c.installed {
			t.Errorf("%s: state %q installed %q, want %q %q", c.name, got.State, got.Installed, c.want, c.installed)
		}
		if c.want == "outdated" && got.Minimum != "3.5.0" {
			t.Errorf("%s: minimum %q, want 3.5.0", c.name, got.Minimum)
		}
	}
}

func TestStateJSONNamesVersionsOnlyWhenOutdated(t *testing.T) {
	fields := func(s commodore.State) map[string]any {
		b, _ := json.Marshal(s)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	if got, want := fields(commodore.StateOf(skillindex.Commodore{Name: "j:commodore", Plugin: "j@m", Version: "3.1.0"}, true)),
		(map[string]any{"state": "outdated", "installed": "3.1.0", "minimum": "3.5.0"}); !reflect.DeepEqual(got, want) {
		t.Errorf("outdated = %v, want %v", got, want)
	}
	for name, s := range map[string]commodore.State{
		"missing":         commodore.StateOf(skillindex.Commodore{}, false),
		"unknown-version": commodore.StateOf(skillindex.Commodore{Name: "commodore"}, true),
	} {
		if got, want := fields(s), (map[string]any{"state": name}); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestDetectReadsTheDocksIdentityNotACheckout(t *testing.T) {
	r := newRig(t, "claude")
	if got := commodore.Detect(r.dock()); got.State != "missing" {
		t.Fatalf("empty identity: state %q, want missing", got.State)
	}
	r.registerMarketplace()
	r.installPluginOnly("3.1.0")
	if got := commodore.Detect(r.dock()); got.State != "outdated" || got.Installed != "3.1.0" || got.Minimum != "3.5.0" {
		t.Errorf("3.1.0 plugin: %+v, want outdated 3.1.0 / 3.5.0", got)
	}
}

func TestCommandsAreTheIssuesTable(t *testing.T) {
	for _, c := range []struct {
		harness string
		action  commodore.Action
		want    []string
	}{
		{"claude", commodore.Install, []string{"claude plugin marketplace add jbuffin/skills", "claude plugin install jasonbuffin@jbuffinskills"}},
		{"claude", commodore.Update, []string{"claude plugin update jasonbuffin@jbuffinskills"}},
		{"codex", commodore.Install, []string{"codex plugin marketplace add jbuffin/skills", "codex plugin add jasonbuffin@jbuffinskills"}},
		{"codex", commodore.Update, []string{"codex plugin marketplace upgrade jbuffinskills"}},
		{"copilot", commodore.Install, []string{"copilot plugin marketplace add jbuffin/skills", "copilot plugin install jasonbuffin@jbuffinskills"}},
		{"copilot", commodore.Update, []string{"copilot plugin update jasonbuffin@jbuffinskills"}},
	} {
		if got := commodore.Commands(c.harness, c.action); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s = %q, want %q", c.harness, c.action, got, c.want)
		}
	}
}

func TestInstallRunsTheHarnessesCommandsAndLandsInTheDocksIdentity(t *testing.T) {
	for harness, want := range map[string][]string{
		"claude":  {"plugin marketplace add jbuffin/skills", "plugin install jasonbuffin@jbuffinskills"},
		"codex":   {"plugin marketplace add jbuffin/skills", "plugin add jasonbuffin@jbuffinskills"},
		"copilot": {"plugin marketplace add jbuffin/skills", "plugin install jasonbuffin@jbuffinskills"},
	} {
		r := newRig(t, harness)
		if _, err := r.run(commodore.Install); err != nil {
			t.Errorf("%s install: %v", harness, err)
			continue
		}
		if got := r.runs(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s ran %q, want %q", harness, got, want)
		}
		if c, ok := r.detected(); !ok || c.Version != "3.5.0" {
			t.Errorf("%s: detected %+v (%v) in the dock's identity, want the 3.5.0 plugin", harness, c, ok)
		}
		if _, err := os.Stat(filepath.Join(r.home, "."+harness)); err == nil {
			t.Errorf("%s: wrote to the default identity directory instead of the dock's", harness)
		}
	}
}

func TestUpdateRunsOnlyTheUpdateCommand(t *testing.T) {
	for harness, want := range map[string][]string{
		"claude":  {"plugin update jasonbuffin@jbuffinskills"},
		"codex":   {"plugin marketplace upgrade jbuffinskills"},
		"copilot": {"plugin update jasonbuffin@jbuffinskills"},
	} {
		r := newRig(t, harness, "FAKE_PLUGIN_VERSION", "3.1.0")
		r.registerMarketplace()
		r.installPluginOnly("3.1.0")
		r.env["FAKE_PLUGIN_VERSION"] = "3.6.0"
		if _, err := r.run(commodore.Update); err != nil {
			t.Errorf("%s update: %v", harness, err)
			continue
		}
		if got := r.runs(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s ran %q, want %q", harness, got, want)
		}
		if c, _ := r.detected(); c.Version != "3.6.0" {
			t.Errorf("%s: version after update %q, want 3.6.0", harness, c.Version)
		}
	}
}

func TestInstallSkipsTheMarketplaceAddWhenTheIdentityHasIt(t *testing.T) {
	for harness, want := range map[string]string{"claude": "plugin install jasonbuffin@jbuffinskills", "codex": "plugin add jasonbuffin@jbuffinskills", "copilot": "plugin install jasonbuffin@jbuffinskills"} {
		r := newRig(t, harness)
		r.registerMarketplace()
		if _, err := r.run(commodore.Install); err != nil {
			t.Errorf("%s install: %v", harness, err)
			continue
		}
		if got := r.runs(); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%s ran %q, want only %q", harness, got, want)
		}
	}
}

func TestTheRunSeesTheShipsPathAndTheDocksOwn(t *testing.T) {
	r := newRig(t, "claude")
	if _, err := r.run(commodore.Install); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(r.record, "plugin-path"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSpace(string(b)), string(os.PathListSeparator))
	if len(parts) < 2 || parts[0] != filepath.Join(r.admiral, "bin") || parts[1] != "/dock-marker/bin" {
		t.Errorf("PATH starts %q, want $ADMIRAL_HOME/bin then the dock's own PATH", parts[:min(2, len(parts))])
	}
}

func TestAFailedRunReportsItsOutputAndStopsAtTheFirstFailingCommand(t *testing.T) {
	for _, c := range []struct{ cue, want string }{{"fail", "boom"}, {"not-logged-in", "not logged in"}} {
		r := newRig(t, "claude", "FAKE_PLUGIN", c.cue)
		out, err := r.run(commodore.Install)
		if err == nil {
			t.Errorf("%s: no error", c.cue)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: output %q lacks %q", c.cue, out, c.want)
		}
		if got := r.runs(); !reflect.DeepEqual(got, []string{"plugin marketplace add jbuffin/skills"}) {
			t.Errorf("%s: ran %q, want only the marketplace add", c.cue, got)
		}
		if _, ok := r.detected(); ok {
			t.Errorf("%s: a Commodore is detected after the failed run", c.cue)
		}
		if _, err := os.Stat(filepath.Join(r.identity, "settings.json")); err == nil {
			t.Errorf("%s: a failed install wrote settings.json", c.cue)
		}
	}
}

func TestAMissingBinaryIsAFailure(t *testing.T) {
	r := newRig(t, "claude")
	d := r.dock()
	d.Executable, d.Found = "claude", false
	if _, err := r.runner().Run(context.Background(), d, commodore.Install); err == nil {
		t.Error("a dock whose executable was not found installed without an error")
	}
	if got := r.runs(); got != nil {
		t.Errorf("ran %q with no binary", got)
	}
}

func TestARunThatHangsTimesOut(t *testing.T) {
	r := newRig(t, "claude")
	hang := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(hang, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := r.dock()
	d.Executable = hang
	start := time.Now()
	_, err := (&commodore.Runner{Home: r.admiral, Timeout: 300 * time.Millisecond}).Run(context.Background(), d, commodore.Install)
	if err == nil || time.Since(start) > 15*time.Second {
		t.Errorf("hanging CLI: err %v after %v, want a timeout failure", err, time.Since(start))
	}
}

func TestInstallTurnsOnAutoUpdateForTheMarketplaceItAdded(t *testing.T) {
	for _, harness := range []string{"claude", "copilot"} {
		r := newRig(t, harness)
		other := map[string]any{"source": map[string]any{"source": "github", "repo": "acme/other"}}
		r.writeSettings(map[string]any{"theme": "dark", "env": map[string]any{"A": "1"}, "extraKnownMarketplaces": map[string]any{"other": other}})
		if _, err := r.run(commodore.Install); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		if got := r.autoUpdate(); got != true {
			t.Errorf("%s: autoUpdate = %v, want true", harness, got)
		}
		s := r.settings()
		if s["theme"] != "dark" || !reflect.DeepEqual(s["env"], map[string]any{"A": "1"}) {
			t.Errorf("%s: other keys changed: %v", harness, s)
		}
		ms, _ := s["extraKnownMarketplaces"].(map[string]any)
		if !reflect.DeepEqual(ms["other"], other) {
			t.Errorf("%s: the other marketplace changed: %v", harness, ms["other"])
		}
		if mk, _ := ms["jbuffinskills"].(map[string]any); mk["source"] == nil {
			t.Errorf("%s: the marketplace lost its source: %v", harness, mk)
		}
		if ep, _ := s["enabledPlugins"].(map[string]any); ep["jasonbuffin@jbuffinskills"] != true {
			t.Errorf("%s: enabledPlugins = %v, want the plugin kept on", harness, ep)
		}
	}
}

func TestInstallIntoAnIdentityWithNoSettingsFileStillTurnsAutoUpdateOn(t *testing.T) {
	r := newRig(t, "copilot")
	if _, err := r.run(commodore.Install); err != nil {
		t.Fatal(err)
	}
	if got := r.autoUpdate(); got != true {
		t.Errorf("autoUpdate = %v, want true", got)
	}
}

func TestInstallLeavesAutoUpdateAloneForAMarketplaceThatWasAlreadyThere(t *testing.T) {
	for _, harness := range []string{"claude", "copilot"} {
		r := newRig(t, harness)
		r.registerMarketplace()
		if _, err := r.run(commodore.Install); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		r.mustDetect("3.5.0")
		if got := r.autoUpdate(); got != nil {
			t.Errorf("%s: autoUpdate = %v for a marketplace Admiral did not add, want no key", harness, got)
		}
	}
}

func TestInstallLeavesAutoUpdateAloneWhenACommodorePluginWasAlreadyDetected(t *testing.T) {
	for _, harness := range []string{"claude", "copilot"} {
		r := newRig(t, harness)
		r.installPluginOnly("3.5.0") // a plugin, and no marketplace registration
		if _, ok := r.detected(); !ok {
			t.Fatalf("%s: setup left no plugin", harness)
		}
		if _, err := r.run(commodore.Install); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		r.mustDetect("3.5.0")
		if got := r.autoUpdate(); got != nil {
			t.Errorf("%s: autoUpdate = %v for a Commodore Admiral did not install, want no key", harness, got)
		}
	}
}

func TestALooseSkillBeforehandDoesNotStopAutoUpdate(t *testing.T) {
	for _, harness := range []string{"claude", "copilot"} {
		r := newRig(t, harness)
		skill := filepath.Join(r.identity, "skills", "commodore")
		if err := os.MkdirAll(skill, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: commodore\ndescription: x\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := r.run(commodore.Install); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		if got := r.autoUpdate(); got != true {
			t.Errorf("%s: autoUpdate = %v, want true after installing over a loose skill", harness, got)
		}
	}
}

func TestUpdateNeverTurnsAutoUpdateOn(t *testing.T) {
	for _, harness := range []string{"claude", "copilot"} {
		r := newRig(t, harness)
		r.installPluginOnly("3.1.0")
		r.env["FAKE_PLUGIN_VERSION"] = "3.5.0"
		if _, err := r.run(commodore.Update); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		r.mustDetect("3.5.0")
		if got := r.autoUpdate(); got != nil {
			t.Errorf("%s: update set autoUpdate = %v", harness, got)
		}
	}
}

func TestCodexInstallWritesNoSettingsFile(t *testing.T) {
	r := newRig(t, "codex")
	if _, err := r.run(commodore.Install); err != nil {
		t.Fatal(err)
	}
	r.mustDetect("3.5.0")
	if _, err := os.Stat(filepath.Join(r.identity, "settings.json")); err == nil {
		t.Error("a Codex install wrote settings.json; Codex updates at startup and gets no auto-update key")
	}
}

func TestAutoUpdateWritesThroughASymlinkedSettingsFileAndKeepsItsText(t *testing.T) {
	r := newRig(t, "claude")
	target := filepath.Join(t.TempDir(), "dotfiles-settings.json")
	if err := os.WriteFile(target, []byte(`{"hooks":{"cmd":"a && b <c>"}}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.identity, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(r.identity, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(commodore.Install); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("settings.json is no longer a symlink: %v %v", st, err)
	}
	b, _ := os.ReadFile(target)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("target: %v\n%s", err, b)
	}
	if got := r.autoUpdate(); got != true {
		t.Errorf("the target's autoUpdate = %v, want true", got)
	}
	if !strings.Contains(string(b), "a && b <c>") {
		t.Errorf("a value with && and <> changed: %s", b)
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", st.Mode().Perm())
	}
}

func TestACopilotSettingsFileWithLeadingCommentLinesIsReadAndKeepsThem(t *testing.T) {
	r := newRig(t, "copilot")
	if err := os.MkdirAll(r.identity, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(r.identity, "settings.json")
	text := "// User settings.\n" + `{"extraKnownMarketplaces":{"jbuffinskills":{"source":{"source":"github","repo":"jbuffin/skills"}}}}` + "\n"
	if err := os.WriteFile(settings, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(commodore.Install); err != nil {
		t.Fatal(err)
	}
	if got := r.runs(); !reflect.DeepEqual(got, []string{"plugin install jasonbuffin@jbuffinskills"}) {
		t.Errorf("ran %q, want no marketplace add: the commented file registers it", got)
	}
	// And a commented file with no marketplace gets the key with its comment kept.
	r2 := newRig(t, "copilot")
	if err := os.MkdirAll(r2.identity, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r2.identity, "settings.json"), []byte("// User settings.\n{\"theme\":\"dark\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.run(commodore.Install); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(r2.identity, "settings.json"))
	if !strings.HasPrefix(string(b), "// User settings.\n") {
		t.Errorf("the comment line is gone: %s", b)
	}
}

func TestATimedOutRunKillsTheCLIsChildren(t *testing.T) {
	r := newRig(t, "claude", "FAKE_PLUGIN", "hang")
	runner := &commodore.Runner{Home: r.admiral, Timeout: 500 * time.Millisecond}
	if _, err := runner.Run(context.Background(), r.dock(), commodore.Install); err == nil {
		t.Fatal("a hanging CLI did not fail")
	}
	b, err := os.ReadFile(filepath.Join(r.record, "plugin-child"))
	if err != nil {
		t.Fatal(err)
	}
	pid := strings.TrimSpace(string(b))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("kill", "-0", pid).Run() != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("the CLI's child %s is still running after the timeout", pid)
}
