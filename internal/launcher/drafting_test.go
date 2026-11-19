package launcher

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func putFile(t *testing.T, path, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	writeFile(t, path, body)
}

type draftRig struct {
	cfg                      *config.Config
	home, identity           string
	checkout, plain, outside string
}

// newDraftRig is a fleet with two search roots: one holding a git checkout with the wayfinder skill, one a plain folder.
func newDraftRig(t *testing.T) *draftRig {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	r := &draftRig{home: filepath.Join(root, "home"), identity: filepath.Join(root, "id")}
	work, plain := filepath.Join(root, "work"), filepath.Join(root, "plain")
	r.checkout, r.plain = filepath.Join(work, "repo"), plain
	for _, d := range []string{r.home, r.identity, r.checkout, plain} {
		mustMkdir(t, d)
	}
	git(t, r.checkout, "init", "-q", "-b", "main")
	putFile(t, filepath.Join(r.checkout, ".claude", "skills", "wayfinder", "SKILL.md"), "---\nname: wayfinder\n---\n")
	r.cfg = &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{work, plain},
		Harnesses: []config.Harness{{Name: "claude", Executable: "/bin/sh", Found: true, Env: map[string]string{"CLAUDE_CONFIG_DIR": r.identity, "HOME": filepath.Join(root, "userhome")}}}}}}
	return r
}

func (r *draftRig) request() Request {
	return Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Skill: Skill{Kind: "skill", Name: "wayfinder"}, Text: "draft the campaign", Drafting: true}
}

func (r *draftRig) check(req Request) (Checked, *Error) { return CheckSpec(r.cfg, r.home, req) }

// installPlugin installs and enables a Claude plugin holding the skills named in the rig's identity.
func (r *draftRig) installPlugin(t *testing.T, plugin string, skills ...string) {
	t.Helper()
	path := filepath.Join(r.identity, "plugins", "cache", "mk", plugin)
	putFile(t, filepath.Join(path, ".claude-plugin", "plugin.json"), `{"name":"`+plugin+`"}`)
	for _, s := range skills {
		putFile(t, filepath.Join(path, "skills", s, "SKILL.md"), "---\nname: x\n---\n")
	}
	installed, _ := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{plugin + "@mk": []map[string]any{{"scope": "user", "installPath": path}}}})
	enabled, _ := json.Marshal(map[string]any{"enabledPlugins": map[string]bool{plugin + "@mk": true}})
	putFile(t, filepath.Join(r.identity, "plugins", "installed_plugins.json"), string(installed))
	putFile(t, filepath.Join(r.identity, "settings.json"), string(enabled))
}

func TestADraftingLaunchOfAnInstalledSkillInACheckoutIsAccepted(t *testing.T) {
	r := newDraftRig(t)
	if _, err := r.check(r.request()); err != nil {
		t.Fatalf("CheckSpec = %v, want the launch accepted", err)
	}
}

func TestADraftingLaunchSetsTheDraftingPaneOptionAndAPlainSkillLaunchDoesNot(t *testing.T) {
	r := newDraftRig(t)
	opt := func(req Request) string {
		chk, err := r.check(req)
		if err != nil {
			t.Fatalf("CheckSpec = %v", err)
		}
		for _, o := range chk.Opts {
			if o[0] == "drafting" {
				return o[1]
			}
		}
		return ""
	}
	if got := opt(r.request()); got != "1" {
		t.Errorf("drafting option of a drafting launch = %q, want 1", got)
	}
	plain := r.request()
	plain.Drafting = false
	if got := opt(plain); got != "" {
		t.Errorf("drafting option of a plain skill launch = %q, want empty", got)
	}
}

func TestADraftingLaunchIsRefusedWhenItCannotDraft(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(r *draftRig, req *Request)
		want   string // the reason names this, lowercase
	}{
		{"it has no skill", func(r *draftRig, req *Request) { req.Skill = Skill{Kind: "none"} }, "skill"},
		{"its skill name is empty", func(r *draftRig, req *Request) { req.Skill = Skill{Kind: "skill"} }, "skill"},
		{"the dir is not a git checkout", func(r *draftRig, req *Request) { req.Dir = r.plain }, "git"},
		{"no installed skill carries the name", func(r *draftRig, req *Request) { req.Skill.Name = "absent" }, "absent"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newDraftRig(t)
			putFile(t, filepath.Join(r.plain, ".claude", "skills", "wayfinder", "SKILL.md"), "---\nname: wayfinder\n---\n")
			req := r.request()
			c.mutate(r, &req)
			_, err := r.check(req)
			if err == nil || err.Status != http.StatusBadRequest || !strings.Contains(strings.ToLower(err.Msg), c.want) {
				t.Fatalf("CheckSpec = %v, want a 400 whose reason names %q", err, c.want)
			}
		})
	}
}

func TestADraftingLaunchWhoseNameTwoInstalledSkillsCarryIsRefusedListingBoth(t *testing.T) {
	r := newDraftRig(t)
	r.installPlugin(t, "a", "wayfinder")
	_, err := r.check(r.request())
	if err == nil || err.Status != http.StatusBadRequest || !strings.Contains(err.Msg, "wayfinder") || !strings.Contains(err.Msg, "a:wayfinder") {
		t.Fatalf("CheckSpec with plain wayfinder and a:wayfinder = %v, want a 400 listing both names", err)
	}
}

func TestADraftingLaunchNamingOneOfTwoAmbiguousSkillsIsAccepted(t *testing.T) {
	r := newDraftRig(t)
	r.installPlugin(t, "a", "wayfinder")
	req := r.request()
	req.Skill.Name = "a:wayfinder"
	if _, err := r.check(req); err != nil {
		t.Fatalf("CheckSpec naming a:wayfinder = %v, want it accepted", err)
	}
}

func TestADraftingLaunchFindsAPluginSkillByItsBareName(t *testing.T) {
	r := newDraftRig(t)
	if err := os.RemoveAll(filepath.Join(r.checkout, ".claude")); err != nil {
		t.Fatal(err)
	}
	r.installPlugin(t, "a", "wayfinder")
	chk, err := r.check(r.request())
	if err != nil {
		t.Fatalf("CheckSpec = %v, want wayfinder to resolve to the one plugin skill", err)
	}
	if chk.Skill != "a:wayfinder" {
		t.Errorf("resolved skill = %q, want a:wayfinder", chk.Skill)
	}
}

// launched runs a launch and answers where the harness ran, the text it was given and the pane option tmux holds for it.
func launchedDrafting(t *testing.T, p *pinRig, req Request) (out *Launched, cwd, text, option string) {
	t.Helper()
	out, err := p.l.Launch(req)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	env := p.recorded(t, "claude")
	b, rerr := os.ReadFile(filepath.Join(p.rec, "claude.argv"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	argv := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
	raw, _ := exec.Command(p.l.Tmux, "-S", p.l.Socket, "show-options", "-t", "="+out.Session+":", "-qv", sessionhost.Prefix+"drafting").CombinedOutput()
	return out, env["PWD"], argv[len(argv)-1], strings.TrimSpace(string(raw))
}

func draftingWorktreeRig(t *testing.T) *pinRig {
	t.Helper()
	p := githubRig(t, "https://github.com/acme/api.git")
	putFile(t, filepath.Join(p.work, "wip.txt"), "untracked\n")
	skill := filepath.Join(p.work, ".claude", "skills", "wayfinder")
	mustMkdir(t, skill)
	putFile(t, filepath.Join(skill, "SKILL.md"), "---\nname: wayfinder\n---\n")
	return p
}

func TestADraftingLaunchGetsItsOwnWorktreeAndNamesTheCampaignRefInItsLaunchText(t *testing.T) {
	p := draftingWorktreeRig(t)
	out, cwd, text, _ := launchedDrafting(t, p, Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "skill", Name: "wayfinder"}, Text: "draft the campaign", Drafting: true})
	if samePath(t, cwd, p.work) || !registered(t, p.work, cwd) {
		t.Errorf("a drafting ship ran in %s, want a worktree of the checkout %s", cwd, p.work)
	}
	home, _ := filepath.Abs(p.home)
	want := filepath.Join(home, "ships", out.Session, "campaign-ref")
	if !strings.HasPrefix(text, "/wayfinder draft the campaign") || !strings.Contains(text, want) {
		t.Errorf("launch text = %q, want it to start with the skill and text and name %s", text, want)
	}
}

func TestADraftingSkillLaunchIsTheOnlySkillLaunchWithAWorktreeAndACampaignRef(t *testing.T) {
	p := draftingWorktreeRig(t)
	_, cwd, text, _ := launchedDrafting(t, p, Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "skill", Name: "wayfinder"}, Text: "go"})
	if !samePath(t, cwd, p.work) {
		t.Errorf("a non-drafting skill ship ran in %s, want the checkout %s", cwd, p.work)
	}
	if strings.Contains(text, "campaign-ref") {
		t.Errorf("a non-drafting launch text names the campaign ref: %q", text)
	}
}
