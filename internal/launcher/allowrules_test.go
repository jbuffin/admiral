package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/config"
)

// claudeArgv waits for the Claude recorder and returns the argv it ran with.
func (r *crewDirRig) claudeArgv(t *testing.T) []string {
	t.Helper()
	r.recorded(t, "claude")
	b, err := os.ReadFile(filepath.Join(r.rec, "claude.argv"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
}

func settingsPath(argv []string) string {
	for i, a := range argv {
		if a == "--settings" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func allowOf(t *testing.T, argv []string) []string {
	t.Helper()
	_, settings := settingsOf(t, argv)
	if settings["hooks"] == nil || settings["statusLine"] == nil {
		t.Errorf("the --settings file has lost Admiral's hooks or status line: %v", settings)
	}
	perms, _ := settings["permissions"].(map[string]any)
	return strs(t, perms["allow"])
}

func TestAClaudeShipGetsItsReposAllowRulesAndLosesARemovedOne(t *testing.T) {
	r := newCrewDirRig(t, nil)
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:acme/widgets.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", r.work}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	store := r.l.AllowRules()
	mine := allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr view:*)"}
	for _, rule := range []allowrules.Rule{mine,
		{Harness: "claude", Repo: "acme/other", Rule: "Bash(make check)"},
		{Harness: "copilot", Repo: "acme/widgets", Rule: "shell(gh pr)"}} {
		if err := store.Add("work", rule); err != nil {
			t.Fatal(err)
		}
	}

	ship := r.launch(t)
	if got := allowOf(t, r.claudeArgv(t)); !reflect.DeepEqual(got, []string{"Bash(gh pr view:*)"}) {
		t.Errorf("a ship in acme/widgets is allowed %q, want only its repo's Claude rule", got)
	}
	if err := r.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: "boat", Harness: "claude", Dir: t.TempDir(), Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if got := allowOf(t, r.claudeArgv(t)); !reflect.DeepEqual(got, []string{"Bash(gh pr view:*)"}) {
		t.Errorf("its crew boat is allowed %q, want the ship's repo's rule", got)
	}

	if err := store.Remove("work", mine); err != nil {
		t.Fatal(err)
	}
	r.launch(t)
	if path := settingsPath(r.claudeArgv(t)); path != ClaudeSettingsPath(r.home) {
		t.Errorf("after the rule was removed a ship runs with %s, want the shared %s", path, ClaudeSettingsPath(r.home))
	}
}

func TestAClaudeShipInAnotherRepoGetsNoneOfTheRules(t *testing.T) {
	r := newCrewDirRig(t, nil)
	if err := r.l.AllowRules().Add("work", allowrules.Rule{Harness: "claude", Repo: filepath.Join(r.work, "elsewhere"), Rule: "Bash(make check)"}); err != nil {
		t.Fatal(err)
	}
	r.launch(t)
	if path := settingsPath(r.claudeArgv(t)); path != ClaudeSettingsPath(r.home) {
		t.Errorf("a ship in %s runs with %s, want the shared %s", r.work, path, ClaudeSettingsPath(r.home))
	}
}

func TestACodexShipsLaunchRestoresTheFleetsRulesFile(t *testing.T) {
	codexHome := t.TempDir()
	r := newCrewDirRig(t, map[string]string{"CODEX_HOME": codexHome})
	if err := r.l.AllowRules().Add("work", allowrules.Rule{Harness: "codex", Pattern: []string{"gh", "pr", "view"}}); err != nil {
		t.Fatal(err)
	}
	path := allowrules.CodexRulesPath(codexHome)
	if err := os.Remove(path); err != nil {
		t.Fatalf("adding a Codex rule wrote no %s: %v", path, err)
	}
	if _, err := r.l.Launch(Request{Fleet: "work", Harness: "codex", Dir: r.work, Skill: Skill{Kind: "none"}}); err != nil {
		t.Fatal(err)
	}
	r.recorded(t, "codex")
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), `prefix_rule(pattern=["gh", "pr", "view"], decision="allow")`) {
		t.Errorf("after a Codex launch %s reads %q (%v), want the fleet's rule", path, b, err)
	}
}

func TestTheStoreKeepsEachFleetsCodexRulesFileToItsRules(t *testing.T) {
	work, home, shared := t.TempDir(), t.TempDir(), t.TempDir()
	codex := func(dir string) []config.Harness {
		return []config.Harness{{Name: "codex", Env: map[string]string{"CODEX_HOME": dir}}}
	}
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Harnesses: codex(work)}, {Key: "home", Harnesses: codex(home)}, {Key: "side", Harnesses: codex(shared)}, {Key: "side2", Harnesses: codex(shared)}}}
	l := &Launcher{Home: t.TempDir(), Config: func() *config.Config { return cfg }}
	own := filepath.Join(work, "rules", "default.rules")
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("the engineer's"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := l.AllowRules()
	view := allowrules.Rule{Harness: "codex", Pattern: []string{"gh", "pr", "view"}}
	for _, add := range []struct {
		fleet string
		rule  allowrules.Rule
	}{{"work", view}, {"side", view}, {"side2", allowrules.Rule{Harness: "codex", Pattern: []string{"make", "check"}}}} {
		if err := store.Add(add.fleet, add.rule); err != nil {
			t.Fatal(err)
		}
	}
	read := func(dir string) string {
		b, _ := os.ReadFile(allowrules.CodexRulesPath(dir))
		return string(b)
	}
	if got := read(work); !strings.Contains(got, `prefix_rule(pattern=["gh", "pr", "view"], decision="allow")`) {
		t.Errorf("work's admiral.rules = %q, want its rule", got)
	}
	if _, err := os.Stat(allowrules.CodexRulesPath(home)); err == nil {
		t.Errorf("a fleet with no Codex rules got an admiral.rules")
	}
	if got := read(shared); !strings.Contains(got, `"gh", "pr", "view"`) || !strings.Contains(got, `"make", "check"`) {
		t.Errorf("two fleets sharing a CODEX_HOME have %q, want both fleets' rules", got)
	}

	if err := store.Remove("work", view); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(allowrules.CodexRulesPath(work)); err == nil {
		t.Errorf("removing work's last Codex rule left its admiral.rules: %q", read(work))
	}
	if b, err := os.ReadFile(own); err != nil || string(b) != "the engineer's" {
		t.Errorf("the engineer's own rules file reads %q (%v), want it untouched", b, err)
	}
}
