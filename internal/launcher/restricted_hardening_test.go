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
