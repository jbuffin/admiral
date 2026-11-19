package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// isolateGit keeps the developer's own git config out of a test that depends on init.defaultBranch.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// localRig is a pin rig whose checkout has no origin: each branch holds its own commit, and feature is checked out
// with uncommitted work, so a launch that touches the checkout shows.
type localRig struct {
	*pinRig
	tips map[string]string
	head string
}

func newLocalRig(t *testing.T, defaultBranch string, branches ...string) *localRig {
	t.Helper()
	isolateGit(t)
	p := newPinRig(t, nil)
	p.l.Worktrees = GitWorktrees{}
	git(t, p.work, "init", "-q", "-b", branches[0])
	if defaultBranch != "" {
		git(t, p.work, "config", "init.defaultBranch", defaultBranch)
	}
	tips := map[string]string{}
	for i, b := range branches {
		if i > 0 {
			git(t, p.work, "checkout", "-q", "-b", b)
		}
		writeFile(t, filepath.Join(p.work, b+".txt"), b+"\n")
		git(t, p.work, "add", b+".txt")
		git(t, p.work, "commit", "-q", "-m", "on "+b)
		tips[b] = git(t, p.work, "rev-parse", "HEAD")
	}
	git(t, p.work, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(p.work, "feature.txt"), "feature\n")
	git(t, p.work, "add", "feature.txt")
	git(t, p.work, "commit", "-q", "-m", "on feature")
	writeFile(t, filepath.Join(p.work, "wip.txt"), "untracked\n")
	return &localRig{pinRig: p, tips: tips, head: git(t, p.work, "rev-parse", "HEAD")}
}

func (r *localRig) status(t *testing.T) string {
	t.Helper()
	return git(t, r.work, "status", "--porcelain")
}

// sessionOption is the tmux option Admiral keeps on a ship's session, "" when it is unset.
func sessionOption(t *testing.T, p *pinRig, session, name string) string {
	t.Helper()
	out, _ := exec.Command(p.l.Tmux, "-S", p.l.Socket, "show-options", "-t", "="+session+":", "-qv", sessionhost.Prefix+name).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func TestANoSkillLaunchFromACheckoutWithNoOriginRunsInAWorktreeCutFromTheDefaultBranch(t *testing.T) {
	for _, c := range []struct {
		name, defaultBranch string
		branches            []string
		want                string
	}{
		{"the configured default branch wins over main and master", "trunk", []string{"master", "main", "trunk"}, "trunk"},
		{"a configured default branch that does not exist falls back to main", "nope", []string{"master", "main"}, "main"},
		{"no configured default branch takes main over master", "", []string{"master", "main"}, "main"},
		{"master is the last resort", "", []string{"master"}, "master"},
		{"a configured default branch is enough with no main or master", "dev", []string{"dev"}, "dev"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newLocalRig(t, c.defaultBranch, c.branches...)
			out, err := r.l.Launch(r.noSkill())
			if err != nil {
				t.Fatalf("Launch = %v, want a ship in a worktree cut from %s", err, c.want)
			}
			cwd := r.cwd(t)
			if samePath(t, cwd, r.work) || !registered(t, r.work, cwd) {
				t.Fatalf("the harness ran in %s, want a worktree of the checkout: %v", cwd, worktreePaths(t, r.work))
			}
			if got := git(t, cwd, "branch", "--show-current"); got != "admiral/"+out.Session {
				t.Errorf("worktree branch = %q, want admiral/%s", got, out.Session)
			}
			if got := git(t, cwd, "rev-parse", "HEAD"); got != r.tips[c.want] {
				t.Errorf("worktree HEAD = %s, want the tip of %s %s", got, c.want, r.tips[c.want])
			}
		})
	}
}

func TestALocalOnlyLaunchLeavesTheCheckoutsHeadAndFilesAlone(t *testing.T) {
	r := newLocalRig(t, "", "main")
	before := r.status(t)
	if _, err := r.l.Launch(r.noSkill()); err != nil {
		t.Fatalf("Launch = %v", err)
	}
	if got := git(t, r.work, "branch", "--show-current"); got != "feature" {
		t.Errorf("checkout branch = %q, want feature untouched", got)
	}
	if got := git(t, r.work, "rev-parse", "HEAD"); got != r.head {
		t.Errorf("checkout HEAD = %s, want %s untouched", got, r.head)
	}
	if got := r.status(t); got != before {
		t.Errorf("checkout status = %q, want %q untouched", got, before)
	}
}

func TestALocalOnlyShipIsMarkedLocalOnlyWithItsBranchAndAGitHubShipIsNot(t *testing.T) {
	r := newLocalRig(t, "", "main")
	out, err := r.l.Launch(r.noSkill())
	if err != nil {
		t.Fatalf("Launch = %v", err)
	}
	if got := sessionOption(t, r.pinRig, out.Session, "local-only"); got != "1" {
		t.Errorf("local-only option = %q, want 1", got)
	}
	if got := sessionOption(t, r.pinRig, out.Session, "branch"); got != "admiral/"+out.Session {
		t.Errorf("branch option = %q, want admiral/%s", got, out.Session)
	}

	g := githubRig(t, "https://github.com/acme/api.git")
	gout, gerr := g.l.Launch(g.noSkill())
	if gerr != nil {
		t.Fatalf("GitHub-origin Launch = %v", gerr)
	}
	if got := sessionOption(t, g, gout.Session, "local-only"); got != "" {
		t.Errorf("a GitHub ship's local-only option = %q, want it unset", got)
	}
}

// githubRig is worktreeRig with its origin spelled as a hosted URL that an insteadOf rule sends to the local bare repo,
// as the e2e checkouts do: reading the URL through get-url would show the bare path and miss this.
func githubRig(t *testing.T, hosted string) *pinRig {
	t.Helper()
	p, _, _ := worktreeRig(t)
	bare := git(t, p.work, "config", "--get", "remote.origin.url")
	git(t, p.work, "config", "remote.origin.url", hosted)
	git(t, p.work, "config", "url."+bare+".insteadOf", hosted)
	return p
}

func TestALaunchFromACheckoutWithNoOriginAndNoDefaultBranchIsRefusedNamingTheBranchesLookedFor(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"an unborn HEAD", func(t *testing.T, dir string) { git(t, dir, "init", "-q", "-b", "main") }},
		{"only a branch that is not the default", func(t *testing.T, dir string) {
			commitRepo(t, dir)
			git(t, dir, "branch", "-m", "main", "dev")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolateGit(t)
			p := newPinRig(t, nil)
			p.l.Worktrees = GitWorktrees{}
			c.setup(t, p.work)
			_, err := p.l.Launch(p.noSkill())
			var lerr *Error
			if !errors.As(err, &lerr) {
				t.Fatalf("Launch error = %v, want a launch refusal", err)
			}
			if lerr.Status != 400 {
				t.Errorf("refusal status = %d, want 400", lerr.Status)
			}
			for _, name := range []string{"main", "master"} {
				if !strings.Contains(lerr.Msg, name) {
					t.Errorf("refusal = %q, want it to name %s", lerr.Msg, name)
				}
			}
			if got := p.sessionNames(t); len(got) != 0 {
				t.Errorf("tmux sessions = %v, want no ship put to sea", got)
			}
			if got := worktreePaths(t, p.work); len(got) != 1 {
				t.Errorf("git worktree list = %v, want the checkout alone", got)
			}
		})
	}
}

func TestALaunchFromANonGitHubOriginStillFetchesAndCutsFromOriginsDefaultTip(t *testing.T) {
	p := githubRig(t, "https://gitlab.com/acme/api.git")
	tip := git(t, p.work, "ls-remote", "origin", "trunk")
	out, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("Launch = %v", err)
	}
	cwd := p.cwd(t)
	if got, want := git(t, cwd, "rev-parse", "HEAD"), strings.Fields(tip)[0]; got != want {
		t.Errorf("worktree HEAD = %s, want origin's default tip %s", got, want)
	}
	if got := sessionOption(t, p, out.Session, "local-only"); got != "1" {
		t.Errorf("a GitLab ship's local-only option = %q, want 1", got)
	}
}

// ghClosingSteps is the closing steps a GitHub ship has always been given, copied here so a reworded constant shows.
const ghClosingSteps = "## Closing steps\n\n" +
	"When the work is done: commit it on your branch, push the branch, and open a pull request. " +
	"Then write your Admiral report to the file named by $ADMIRAL_REPORT as JSON with version 2, a result of done, partial or failed, " +
	"and prs listing the pull request you opened as [{\"url\": ...}], each url the full URL (https://host/owner/repo/pull/N). " +
	"If you found something worth reading later, write it in markdown to the file named by $ADMIRAL_FINDINGS."

func TestAQuickShipFromAGitHubOriginGetsTheClosingStepsByteForByte(t *testing.T) {
	p := githubRig(t, "https://github.com/acme/api.git")
	if _, err := p.l.Launch(p.chatLaunch()); err != nil {
		t.Fatalf("Launch = %v", err)
	}
	if got := p.lastArg(t); !strings.HasSuffix(got, ghClosingSteps) || !strings.HasPrefix(got, "ship the fix") {
		t.Errorf("prompt = %q, want the launch text then today's closing steps unchanged", got)
	}
}

func TestAQuickShipFromALocalOnlyCheckoutIsToldToCommitAndReportWithNoPR(t *testing.T) {
	norigin := newLocalRig(t, "", "main")
	gitlab := githubRig(t, "https://gitlab.com/acme/api.git")
	for _, c := range []struct {
		name string
		p    *pinRig
	}{
		{"no origin", norigin.pinRig},
		{"a non-GitHub origin", gitlab},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.p.l.Launch(c.p.chatLaunch()); err != nil {
				t.Fatalf("Launch = %v", err)
			}
			got := c.p.lastArg(t)
			low := strings.ToLower(got)
			if !strings.HasPrefix(got, "ship the fix") {
				t.Fatalf("prompt = %q, want AdmiralBot's launch text first", got)
			}
			for _, want := range []string{"commit", "do not push", "do not open a pull request", "admiral_report"} {
				if !strings.Contains(low, want) {
					t.Errorf("closing steps = %q, want them to say %q", got, want)
				}
			}
			if !regexp.MustCompile(`prs"?:?\s*\[\]`).MatchString(got) {
				t.Errorf("closing steps = %q, want them to tell the ship to report prs as an empty list", got)
			}
			for _, no := range []string{"push the branch", "full URL"} {
				if strings.Contains(got, no) {
					t.Errorf("closing steps = %q, want no %q for a ship with no GitHub", got, no)
				}
			}
		})
	}
}

func TestADraftingLaunchFromACheckoutWithNoOriginIsRefusedBecauseCampaignsNeedGitHub(t *testing.T) {
	isolateGit(t)
	p := newPinRig(t, nil)
	p.l.Worktrees = GitWorktrees{}
	commitRepo(t, p.work)
	skill := filepath.Join(p.work, ".claude", "skills", "wayfinder")
	mustMkdir(t, skill)
	putFile(t, filepath.Join(skill, "SKILL.md"), "---\nname: wayfinder\n---\n")
	_, err := p.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "skill", Name: "wayfinder"}, Text: "draft", Drafting: true})
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("Launch error = %v, want a launch refusal", err)
	}
	if lerr.Status != 400 || !strings.Contains(strings.ToLower(lerr.Msg), "campaign") || !strings.Contains(lerr.Msg, "GitHub") {
		t.Errorf("refusal = %d %q, want a 400 saying campaigns need a GitHub repo", lerr.Status, lerr.Msg)
	}
	if got := p.sessionNames(t); len(got) != 0 {
		t.Errorf("tmux sessions = %v, want no ship put to sea", got)
	}
}

func TestADraftingLaunchFromAGitHubOriginStillGetsItsWorktree(t *testing.T) {
	p := githubRig(t, "https://github.com/acme/api.git")
	skill := filepath.Join(p.work, ".claude", "skills", "wayfinder")
	mustMkdir(t, skill)
	putFile(t, filepath.Join(skill, "SKILL.md"), "---\nname: wayfinder\n---\n")
	_, cwd, _, _ := launchedDrafting(t, p, Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "skill", Name: "wayfinder"}, Text: "draft", Drafting: true})
	if samePath(t, cwd, p.work) || !registered(t, p.work, cwd) {
		t.Errorf("a drafting ship ran in %s, want a worktree of the checkout %s", cwd, p.work)
	}
}
