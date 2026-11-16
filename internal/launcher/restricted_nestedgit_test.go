package launcher

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

// Tests that a restricted profile denies creating a .git below every root a ship can write.

const (
	nestCheckout = "/home/x/admiral/clones/saltpaw/work"
	nestTemp     = "/private/var/folders/ab/T/claude-saltpaw"
)

func nestedProfile(t *testing.T, level standing.Restricted) (denyWrite, permAllow, permDeny []string) {
	t.Helper()
	got := restrictedSettings(level, nestCheckout, "/home/x/admiral", nestTemp, nil)
	perms := sub(t, got, "permissions")
	return strs(t, sub(t, got, "sandbox", "filesystem")["denyWrite"]), strs(t, perms["allow"]), strs(t, perms["deny"])
}

func wantAll(t *testing.T, where string, have []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(have, w) {
			t.Errorf("%s = %q, want %s", where, have, w)
		}
	}
}

func TestADraftFixShipCannotCreateAGitBelowItsCheckoutRootOrItsTempDir(t *testing.T) {
	denyWrite, _, permDeny := nestedProfile(t, standing.DraftFix)
	wantAll(t, "sandbox.filesystem.denyWrite", denyWrite, nestCheckout+"/*/**/.git", nestTemp+"/**/.git")
	wantAll(t, "permissions.deny", permDeny,
		"Edit(/"+nestCheckout+"/*/**/.git)", "Edit(/"+nestCheckout+"/*/**/.git/**)",
		"Edit(/"+nestTemp+"/**/.git)", "Edit(/"+nestTemp+"/**/.git/**)")
}

func TestADraftFixShipsOwnGitStaysWritableAsAWhole(t *testing.T) {
	denyWrite, _, permDeny := nestedProfile(t, standing.DraftFix)
	root := nestCheckout + "/.git"
	for _, bad := range []string{root, nestCheckout + "/**/.git", nestCheckout + "/**"} {
		if slices.Contains(denyWrite, bad) {
			t.Errorf("denyWrite holds %s, which stops git commit writing the index and refs", bad)
		}
	}
	for _, bad := range []string{"Edit(/" + root + ")", "Edit(/" + root + "/**)", "Edit(/" + nestCheckout + "/**/.git)", "Edit(/" + nestCheckout + "/**/.git/**)"} {
		if slices.Contains(permDeny, bad) {
			t.Errorf("permissions.deny holds %s, which Claude Code turns into a sandbox deny on the root's own .git", bad)
		}
	}
}

func TestAnInvestigateShipCannotCreateAGitBelowItsTempDir(t *testing.T) {
	denyWrite, _, permDeny := nestedProfile(t, standing.Investigate)
	wantAll(t, "sandbox.filesystem.denyWrite", denyWrite, nestTemp+"/**/.git")
	wantAll(t, "permissions.deny", permDeny, "Edit(/"+nestTemp+"/**/.git)", "Edit(/"+nestTemp+"/**/.git/**)")
}

func TestARestrictedProfileHoldsNoWritePermissionRules(t *testing.T) {
	for _, level := range []standing.Restricted{standing.DraftFix, standing.Investigate} {
		_, allow, deny := nestedProfile(t, level)
		for _, r := range append(slices.Clone(allow), deny...) {
			if r == "Write" || strings.HasPrefix(r, "Write(") {
				t.Errorf("%v profile holds %q: Claude Code ignores Write rules and only Edit rules apply", level, r)
			}
		}
	}
}

func TestTheProfileDeniesTheResolvedTempDirNotTheShortLink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "adm-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	got := RestrictedProfile(standing.DraftFix, nestCheckout, t.TempDir(), t.TempDir(), link)
	denyWrite := strs(t, sub(t, got, "sandbox", "filesystem")["denyWrite"])
	permDeny := strs(t, sub(t, got, "permissions")["deny"])
	wantAll(t, "denyWrite", denyWrite, real+"/**/.git")
	wantAll(t, "permissions.deny", permDeny, "Edit(/"+real+"/**/.git)", "Edit(/"+real+"/**/.git/**)")
	for _, bad := range []string{link + "/**/.git", "Edit(/" + link + "/**/.git)", "Edit(/" + link + "/**/.git/**)"} {
		if slices.Contains(denyWrite, bad) || slices.Contains(permDeny, bad) {
			t.Errorf("the profile denies %s, the short link, which the sandbox does not match", bad)
		}
	}
}

func TestEveryProfileDeniesAGitBelowClaudeCodesDefaultWriteRoots(t *testing.T) {
	for _, level := range []standing.Restricted{standing.DraftFix, standing.Investigate} {
		denyWrite, _, permDeny := nestedProfile(t, level)
		for _, root := range []string{"/tmp/claude", "/private/tmp/claude", "~/.npm/_logs"} {
			rule := "/" + root
			if strings.HasPrefix(root, "~") {
				rule = root
			}
			wantAll(t, "denyWrite", denyWrite, root+"/**/.git")
			wantAll(t, "permissions.deny", permDeny, "Edit("+rule+"/**/.git)", "Edit("+rule+"/**/.git/**)")
			for _, bad := range []string{root, "Edit(" + rule + "/**)", "Edit(" + rule + ")"} {
				if slices.Contains(denyWrite, bad) || slices.Contains(permDeny, bad) {
					t.Errorf("%v profile denies %s whole, but Claude Code writes there", level, bad)
				}
			}
		}
	}
}
