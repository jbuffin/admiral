package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// originWorld gives work a local bare origin whose default branch is trunk and one commit ahead of work's HEAD, with
// work holding uncommitted changes. It answers origin's tip and work's HEAD.
func originWorld(t *testing.T, work string) (tip, head string) {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, bare, "init", "-q", "--bare", "-b", "trunk")
	scratch := t.TempDir()
	git(t, scratch, "init", "-q", "-b", "trunk")
	writeFile(t, filepath.Join(scratch, "a.txt"), "committed\n")
	git(t, scratch, "add", "a.txt")
	git(t, scratch, "commit", "-q", "-m", "first")
	git(t, scratch, "remote", "add", "origin", bare)
	git(t, scratch, "push", "-q", "origin", "trunk")

	git(t, work, "init", "-q", "-b", "main")
	git(t, work, "remote", "add", "origin", bare)
	git(t, work, "fetch", "-q", "origin")
	git(t, work, "checkout", "-q", "-B", "main", "origin/trunk")

	writeFile(t, filepath.Join(scratch, "b.txt"), "ahead\n")
	git(t, scratch, "add", "b.txt")
	git(t, scratch, "commit", "-q", "-m", "second")
	git(t, scratch, "push", "-q", "origin", "trunk")

	writeFile(t, filepath.Join(work, "a.txt"), "edited and not committed\n")
	writeFile(t, filepath.Join(work, "wip.txt"), "untracked\n")
	return git(t, bare, "rev-parse", "trunk"), git(t, work, "rev-parse", "HEAD")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// worktreePaths is every path git lists for repo's worktrees, the repo's own included.
func worktreePaths(t *testing.T, repo string) []string {
	t.Helper()
	var paths []string
	for _, line := range strings.Split(git(t, repo, "worktree", "list", "--porcelain"), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			if real, err := filepath.EvalSymlinks(p); err == nil {
				p = real
			}
			paths = append(paths, p)
		}
	}
	return paths
}

func registered(t *testing.T, repo, path string) bool {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for _, p := range worktreePaths(t, repo) {
		if p == real {
			return true
		}
	}
	return false
}

func (p *pinRig) noSkill() Request {
	return Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "none"}}
}

// cwd is where the harness of the next recorded launch ran.
func (p *pinRig) cwd(t *testing.T) string {
	t.Helper()
	return p.recorded(t, "claude")["PWD"]
}

func (p *pinRig) killSession(t *testing.T, session string) {
	t.Helper()
	if out, err := exec.Command(p.l.Tmux, "-S", p.l.Socket, "kill-session", "-t", session).CombinedOutput(); err != nil {
		t.Fatalf("kill-session: %v: %s", err, out)
	}
}

func (p *pinRig) sessionNames(t *testing.T) []string {
	t.Helper()
	rows, err := p.l.server().SessionRows()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r[0])
	}
	return out
}

// worktreeRig is a pin rig whose work dir is a checkout with origin's default branch ahead of its HEAD.
func worktreeRig(t *testing.T) (p *pinRig, tip, head string) {
	t.Helper()
	p = newPinRig(t, nil)
	p.l.Worktrees = GitWorktrees{}
	tip, head = originWorld(t, p.work)
	return p, tip, head
}

func TestANoSkillLaunchRunsInAFreshWorktreeOnItsOwnBranchCutFromOriginsDefaultTip(t *testing.T) {
	p, tip, head := worktreeRig(t)
	out, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	cwd := p.cwd(t)
	if samePath(t, cwd, p.work) {
		t.Fatalf("the harness ran in the checkout %s, want a worktree of its own", cwd)
	}
	if !registered(t, p.work, cwd) {
		t.Errorf("cwd %s is not in git worktree list of the checkout: %v", cwd, worktreePaths(t, p.work))
	}
	if got := git(t, cwd, "branch", "--show-current"); got != "admiral/"+out.Session {
		t.Errorf("worktree branch = %q, want admiral/%s", got, out.Session)
	}
	if got := git(t, cwd, "rev-parse", "HEAD"); got != tip {
		t.Errorf("worktree HEAD = %s, want origin's default-branch tip %s (the checkout's HEAD is %s)", got, tip, head)
	}
	if !samePath(t, out.Options["dir"], p.work) {
		t.Errorf("dir option = %s, want the engineer's checkout %s", out.Options["dir"], p.work)
	}
}

func TestANoSkillLaunchLeavesTheEngineersCheckoutWithItsHeadBranchAndStatus(t *testing.T) {
	p, _, head := worktreeRig(t)
	statusBefore := git(t, p.work, "status", "--porcelain")
	if _, err := p.l.Launch(p.noSkill()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	cwd := p.cwd(t)
	if samePath(t, cwd, p.work) {
		t.Fatalf("the harness ran in the checkout, want a worktree of its own")
	}
	if got := git(t, p.work, "rev-parse", "HEAD"); got != head {
		t.Errorf("checkout HEAD = %s, want %s", got, head)
	}
	if got := git(t, p.work, "branch", "--show-current"); got != "main" {
		t.Errorf("checkout branch = %q, want main", got)
	}
	if got := git(t, p.work, "status", "--porcelain"); got != statusBefore {
		t.Errorf("checkout status = %q, want %q", got, statusBefore)
	}
	if _, err := os.Stat(filepath.Join(p.work, ".admiral")); err == nil {
		t.Error("the checkout got a .admiral directory")
	}
	if got := worktreePaths(t, p.work); len(got) != 2 {
		t.Errorf("git worktree list = %v, want the checkout and the ship's worktree only", got)
	}
}

func TestBeforeRunsInTheWorktreeSoARoundsFeedbackNeverLandsInTheCheckout(t *testing.T) {
	p, _, _ := worktreeRig(t)
	req := p.noSkill()
	var got string
	req.Before = func(dir string) error {
		got = dir
		return os.MkdirAll(filepath.Join(dir, ".admiral", "untrusted"), 0o755)
	}
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got == "" || samePath(t, got, p.work) {
		t.Fatalf("Before got %q, want the ship's worktree, not the checkout %s", got, p.work)
	}
	if !registered(t, p.work, got) {
		t.Errorf("Before's dir %s is not a worktree of the checkout: %v", got, worktreePaths(t, p.work))
	}
	if _, err := os.Stat(filepath.Join(p.work, ".admiral")); err == nil {
		t.Error("what Before wrote landed in the checkout")
	}
}

func TestARelaunchOfANoSkillShipRunsInTheSameWorktreeWithNoNewWorktreeOrBranch(t *testing.T) {
	p, _, _ := worktreeRig(t)
	first, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	cwd := p.cwd(t)
	if samePath(t, cwd, p.work) {
		t.Fatal("the first launch ran in the checkout, want a worktree of its own")
	}
	writeFile(t, filepath.Join(cwd, "work.txt"), "the ship's work")
	branches := git(t, p.work, "branch", "--list", "admiral/*")
	p.killSession(t, first.Session)

	req := p.noSkill()
	req.Session, req.Name = first.Session, first.Options["name"]
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("relaunch: %v", err)
	}
	if got := p.cwd(t); !samePath(t, got, cwd) {
		t.Errorf("relaunch cwd = %s, want the same worktree %s", got, cwd)
	}
	if got := worktreePaths(t, p.work); len(got) != 2 {
		t.Errorf("git worktree list = %v, want still the checkout and one worktree", got)
	}
	if got := git(t, p.work, "branch", "--list", "admiral/*"); got != branches {
		t.Errorf("branches = %q, want %q", got, branches)
	}
	if b, err := os.ReadFile(filepath.Join(cwd, "work.txt")); err != nil || string(b) != "the ship's work" {
		t.Errorf("the ship's uncommitted work = %q (%v), want it kept", b, err)
	}
}

func TestAFailedRelaunchNeverRemovesTheWorktreeItReused(t *testing.T) {
	p, _, _ := worktreeRig(t)
	first, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	cwd := p.cwd(t)
	if samePath(t, cwd, p.work) {
		t.Fatal("the first launch ran in the checkout, want a worktree of its own")
	}
	writeFile(t, filepath.Join(cwd, "work.txt"), "the ship's work")
	p.killSession(t, first.Session)

	req := p.noSkill()
	req.Session, req.Name = first.Session, first.Options["name"]
	req.Before = func(string) error { return errors.New("stop here") }
	if _, err := p.l.Launch(req); err == nil {
		t.Fatal("relaunch succeeded, want Before's error")
	}
	if b, err := os.ReadFile(filepath.Join(cwd, "work.txt")); err != nil || string(b) != "the ship's work" {
		t.Errorf("the reused worktree's work = %q (%v), want it left alone", b, err)
	}
	if !registered(t, p.work, cwd) {
		t.Errorf("the reused worktree %s is no longer registered", cwd)
	}
}

func TestAFailedFirstLaunchRemovesTheWorktreeItMade(t *testing.T) {
	p, _, _ := worktreeRig(t)
	req := p.noSkill()
	var made string
	req.Before = func(dir string) error {
		made = dir
		if samePath(t, dir, p.work) || !registered(t, p.work, dir) {
			t.Errorf("Before's dir %s is not a worktree of the checkout: %v", dir, worktreePaths(t, p.work))
		}
		return errors.New("stop here")
	}
	if _, err := p.l.Launch(req); err == nil {
		t.Fatal("Launch succeeded, want Before's error")
	}
	if made == "" {
		t.Fatal("Before never ran")
	}
	if _, err := os.Stat(made); err == nil {
		t.Errorf("the worktree %s outlived the failed launch", made)
	}
	if got := worktreePaths(t, p.work); len(got) != 1 {
		t.Errorf("git worktree list = %v, want the checkout alone", got)
	}
}

func TestALaunchFromACheckoutWithNoOriginFailsVisiblyAndPutsNoShipToSea(t *testing.T) {
	p := newPinRig(t, nil)
	p.l.Worktrees = GitWorktrees{}
	commitRepo(t, p.work)
	_, err := p.l.Launch(p.noSkill())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("Launch error = %v, want a launch refusal", err)
	}
	if !strings.Contains(lerr.Msg, "origin") {
		t.Errorf("refusal = %q, want it to name origin", lerr.Msg)
	}
	if got := p.sessionNames(t); len(got) != 0 {
		t.Errorf("tmux sessions = %v, want no ship put to sea", got)
	}
	if got := worktreePaths(t, p.work); len(got) != 1 {
		t.Errorf("git worktree list = %v, want the checkout alone", got)
	}
}

func TestALaunchWhoseFetchFailsFailsVisiblyInsteadOfFallingBackToTheLocalHead(t *testing.T) {
	p := newPinRig(t, nil)
	p.l.Worktrees = GitWorktrees{}
	commitRepo(t, p.work)
	git(t, p.work, "remote", "add", "origin", filepath.Join(t.TempDir(), "gone.git"))
	_, err := p.l.Launch(p.noSkill())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("Launch error = %v, want a launch refusal", err)
	}
	if !strings.Contains(lerr.Msg, "origin") {
		t.Errorf("refusal = %q, want it to name origin", lerr.Msg)
	}
	if got := p.sessionNames(t); len(got) != 0 {
		t.Errorf("tmux sessions = %v, want no ship put to sea", got)
	}
	if got := worktreePaths(t, p.work); len(got) != 1 {
		t.Errorf("git worktree list = %v, want the checkout alone", got)
	}
}

func TestAWorktreeIsMadeOnlyWhenTheSearchRootIsAGitCheckout(t *testing.T) {
	p := newPinRig(t, nil)
	p.l.Worktrees = GitWorktrees{}
	if _, err := p.l.Launch(p.noSkill()); err != nil {
		t.Fatalf("Launch in a plain folder: %v", err)
	}
	if got := p.cwd(t); !samePath(t, got, p.work) {
		t.Errorf("a plain folder's ship ran in %s, want it in place at %s", got, p.work)
	}

	originWorld(t, p.work)
	if _, err := p.l.Launch(p.noSkill()); err != nil {
		t.Fatalf("Launch in a checkout: %v", err)
	}
	if got := p.cwd(t); samePath(t, got, p.work) || !registered(t, p.work, got) {
		t.Errorf("the checkout's ship ran in %s, want a worktree of it", got)
	}
}

func TestOnlyAShipLaunchedWithNoSkillGetsAWorktree(t *testing.T) {
	p, _, _ := worktreeRig(t)
	req := p.noSkill()
	req.Skill = Skill{Kind: "commodore"}
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("Launch with a skill: %v", err)
	}
	if got := p.cwd(t); !samePath(t, got, p.work) {
		t.Errorf("a skill ship ran in %s, want the checkout %s", got, p.work)
	}
	if got := worktreePaths(t, p.work); len(got) != 1 {
		t.Errorf("git worktree list = %v after a skill launch, want the checkout alone", got)
	}

	if _, err := p.l.Launch(p.noSkill()); err != nil {
		t.Fatalf("Launch with no skill: %v", err)
	}
	if got := p.cwd(t); samePath(t, got, p.work) || !registered(t, p.work, got) {
		t.Errorf("a no-skill ship ran in %s, want a worktree of the checkout", got)
	}
}

func TestAnEventShipKeepsItsOwnCloneWhileANoSkillShipGetsAWorktree(t *testing.T) {
	p, _, _ := worktreeRig(t)
	req, fc := p.eventShip()
	fc.t = t
	out, err := p.l.Launch(req)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := p.cwd(t); !samePath(t, got, filepath.Join(p.home, "checkouts", out.Session)) {
		t.Errorf("an event ship ran in %s, want its clone", got)
	}
	if got := worktreePaths(t, p.work); len(got) != 1 {
		t.Errorf("git worktree list = %v after an event ship, want the checkout alone", got)
	}

	if _, err := p.l.Launch(p.noSkill()); err != nil {
		t.Fatalf("Launch with no skill: %v", err)
	}
	if got := p.cwd(t); samePath(t, got, p.work) || !registered(t, p.work, got) {
		t.Errorf("a no-skill ship ran in %s, want a worktree of the checkout", got)
	}
}

// blockOtherNames takes every name in the pool but keep's, so a launch can only get keep's unless something else takes it.
func blockOtherNames(t *testing.T, p *pinRig, keep string) {
	t.Helper()
	for _, n := range names {
		if Slug(n) == keep {
			continue
		}
		if err := os.MkdirAll(filepath.Join(p.home, "checkouts", Slug(n)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestANameWhoseWorktreeADismissKeptIsNotGivenToANewShip(t *testing.T) {
	p, _, _ := worktreeRig(t)
	first, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	p.cwd(t)
	p.killSession(t, first.Session)
	blockOtherNames(t, p, first.Session)

	second, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("second Launch: %v", err)
	}
	if second.Session == first.Session {
		t.Errorf("a new ship got the name %s, whose worktree is still there", first.Session)
	}
}

func TestANameWhoseBranchSurvivedItsWorktreesRemovalIsNotGivenToANewShip(t *testing.T) {
	p, _, _ := worktreeRig(t)
	first, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	cwd := p.cwd(t)
	p.killSession(t, first.Session)
	if samePath(t, cwd, p.work) {
		t.Fatal("the first launch ran in the checkout, want a worktree of its own")
	}
	git(t, p.work, "worktree", "remove", "--force", cwd)
	if got := git(t, p.work, "branch", "--list", "admiral/"+first.Session); got == "" {
		t.Fatalf("the branch admiral/%s is gone after the worktree was removed", first.Session)
	}
	blockOtherNames(t, p, first.Session)

	second, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("second Launch: %v", err)
	}
	if second.Session == first.Session {
		t.Errorf("a new ship got the name %s, whose branch is still in the repo", first.Session)
	}
}

func TestARelaunchOfAShipWithNoWorktreeRunsInTheCheckoutAndMakesNone(t *testing.T) {
	p, _, _ := worktreeRig(t)
	p.l.Worktrees = nil // a ship moored before ships got worktrees
	first, err := p.l.Launch(p.noSkill())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	p.cwd(t)
	p.killSession(t, first.Session)
	p.l.Worktrees = GitWorktrees{}

	req := p.noSkill()
	req.Session, req.Name = first.Session, first.Options["name"]
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("relaunch: %v", err)
	}
	if got := p.cwd(t); !samePath(t, got, p.work) {
		t.Errorf("relaunch cwd = %s, want the checkout %s", got, p.work)
	}
	if got := worktreePaths(t, p.work); len(got) != 1 {
		t.Errorf("git worktree list = %v, want only the checkout", got)
	}
	if got := git(t, p.work, "branch", "--list", "admiral/*"); got != "" {
		t.Errorf("branches = %q, want no admiral branch", got)
	}
}

func TestALaunchWhoseFetchHangsIsRefusedAsTimedOutAndPutsNoShipToSea(t *testing.T) {
	p, _, _ := worktreeRig(t)
	p.l.Worktrees = GitWorktrees{Timeout: 200 * time.Millisecond}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	writeFile(t, filepath.Join(shim, "git"), "#!/bin/sh\ncase \"$*\" in *ls-remote*) exec sleep 60 ;; esac\nexec "+real+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(shim, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err = p.l.Launch(p.noSkill())
	if err == nil || !strings.Contains(err.Error(), "fetching the origin remote") || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Launch error = %v, want a refusal that says the git call timed out", err)
	}
	if got := p.sessionNames(t); len(got) != 0 {
		t.Errorf("sessions = %v, want no ship put to sea", got)
	}
}

func TestTheCheckoutsSharedExcludeFileIsUntouchedAndAFeedbackRoundLeavesTheWorktreeClean(t *testing.T) {
	p, _, _ := worktreeRig(t)
	exclude := filepath.Join(p.work, ".git", "info", "exclude")
	before, _ := os.ReadFile(exclude)
	req := p.noSkill()
	var wt string
	req.Before = func(dir string) error {
		wt = dir
		_, err := moor.WriteFeedback(dir, 1, "feedback")
		return err
	}
	if _, err := p.l.Launch(req); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got := git(t, wt, "status", "--porcelain"); got != "" {
		t.Errorf("the worktree's status after a feedback round = %q, want clean", got)
	}
	if after, _ := os.ReadFile(exclude); string(after) != string(before) {
		t.Errorf("the checkout's info/exclude changed from %q to %q", before, after)
	}
}
