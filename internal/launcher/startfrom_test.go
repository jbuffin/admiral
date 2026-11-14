// A ship started from a restricted ship's work: its own clone, no resume, no restricted profile.
package launcher

import (
	"context"
	"github.com/jbuffin/admiral/internal/promote"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const promotedOrder = "Review the restricted work in admiral/calm-otter before building on it or pushing."

func startGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func startWrite(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// restrictedClone makes the request's dir a repo with one commit and a restricted ship's clone of it.
func (p *pinRig) restrictedClone(t *testing.T) (clone, branch string) {
	t.Helper()
	startGit(t, p.work, "init", "-q", "-b", "main")
	startWrite(t, filepath.Join(p.work, "a.txt"), "a\n")
	startGit(t, p.work, "add", "a.txt")
	startGit(t, p.work, "commit", "-q", "-m", "a")
	clone, err := GitCheckouts{}.Make(p.home, "restricted-one", p.work)
	if err != nil {
		t.Fatal(err)
	}
	return clone, "admiral/restricted-one"
}

func (p *pinRig) promoted(clone, branch string) Request {
	p.l.Checkouts = GitCheckouts{}
	p.guided(guidanceText, nil)
	// The caller reads the work first and hands the tip it saw, as the promote route does.
	tip := ""
	if w, err := promote.Read(context.Background(), p.work, clone, branch); err == nil {
		tip = w.Tip
	}
	return Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "none"}, Prompt: promotedOrder, By: "promote",
		StartFrom: &StartFrom{Clone: clone, Branch: branch, Tip: tip}}
}

func TestAShipStartedFromCommitsWorksInItsOwnCloneOnItsOwnBranchAtTheRestrictedTip(t *testing.T) {
	p := newPinRig(t, nil)
	clone, branch := p.restrictedClone(t)
	startWrite(t, filepath.Join(clone, "c.txt"), "the fix\n")
	startGit(t, clone, "add", "c.txt")
	startGit(t, clone, "commit", "-q", "-m", "fix")
	tip := startGit(t, clone, "rev-parse", "HEAD")

	out, err := p.l.Launch(p.promoted(clone, branch))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	dir := out.Options["dir"]
	if samePath(t, dir, clone) || samePath(t, dir, p.work) || !samePath(t, filepath.Dir(dir), filepath.Join(p.home, "checkouts")) {
		t.Fatalf("dir = %q, want the new ship's own clone under %s/checkouts, not %s or %s", dir, p.home, clone, p.work)
	}
	if got := startGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Errorf("HEAD = %s, want the restricted tip %s", got, tip)
	}
	if got := startGit(t, dir, "branch", "--show-current"); got != "admiral/"+out.Session {
		t.Errorf("branch = %q, want admiral/%s", got, out.Session)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "c.txt")); err != nil || string(b) != "the fix\n" {
		t.Errorf("c.txt = %q (%v), want the restricted ship's file", b, err)
	}
}

func TestAShipStartedFromAnUncommittedDiffGetsItAppliedAndNoAdmiralFiles(t *testing.T) {
	p := newPinRig(t, nil)
	clone, branch := p.restrictedClone(t)
	startWrite(t, filepath.Join(clone, "a.txt"), "a changed\n")
	startWrite(t, filepath.Join(clone, "new.txt"), "brand new\n")
	startWrite(t, filepath.Join(clone, ".admiral", "untrusted", "event.md"), "canary-event-text\n")

	out, err := p.l.Launch(p.promoted(clone, branch))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	dir := out.Options["dir"]
	for file, want := range map[string]string{"a.txt": "a changed\n", "new.txt": "brand new\n"} {
		if b, err := os.ReadFile(filepath.Join(dir, file)); err != nil || string(b) != want {
			t.Errorf("%s = %q (%v), want %q", file, b, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".admiral", "untrusted")); err == nil {
		t.Error("the new clone has .admiral/untrusted/")
	}
}

func TestAShipStartedFromRestrictedWorkIsANewSessionOnTheOrderAloneWithNoResumeNoRestrictedProfileNoGuidance(t *testing.T) {
	p := newPinRig(t, nil)
	clone, branch := p.restrictedClone(t)
	startWrite(t, filepath.Join(clone, "c.txt"), "the fix\n")
	startGit(t, clone, "add", "c.txt")
	startGit(t, clone, "commit", "-q", "-m", "fix")
	req := p.promoted(clone, branch)
	req.WorkerLaunched = false

	out, err := p.l.Launch(req)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(out.Options["dir"], "c.txt")); err != nil || string(b) != "the fix\n" {
		t.Fatalf("c.txt = %q (%v): the ship did not start from the restricted work", b, err)
	}
	argv, _ := p.shipRecord(t)
	if !slices.Contains(argv, "--session-id") {
		t.Errorf("argv = %q, want a new --session-id", argv)
	}
	for _, f := range []string{"--resume", "-r", "-c", "--continue", "--permission-mode", "--setting-sources"} {
		if slices.Contains(argv, f) {
			t.Errorf("argv = %q, want no %s", argv, f)
		}
	}
	if got := argv[len(argv)-1]; got != promotedOrder {
		t.Errorf("prompt = %q, want the order alone: %q", got, promotedOrder)
	}
	if _, err := os.Stat(restrictedSettingsPath(p.home, out.Session)); err == nil {
		t.Error("a promoted ship got a restricted settings file")
	}
}

func TestAStartFromWhoseFetchFailsFailsTheLaunchAndLeavesNoCloneAndNoSession(t *testing.T) {
	p := newPinRig(t, nil)
	clone, _ := p.restrictedClone(t)
	for name, req := range map[string]Request{
		"a branch the clone lacks": p.promoted(clone, "admiral/no-such-branch"),
		"a clone that is gone":     p.promoted(filepath.Join(p.home, "checkouts", "gone"), "admiral/restricted-one"),
	} {
		if _, err := p.l.Launch(req); err == nil {
			t.Fatalf("%s: Launch succeeded, want it to fail", name)
		}
		left, _ := os.ReadDir(filepath.Join(p.home, "checkouts"))
		if len(left) != 1 || left[0].Name() != "restricted-one" {
			t.Errorf("%s: checkouts = %v, want only the restricted clone", name, left)
		}
		if out, err := exec.Command(p.l.Tmux, "-S", p.l.Socket, "list-sessions").CombinedOutput(); err == nil {
			t.Errorf("%s: a failed launch left sessions: %s", name, out)
		}
	}
}

func TestAStartFromWhoseTipIsNotTheOneTheCallerReadFailsAfterTheCloneExistsAndRemovesIt(t *testing.T) {
	p := newPinRig(t, nil)
	clone, branch := p.restrictedClone(t)
	startWrite(t, filepath.Join(clone, "c.txt"), "the fix\n")
	startGit(t, clone, "add", "c.txt")
	startGit(t, clone, "commit", "-q", "-m", "fix")
	req := p.promoted(clone, branch)
	req.StartFrom.Tip = "0000000000000000000000000000000000000001"
	_, err := p.l.Launch(req)
	if err == nil {
		t.Fatal("Launch succeeded, want the tip mismatch to fail it")
	}
	if left, _ := os.ReadDir(filepath.Join(p.home, "checkouts")); len(left) != 1 || left[0].Name() != "restricted-one" {
		t.Errorf("checkouts = %v, want only the restricted clone", left)
	}
	if strings.Contains(err.Error(), "c.txt") {
		t.Errorf("the refusal %q names a file of the work", err)
	}
}

func TestAStartFromRefusesWorkThatTouchesAdmiralOrHarnessConfigEvenWhenAReplaceRefHidesIt(t *testing.T) {
	for _, path := range []string{".admiral/untrusted/event.md", ".claude/settings.json"} {
		p := newPinRig(t, nil)
		clone, branch := p.restrictedClone(t)
		startWrite(t, filepath.Join(clone, "c.txt"), "c\n")
		startGit(t, clone, "add", "c.txt")
		startGit(t, clone, "commit", "-q", "-m", "c")
		startWrite(t, filepath.Join(clone, path), "smuggled\n")
		startGit(t, clone, "add", "-f", path)
		startGit(t, clone, "commit", "-q", "-m", "bad")
		fake := startGit(t, clone, "commit-tree", "HEAD~1^{tree}", "-p", "HEAD~1", "-m", "look-alike")
		startGit(t, clone, "replace", "HEAD", fake)
		if _, err := p.l.Launch(p.promoted(clone, branch)); err == nil {
			t.Errorf("%s hidden by a replace ref: Launch succeeded, want it refused", path)
		}
		if left, _ := os.ReadDir(filepath.Join(p.home, "checkouts")); len(left) != 1 {
			t.Errorf("%s: checkouts = %v, want only the restricted clone", path, left)
		}
	}
}
