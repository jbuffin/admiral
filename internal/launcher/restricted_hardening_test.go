package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

func TestAnExistingCheckoutKeepsItsNameOutOfThePool(t *testing.T) {
	p := newPinRig(t, nil)
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(p.home, "checkouts", Slug(n)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := p.l.pickName()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, " II") {
		t.Errorf("pickName = %q with every name's checkout kept, want a numbered name", got)
	}
}

func TestARelaunchOfARestrictedShipReusesItsKeptCloneAndALateFailureLeavesIt(t *testing.T) {
	p := newPinRig(t, nil)
	req, fc := p.restricted(standing.DraftFix)
	fc.t = t
	req.Session, req.Name = "kelpie", "Kelpie"
	kept := filepath.Join(p.home, "checkouts", "kelpie")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "work.txt"), []byte("the ship's work"), 0o644); err != nil {
		t.Fatal(err)
	}
	req.Before = func(string) error { return errors.New("stop here") }
	if _, err := p.l.Launch(req); err == nil {
		t.Fatal("Launch succeeded, want Before's error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("the seam was asked to make a clone over a kept one: %v", fc.calls)
	}
	if b, err := os.ReadFile(filepath.Join(kept, "work.txt")); err != nil || string(b) != "the ship's work" {
		t.Errorf("the kept clone's work = %q (%v), want it left alone", b, err)
	}
	if _, err := os.Stat(restrictedSettingsPath(p.home, "kelpie")); err == nil {
		t.Error("the settings file outlived the failed launch")
	}
}

func TestALateFailureAfterTheCheckoutRemovesTheCloneAndTheSettingsFile(t *testing.T) {
	p := newPinRig(t, nil)
	req, fc := p.restricted(standing.DraftFix)
	fc.t = t
	req.Before = func(string) error { return errors.New("stop here") }
	if _, err := p.l.Launch(req); err == nil {
		t.Fatal("Launch succeeded, want Before's error")
	}
	if fc.madeFor == "" {
		t.Fatal("no checkout was made")
	}
	if _, err := os.Stat(fc.madeFor); err == nil {
		t.Errorf("the clone %s outlived the failed launch", fc.madeFor)
	}
	session := filepath.Base(fc.madeFor)
	if _, err := os.Stat(restrictedSettingsPath(p.home, session)); err == nil {
		t.Error("the settings file outlived the failed launch")
	}
}

func TestTheRestrictedSettingsDenyReadingTheHomeAndAdmiralsHomeExceptTheCheckoutAndCaches(t *testing.T) {
	for _, level := range []standing.Restricted{standing.Investigate, standing.DraftFix} {
		t.Run(string(level), func(t *testing.T) {
			p := newPinRig(t, nil)
			_, fc, argv, _ := p.mustRestricted(t, level)
			_, got := settingsOf(t, argv)
			fs := sub(t, got, "sandbox", "filesystem")
			deny, allow := strs(t, fs["denyRead"]), strs(t, fs["allowRead"])
			realHome, _ := filepath.EvalSymlinks(p.home)
			for _, want := range []string{"~/", realHome, "~/.gem/credentials"} {
				if !slices.Contains(deny, want) {
					t.Errorf("denyRead = %q, want %q", deny, want)
				}
			}
			for _, want := range append([]string{fc.madeFor}, toolchainCaches...) {
				if !slices.Contains(allow, want) {
					t.Errorf("allowRead = %q, want %q", allow, want)
				}
			}
			if v, _ := sub(t, got, "permissions")["blockReadsOutsideWorkingDirectories"].(bool); !v {
				t.Error("permissions.blockReadsOutsideWorkingDirectories is not true")
			}
		})
	}
}

func TestARealCloneSandboxMayReadTheObjectsItSharesWithItsDir(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	p.l.Checkouts = GitCheckouts{}
	p.guided(guidanceText, nil)
	out, err := p.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "none"},
		Restricted: standing.Investigate, EventText: rawEvent, Template: "Triage."})
	if err != nil {
		t.Fatal(err)
	}
	argv, _ := p.shipRecord(t)
	_, got := settingsOf(t, argv)
	want, _ := filepath.EvalSymlinks(filepath.Join(p.work, ".git", "objects"))
	if allow := strs(t, sub(t, got, "sandbox", "filesystem")["allowRead"]); !slices.Contains(allow, want) {
		t.Errorf("allowRead = %q, want the dir's objects %s for ship %s", allow, want, out.Session)
	}
}

func TestTheRestrictedSettingsDenyEveryToolThatReachesOtherSessionsOrOffTheMachine(t *testing.T) {
	p := newPinRig(t, nil)
	_, _, argv, _ := p.mustRestricted(t, standing.DraftFix)
	_, got := settingsOf(t, argv)
	deny := strs(t, sub(t, got, "permissions")["deny"])
	for _, tool := range []string{"SendMessage", "ListAgents", "RemoteTrigger", "PushNotification", "Artifact", "SendUserFile", "SendFeedback"} {
		if !slices.Contains(deny, tool) {
			t.Errorf("permissions.deny = %q, want %s", deny, tool)
		}
	}
}

func TestADraftFixShipsSandboxCannotWriteGitCommondirOrConfigWorktreeAndAnInvestigateShipsCheckoutIsDenied(t *testing.T) {
	p := newPinRig(t, nil)
	_, fc, argv, _ := p.mustRestricted(t, standing.DraftFix)
	_, got := settingsOf(t, argv)
	deny := strs(t, sub(t, got, "sandbox", "filesystem")["denyWrite"])
	for _, f := range []string{"commondir", "config.worktree"} {
		if !slices.Contains(deny, filepath.Join(fc.madeFor, ".git", f)) {
			t.Errorf("Draft fix denyWrite = %q, want .git/%s", deny, f)
		}
	}
	p = newPinRig(t, nil)
	_, fc, argv, _ = p.mustRestricted(t, standing.Investigate)
	_, got = settingsOf(t, argv)
	if deny := strs(t, sub(t, got, "sandbox", "filesystem")["denyWrite"]); !slices.Contains(deny, fc.madeFor) {
		t.Errorf("Investigate denyWrite = %q, want the checkout", deny)
	}
}

func TestTheRestrictedSettingsPinWhatARepoSettingsFileCouldWiden(t *testing.T) {
	p := newPinRig(t, nil)
	_, _, argv, _ := p.mustRestricted(t, standing.DraftFix)
	_, got := settingsOf(t, argv)
	nw := sub(t, got, "sandbox", "network")
	for _, k := range []string{"allowLocalBinding", "allowAllUnixSockets"} {
		if v, ok := nw[k].(bool); !ok || v {
			t.Errorf("sandbox.network.%s = %v, want false", k, nw[k])
		}
	}
	if v, ok := got["disableAllHooks"].(bool); !ok || v {
		t.Errorf("disableAllHooks = %v, want false", got["disableAllHooks"])
	}
}

func TestACommittedSymlinkInTheCloneCannotSendTheEventFileOutsideIt(t *testing.T) {
	for _, link := range []string{".admiral", ".admiral/untrusted"} {
		t.Run(link, func(t *testing.T) {
			p := newPinRig(t, nil)
			outside := t.TempDir()
			commitRepo(t, p.work)
			if link == ".admiral" {
				if err := os.Symlink(outside, filepath.Join(p.work, ".admiral")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Join(p.work, ".admiral"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(p.work, ".admiral", "untrusted")); err != nil {
					t.Fatal(err)
				}
			}
			git(t, p.work, "add", "-f", ".admiral")
			git(t, p.work, "-c", "user.email=a@b.c", "-c", "user.name=a", "commit", "-m", "link")
			p.l.Checkouts = GitCheckouts{}
			p.guided(guidanceText, nil)
			_, err := p.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "none"},
				Restricted: standing.DraftFix, EventText: rawEvent, Template: "Triage."})
			if err == nil {
				t.Fatal("Launch succeeded over a linked event folder")
			}
			if ents, _ := os.ReadDir(outside); len(ents) != 0 {
				t.Errorf("the event reached %s: %v", outside, ents)
			}
			if out, err := exec.Command(p.l.Tmux, "-S", p.l.Socket, "list-sessions").CombinedOutput(); err == nil {
				t.Errorf("a refused launch left sessions: %s", out)
			}
			if ents, _ := os.ReadDir(filepath.Join(p.home, "checkouts")); len(ents) != 0 {
				t.Errorf("a refused launch left clones: %v", ents)
			}
		})
	}
}

func TestACheckoutIsReturnedByItsResolvedPath(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	commitRepo(t, dir)
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	path, err := GitCheckouts{}.Make(link, "kelpie", dir)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(path); path != want {
		t.Errorf("Make = %s, want the resolved %s", path, want)
	}
}

func TestAnEventFileThatIsALinkInsideTheCloneIsReplacedNotWrittenThrough(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".admiral", "untrusted"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "config")
	if err := os.WriteFile(cfg, []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../config", filepath.Join(root, ".admiral", "untrusted", "event.md")); err != nil {
		t.Fatal(err)
	}
	if err := writeEventFile(root, rawEvent); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "[core]\n" {
		t.Errorf("the linked file now holds %q, want it untouched", b)
	}
	if b, err := os.ReadFile(filepath.Join(root, ".admiral", "untrusted", "event.md")); err != nil || string(b) != rawEvent {
		t.Errorf("event.md = %q (%v), want the event", b, err)
	}
}

func TestARelaunchOfARestrictedShipSucceedsInItsKeptCloneWithoutMakingANewOne(t *testing.T) {
	p := newPinRig(t, nil)
	req, fc := p.restricted(standing.DraftFix)
	fc.t = t
	req.Session, req.Name = "kelpie", "Kelpie"
	kept := filepath.Join(p.home, "checkouts", "kelpie")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := p.l.Launch(req)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("the seam was asked to make a clone over a kept one: %v", fc.calls)
	}
	if !samePath(t, out.Options["dir"], kept) {
		t.Errorf("dir = %s, want the kept clone %s", out.Options["dir"], kept)
	}
}

// keptDraftFixClone makes the clone an earlier round of a Draft fix ship would have left under session.
func keptDraftFixClone(t *testing.T, p *pinRig, dir, session string) string {
	t.Helper()
	path, err := GitCheckouts{}.Make(p.home, session, dir)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// relaunchDraftFix relaunches session in its kept clone and returns the settings the ship was given.
func relaunchDraftFix(t *testing.T, p *pinRig, dir, session string) (map[string]any, error) {
	t.Helper()
	p.l.Checkouts = GitCheckouts{}
	p.guided(guidanceText, nil)
	if _, err := p.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: dir, Skill: Skill{Kind: "none"},
		Restricted: standing.DraftFix, Template: "Fix it.", Session: session, Name: "Kelpie"}); err != nil {
		return nil, err
	}
	argv, _ := p.shipRecord(t)
	_, got := settingsOf(t, argv)
	return got, nil
}

func TestAShipCannotWidenItsReadsThroughTheAlternatesFileOfItsKeptClone(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	clone := keptDraftFixClone(t, p, p.work, "kelpie")
	f, err := os.OpenFile(filepath.Join(clone, ".git", "objects", "info", "alternates"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("/Users/x/.ssh\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := relaunchDraftFix(t, p, p.work, "kelpie")
	if err != nil {
		t.Fatal(err)
	}
	allow := strs(t, sub(t, got, "sandbox", "filesystem")["allowRead"])
	if slices.Contains(allow, "/Users/x/.ssh") {
		t.Errorf("allowRead = %q holds a path the ship wrote into alternates", allow)
	}
	objects, _ := filepath.EvalSymlinks(filepath.Join(p.work, ".git", "objects"))
	if !slices.Contains(allow, objects) {
		t.Errorf("allowRead = %q, want the dir's objects %s", allow, objects)
	}
}

func TestALinkedWorktreeDirAllowsItsCommonObjectDirectory(t *testing.T) {
	p := newPinRig(t, nil)
	main := t.TempDir()
	commitRepo(t, main)
	git(t, main, "worktree", "add", "-q", "-b", "side", p.work)
	keptDraftFixClone(t, p, p.work, "kelpie")
	got, err := relaunchDraftFix(t, p, p.work, "kelpie")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(main, ".git", "objects"))
	if allow := strs(t, sub(t, got, "sandbox", "filesystem")["allowRead"]); !slices.Contains(allow, want) {
		t.Errorf("allowRead = %q, want the common objects %s", allow, want)
	}
}

func TestADirThatBorrowsObjectsByARelativeAlternateAllowsThemToo(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	lender := t.TempDir()
	commitRepo(t, lender)
	rel, err := filepath.Rel(filepath.Join(p.work, ".git", "objects"), filepath.Join(lender, ".git", "objects"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.work, ".git", "objects", "info", "alternates"), []byte(rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keptDraftFixClone(t, p, p.work, "kelpie")
	got, err := relaunchDraftFix(t, p, p.work, "kelpie")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(lender, ".git", "objects"))
	if allow := strs(t, sub(t, got, "sandbox", "filesystem")["allowRead"]); !slices.Contains(allow, want) {
		t.Errorf("allowRead = %q, want the borrowed objects %s", allow, want)
	}
}

func TestADraftFixShipsSandboxCannotWriteTheFilesGitReadsItsConfigurationFrom(t *testing.T) {
	p := newPinRig(t, nil)
	_, fc, argv, _ := p.mustRestricted(t, standing.DraftFix)
	_, got := settingsOf(t, argv)
	deny := strs(t, sub(t, got, "sandbox", "filesystem")["denyWrite"])
	for _, rel := range []string{"info", "modules", filepath.Join("objects", "info")} {
		if !slices.Contains(deny, filepath.Join(fc.madeFor, ".git", rel)) {
			t.Errorf("Draft fix denyWrite = %q, want .git/%s", deny, rel)
		}
	}
	permDeny := strs(t, sub(t, got, "permissions")["deny"])
	for _, tool := range []string{"Edit", "Write"} {
		if want := tool + "(/" + fc.madeFor + "/.git/**)"; !slices.Contains(permDeny, want) {
			t.Errorf("permissions.deny = %q, want %s", permDeny, want)
		}
	}
}

func TestARelaunchRemovesGitConfigurationAShipPlantedInItsKeptClone(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	clone := keptDraftFixClone(t, p, p.work, "kelpie")
	gitDir := filepath.Join(clone, ".git")
	attrs := filepath.Join(gitDir, "info", "attributes")
	if err := os.WriteFile(attrs, []byte("* filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "modules", "m"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(attrs); err == nil {
		t.Error("info/attributes survived the relaunch")
	}
	if _, err := os.Stat(filepath.Join(gitDir, "modules")); err == nil {
		t.Error(".git/modules survived the relaunch")
	}
	if b, _ := os.ReadFile(filepath.Join(gitDir, "info", "exclude")); !strings.Contains(string(b), ".admiral/") {
		t.Errorf("info/exclude = %q, want it kept with .admiral/", b)
	}
}

func TestACommitAndAnFsckStillWorkInAClonePastTheReusesAlternatesRewrite(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	clone := keptDraftFixClone(t, p, p.work, "kelpie")
	if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "add", "b.txt")
	git(t, clone, "commit", "-q", "-m", "second")
	git(t, clone, "fsck", "--connectivity-only")
	if got := git(t, clone, "log", "--format=%s", "-n", "2"); got != "second\nfirst" {
		t.Errorf("log = %q, want the clone to read the Dir's objects through the rewritten alternates", got)
	}
}

func TestARelaunchWhoseDirsObjectsCannotBeFoundRefusesAndKeepsTheClonesAlternates(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	clone := keptDraftFixClone(t, p, p.work, "kelpie")
	alt := filepath.Join(clone, ".git", "objects", "info", "alternates")
	before, err := os.ReadFile(alt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(p.work, ".git"), filepath.Join(p.work, ".git-away")); err != nil {
		t.Fatal(err)
	}
	if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err == nil {
		t.Error("a relaunch went ahead with no object directory for the Dir")
	}
	if after, _ := os.ReadFile(alt); string(after) != string(before) {
		t.Errorf("alternates = %q, want it left as %q", after, before)
	}
}

func TestARelaunchNeverFollowsALinkTheShipPlantedInsideItsKeptClonesGit(t *testing.T) {
	for _, rel := range []string{"info", filepath.Join("objects", "info"), "config"} {
		t.Run(rel, func(t *testing.T) {
			p := newPinRig(t, nil)
			commitRepo(t, p.work)
			clone := keptDraftFixClone(t, p, p.work, "kelpie")
			outside := t.TempDir()
			for _, name := range []string{"attributes", "alternates"} {
				if err := os.WriteFile(filepath.Join(outside, name), []byte("keep\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			target := outside
			if rel == "config" {
				target = filepath.Join(outside, "alternates")
			}
			link := filepath.Join(clone, ".git", rel)
			if err := os.RemoveAll(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err == nil {
				t.Error("a relaunch over a linked .git/" + rel + " went ahead")
			}
			for _, name := range []string{"attributes", "alternates"} {
				if b, _ := os.ReadFile(filepath.Join(outside, name)); string(b) != "keep\n" {
					t.Errorf("%s outside the clone = %q, want it untouched", name, b)
				}
			}
			if _, err := os.Stat(clone); err != nil {
				t.Errorf("the refused relaunch removed the kept clone: %v", err)
			}
		})
	}
}

// plantNestedRepo does what a ship can inside its sandbox: a gitlink in the index over a gitfile that points
// at a git directory of its own, whose config runs marker's command when any git looks at the nested repo.
func plantNestedRepo(t *testing.T, clone, marker string) {
	t.Helper()
	evil := filepath.Join(clone, "evil")
	git(t, clone, "init", "-q", "--bare", evil)
	git(t, clone, "--git-dir="+evil, "config", "core.bare", "false")
	git(t, clone, "--git-dir="+evil, "config", "core.fsmonitor", "touch "+marker)
	if err := os.MkdirAll(filepath.Join(clone, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "sub", ".git"), []byte("gitdir: ../evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "update-index", "--add", "--cacheinfo", "160000,"+git(t, clone, "rev-parse", "HEAD")+",sub")
}

func assertGitInTheCloneRunsNothingFromANestedRepo(t *testing.T, clone, marker string) {
	t.Helper()
	for _, args := range [][]string{{"status"}, {"diff"}, {"status", "--porcelain=2"}} {
		git(t, clone, args...)
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("git %v in the clone ran the nested repo's core.fsmonitor", args)
		}
	}
}

func TestAFreshCloneRunsNothingFromANestedRepoAShipPlantsWhenGitLooksAtIt(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	commitRepo(t, dir)
	clone, err := GitCheckouts{}.Make(home, "kelpie", dir)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	plantNestedRepo(t, clone, marker)
	assertGitInTheCloneRunsNothingFromANestedRepo(t, clone, marker)
}

func TestAReusedCloneMadeBeforeTheFixRunsNothingFromANestedRepoEither(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	clone := keptDraftFixClone(t, p, p.work, "kelpie")
	_ = exec.Command("git", "-C", clone, "config", "--unset", "diff.ignoreSubmodules").Run() // the clone predates the fix
	if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	plantNestedRepo(t, clone, marker)
	assertGitInTheCloneRunsNothingFromANestedRepo(t, clone, marker)
}

const submoduleOn = "[submodule \"sub\"]\n\tpath = sub\n\turl = x\n\tignore = none\n"

// plantSubmodule is what a ship can do to turn the planted gitlink into a configured submodule, which overrides
// diff.ignoreSubmodules: a .gitmodules in the working tree, only in the index, or only in a commit.
func plantSubmodule(t *testing.T, clone, where string) {
	t.Helper()
	switch where {
	case "tree":
		if err := os.WriteFile(filepath.Join(clone, ".gitmodules"), []byte(submoduleOn), 0o644); err != nil {
			t.Fatal(err)
		}
	case "index", "commit":
		blob := gitIn(t, clone, submoduleOn, "hash-object", "-w", "--stdin")
		git(t, clone, "update-index", "--add", "--cacheinfo", "100644,"+blob+",.gitmodules")
		if where == "commit" {
			git(t, clone, "commit", "-q", "-m", "plant")
			git(t, clone, "update-index", "--force-remove", ".gitmodules")
		}
	case "gitlink":
	default:
		t.Fatalf("unknown plant %q", where)
	}
}

func gitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// shipGitEnv is the git configuration a Draft fix ship's environment carries, as the harness recorded it.
func shipGitEnv(t *testing.T) []string {
	t.Helper()
	_, _, _, env := newPinRig(t, nil).mustRestricted(t, standing.DraftFix)
	var out []string
	for k, v := range env {
		if strings.HasPrefix(k, "GIT_CONFIG_") {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func assertNoMarkerFromGit(t *testing.T, clone, marker string, env []string) {
	t.Helper()
	for _, args := range [][]string{{"status"}, {"diff"}, {"status", "--porcelain=2"}} {
		cmd := exec.Command("git", append([]string{"-C", clone}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		_ = cmd.Run()
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("git %v in the clone ran the nested repo's core.fsmonitor", args)
		}
	}
}

func TestADraftFixShipsGitCannotBeMadeToRunANestedReposFsmonitor(t *testing.T) {
	env := shipGitEnv(t)
	if len(env) == 0 {
		t.Fatal("the ship's environment carries no git configuration")
	}
	for _, where := range []string{"tree", "index", "commit"} {
		t.Run(where, func(t *testing.T) {
			dir, home := t.TempDir(), t.TempDir()
			commitRepo(t, dir)
			clone, err := GitCheckouts{}.Make(home, "kelpie", dir)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "ran")
			plantNestedRepo(t, clone, marker)
			plantSubmodule(t, clone, where)
			assertNoMarkerFromGit(t, clone, marker, env)
		})
	}
}

func TestAFleetsOwnGitConfigurationInTheEnvironmentIsKeptBesideTheFsmonitorSetting(t *testing.T) {
	p := newPinRig(t, map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "user.name", "GIT_CONFIG_VALUE_0": "fleet"})
	_, _, _, env := p.mustRestricted(t, standing.DraftFix)
	if env["GIT_CONFIG_COUNT"] != "2" || env["GIT_CONFIG_KEY_0"] != "user.name" || env["GIT_CONFIG_VALUE_0"] != "fleet" ||
		env["GIT_CONFIG_KEY_1"] != "core.fsmonitor" || env["GIT_CONFIG_VALUE_1"] != "false" {
		t.Errorf("git config env = %v, want the fleet's entry kept and core.fsmonitor=false added", env)
	}
}

func TestADraftFixShipsSandboxAndToolsCannotWriteGitmodules(t *testing.T) {
	p := newPinRig(t, nil)
	_, fc, argv, _ := p.mustRestricted(t, standing.DraftFix)
	_, got := settingsOf(t, argv)
	gm := filepath.Join(fc.madeFor, ".gitmodules")
	if deny := strs(t, sub(t, got, "sandbox", "filesystem")["denyWrite"]); !slices.Contains(deny, gm) {
		t.Errorf("denyWrite = %q, want %s", deny, gm)
	}
	permDeny := strs(t, sub(t, got, "permissions")["deny"])
	for _, tool := range []string{"Edit", "Write"} {
		if want := tool + "(/" + gm + ")"; !slices.Contains(permDeny, want) {
			t.Errorf("permissions.deny = %q, want %s", permDeny, want)
		}
	}
}

func TestARelaunchRefusesAKeptCloneWhoseSubmoduleStateTheShipChanged(t *testing.T) {
	for _, where := range []string{"gitlink", "tree", "index", "commit"} {
		t.Run(where, func(t *testing.T) {
			p := newPinRig(t, nil)
			commitRepo(t, p.work)
			clone := keptDraftFixClone(t, p, p.work, "kelpie")
			marker := filepath.Join(t.TempDir(), "ran")
			plantNestedRepo(t, clone, marker)
			plantSubmodule(t, clone, where)
			if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err == nil {
				t.Error("a relaunch went ahead over a planted gitlink")
			}
			if _, err := os.Stat(marker); err == nil {
				t.Error("Admiral's own git ran the nested repo's core.fsmonitor")
			}
			if _, err := os.Stat(clone); err != nil {
				t.Errorf("the refused relaunch removed the kept clone: %v", err)
			}
		})
	}
}

func TestARelaunchAcceptsADirsOwnGitmodulesThatTheShipLeftAlone(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	if err := os.WriteFile(filepath.Join(p.work, ".gitmodules"), []byte(submoduleOn), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, p.work, "add", ".gitmodules")
	git(t, p.work, "commit", "-q", "-m", "modules")
	keptDraftFixClone(t, p, p.work, "kelpie")
	if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err != nil {
		t.Errorf("a relaunch over the Dir's own .gitmodules was refused: %v", err)
	}
}
