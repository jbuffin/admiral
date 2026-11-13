package launcher

import (
	"crypto/sha256"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitRepo(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-q", "-m", "first")
}

// snapshot hashes every file under dir, by path.
func snapshot(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	got := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			got[path+"/"] = [32]byte{}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got[path] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTheRealCheckoutIsASharedCloneOnItsOwnBranchThatNeverTouchesTheDirsGit(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	commitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, filepath.Join(dir, ".git"))

	path, err := GitCheckouts{}.Make(home, "kelp-1", dir)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	if after := snapshot(t, filepath.Join(dir, ".git")); len(after) != len(before) || func() bool {
		for k, v := range before {
			if after[k] != v {
				return true
			}
		}
		return false
	}() {
		t.Errorf("Make changed %s/.git: refs, worktrees or exclude lines were written there", dir)
	}
	realHome, _ := filepath.EvalSymlinks(home)
	if !strings.HasPrefix(path, realHome+string(os.PathSeparator)) {
		t.Errorf("checkout = %s, want a path under the resolved home %s", path, realHome)
	}
	if st, err := os.Stat(filepath.Join(path, ".git")); err != nil || !st.IsDir() {
		t.Errorf("%s/.git: %v, want a directory inside the checkout, not a link to Dir's", path, err)
	}
	if got := git(t, path, "branch", "--show-current"); got != "admiral/kelp-1" {
		t.Errorf("checkout branch = %q, want admiral/kelp-1", got)
	}
	if got, want := git(t, path, "rev-parse", "HEAD"), git(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("checkout HEAD = %s, want Dir's committed HEAD %s", got, want)
	}
	if b, err := os.ReadFile(filepath.Join(path, "a.txt")); err != nil || string(b) != "committed\n" {
		t.Errorf("a.txt in the checkout = %q (%v), want the committed file", b, err)
	}
	if _, err := os.Stat(filepath.Join(path, "wip.txt")); err == nil {
		t.Error("the checkout carries Dir's uncommitted file")
	}
	if out, err := exec.Command("git", "-C", path, "check-ignore", "-q", ".admiral/x").CombinedOutput(); err != nil {
		t.Errorf(".admiral/x is not ignored in the checkout: %v %s", err, out)
	}
	if err := exec.Command("git", "-C", dir, "check-ignore", "-q", ".admiral/x").Run(); err == nil {
		t.Errorf(".admiral/x is ignored in Dir, which the checkout must not change")
	}
}

func TestARealCheckoutOfADirThatIsNotARepoFailsAndLeavesNoHalfClone(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	if _, err := (GitCheckouts{}).Make(home, "kelp-1", dir); err == nil {
		t.Fatal("Make of a directory that is not a git repo succeeded")
	}
	if _, err := os.Stat(filepath.Join(home, "checkouts", "kelp-1")); err == nil {
		t.Error("a failed Make left a checkout directory behind")
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "checkouts")); len(entries) != 0 {
		t.Errorf("a failed Make left %d entries under checkouts/", len(entries))
	}
}

func TestARealCheckoutRefusesAPathThatExistsAndKeepsWhatIsThere(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	commitRepo(t, dir)
	first, err := GitCheckouts{}.Make(home, "kelp-1", dir)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first, "keep.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (GitCheckouts{}).Make(home, "kelp-1", dir); err == nil {
		t.Fatal("a second Make for the same session succeeded")
	}
	if b, err := os.ReadFile(filepath.Join(first, "keep.txt")); err != nil || string(b) != "mine" {
		t.Errorf("the refused Make damaged the existing checkout: %q %v", b, err)
	}
}

func TestARestrictedLaunchWithTheRealCheckoutRunsTheShipOnItsOwnBranch(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	p.l.Checkouts = GitCheckouts{}
	p.guided(guidanceText, nil)
	out, err := p.l.Launch(Request{Fleet: "work", Harness: "claude", Dir: p.work, Skill: Skill{Kind: "none"},
		Restricted: standing.DraftFix, EventText: rawEvent, Template: "Triage."})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	dir := out.Options["dir"]
	if dir == p.work {
		t.Fatalf("the ship's dir is the request's Dir, want its own checkout")
	}
	if got := git(t, dir, "branch", "--show-current"); got != "admiral/"+out.Session {
		t.Errorf("branch = %q, want admiral/%s", got, out.Session)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".admiral", "untrusted", "event.md")); err != nil || string(b) != rawEvent {
		t.Errorf("event.md = %q (%v), want the event", b, err)
	}
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); err != nil || strings.Contains(string(out), ".admiral") {
		t.Errorf("git status in the checkout shows .admiral: %q %v", out, err)
	}
}

func TestAnInvestigateShipMayWriteOnlyItsReportFileAndItsSandboxDeniesTheCheckout(t *testing.T) {
	p := newPinRig(t, nil)
	_, fc, argv, _ := p.mustRestricted(t, standing.Investigate)
	_, got := settingsOf(t, argv)
	perms := sub(t, got, "permissions")
	// The Edit and Write rules take // for an absolute path; sandbox paths below are plain absolute.
	report := "/" + filepath.Join(fc.madeFor, ".admiral", "report.md")
	var tools []string
	for _, r := range strs(t, perms["allow"]) {
		if r != "Write("+report+")" && r != "Edit("+report+")" {
			t.Errorf("permissions.allow holds %q, want only Write/Edit of %s", r, report)
		}
		tools = append(tools, r)
	}
	if !slices.Contains(tools, "Write("+report+")") {
		t.Errorf("permissions.allow = %q, want Write(%s)", tools, report)
	}
	for _, r := range strs(t, perms["deny"]) {
		if r == "Edit" || r == "Write" || strings.HasPrefix(r, "Edit(") || strings.HasPrefix(r, "Write(") {
			t.Errorf("permissions.deny holds %q, which would beat the report allow", r)
		}
	}
	deny := strs(t, sub(t, got, "sandbox", "filesystem")["denyWrite"])
	if !slices.Contains(deny, fc.madeFor) {
		t.Errorf("sandbox.filesystem.denyWrite = %q, want the checkout %s", deny, fc.madeFor)
	}
}

func TestADraftFixShipsSandboxDoesNotDenyItsCheckout(t *testing.T) {
	p := newPinRig(t, nil)
	_, fc, argv, _ := p.mustRestricted(t, standing.DraftFix)
	_, got := settingsOf(t, argv)
	if fs, ok := sub(t, got, "sandbox")["filesystem"].(map[string]any); ok {
		for _, d := range strs(t, fs["denyWrite"]) {
			// Only git's commondir and config.worktree are denied inside it.
			gitFile := d == filepath.Join(fc.madeFor, ".git", "commondir") || d == filepath.Join(fc.madeFor, ".git", "config.worktree")
			if !gitFile && strings.HasPrefix(fc.madeFor, d) {
				t.Errorf("sandbox.filesystem.denyWrite holds %q, which covers the Draft fix checkout %s", d, fc.madeFor)
			}
		}
	}
	for _, r := range strs(t, sub(t, got, "permissions")["allow"]) {
		if strings.Contains(r, "report.md") {
			t.Errorf("permissions.allow holds %q: the report file is Investigate's", r)
		}
	}
}

var unsafeFlags = []string{"--dangerously-skip-permissions", "--permission-mode", "--settings", "--setting-sources", "--add-dir",
	"--mcp-config", "--strict-mcp-config", "--allowedTools", "--allowed-tools", "--disallowedTools", "--disallowed-tools", "--permission-prompt-tool",
	"--plugin-dir", "--agents", "--allow-dangerously-skip-permissions"}

func (p *pinRig) setDockArgs(args []string) {
	p.l.Config().Fleets[0].Harnesses[0].Args = args
}

func TestARestrictedLaunchRefusesUnsafeDockAndExtraArgsNamingTheFlag(t *testing.T) {
	for _, level := range []standing.Restricted{standing.Investigate, standing.DraftFix} {
		for _, flag := range unsafeFlags {
			for form, args := range map[string][]string{"separate": {flag, "x"}, "equals": {flag + "=x"}} {
				for _, where := range []string{"dock", "extra"} {
					t.Run(string(level)+"/"+where+"/"+form+"/"+flag, func(t *testing.T) {
						p := newPinRig(t, nil)
						req, fc := p.restricted(level)
						if where == "dock" {
							p.setDockArgs(args)
						} else {
							req.ExtraArgs = args
						}
						_, err := p.l.Launch(req)
						var re *Error
						if !errors.As(err, &re) || re.Status != http.StatusBadRequest || !strings.Contains(re.Msg, flag) {
							t.Fatalf("Launch = %v, want a 400 naming %s", err, flag)
						}
						if len(fc.calls) != 0 {
							t.Errorf("the checkout seam was asked %d times for a refused launch", len(fc.calls))
						}
						if out, err := exec.Command(p.l.Tmux, "-S", p.l.Socket, "list-sessions").CombinedOutput(); err == nil {
							t.Errorf("a refused launch left sessions: %s", out)
						}
					})
				}
			}
		}
	}
}

func TestANormalLaunchStartsWithTheSameArgsARestrictedOneRefuses(t *testing.T) {
	for _, where := range []string{"dock", "extra"} {
		t.Run(where, func(t *testing.T) {
			p := newPinRig(t, nil)
			var args []string
			for _, f := range unsafeFlags {
				args = append(args, f+"=x")
			}
			req := p.justRequest()
			if where == "dock" {
				p.setDockArgs(args)
			} else {
				req.ExtraArgs = args
			}
			if _, err := p.l.Launch(req); err != nil {
				t.Fatalf("a normal launch with %v: %v", args, err)
			}
		})
	}
}
