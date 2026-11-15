// A relaunch or a Promote over a kept restricted clone refuses a git carrier a ship planted at any depth, and runs no submodule filter.
package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func errText(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Msg
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

// plantCarrier puts what git treats as a repository or submodule marker at rel inside clone, untracked.
func plantCarrier(t *testing.T, clone, kind, rel string) {
	t.Helper()
	path := filepath.Join(clone, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	switch kind {
	case "git folder":
		git(t, clone, "init", "-q", "--bare", path)
	case "git file":
		if err := os.WriteFile(path, []byte("gitdir: ../evil\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	case "git link":
		if err := os.Symlink("/nonexistent-target", path); err != nil {
			t.Fatal(err)
		}
	case "gitmodules":
		if err := os.WriteFile(path, []byte(submoduleOn), 0o644); err != nil {
			t.Fatal(err)
		}
	case "gitlink":
		git(t, clone, "update-index", "--add", "--cacheinfo", "160000,"+git(t, clone, "rev-parse", "HEAD")+","+rel)
	default:
		t.Fatalf("unknown carrier %q", kind)
	}
}

func TestARelaunchRefusesAGitCarrierAnywhereBelowTheRootAndNamesItsPath(t *testing.T) {
	for _, c := range []struct{ kind, rel string }{
		{"git folder", "src/deep/.git"},
		{"git file", "src/deep/.git"},
		{"git link", "src/deep/.git"},
		{"git folder", "one/.git"},
		{"gitmodules", "src/deep/.gitmodules"},
		{"gitmodules", "one/.gitmodules"},
		{"gitlink", "src/deep/sub"},
		{"git file", ".admiral/untrusted/.git"},
	} {
		t.Run(c.kind+" at "+c.rel, func(t *testing.T) {
			p := newPinRig(t, nil)
			commitRepo(t, p.work)
			clone := keptDraftFixClone(t, p, p.work, "kelpie")
			plantCarrier(t, clone, c.kind, c.rel)
			_, err := relaunchDraftFix(t, p, p.work, "kelpie")
			if err == nil {
				t.Fatal("a relaunch went ahead over a planted " + c.kind)
			}
			if want := strconv.Quote(c.rel); !strings.Contains(errText(err), want) {
				t.Errorf("refusal = %q, want it to name %s", errText(err), want)
			}
			if _, err := os.Stat(clone); err != nil {
				t.Errorf("the refused relaunch removed the kept clone: %v", err)
			}
		})
	}
}

func TestARelaunchRunsNoCoreFsmonitorOfAnUntrackedNestedRepoItRefuses(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	clone := keptDraftFixClone(t, p, p.work, "kelpie")
	marker := filepath.Join(t.TempDir(), "ran")
	plantCarrier(t, clone, "git folder", "src/deep/.git")
	git(t, clone, "--git-dir="+filepath.Join(clone, "src/deep/.git"), "config", "core.fsmonitor", "touch "+marker)
	if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err == nil {
		t.Error("a relaunch went ahead over an untracked nested repo")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("Admiral's git ran the nested repo's core.fsmonitor")
	}
}

func TestARefusalNamesAPathQuotedSoItCannotForgeALineOrFloodTheMessage(t *testing.T) {
	long := filepath.Join(strings.Repeat("a", 90), strings.Repeat("b", 90), strings.Repeat("c", 90), ".git")
	for name, rel := range map[string]string{"quoted": "q\"uote\nforged: ok/.git", "capped": filepath.ToSlash(long)} {
		t.Run(name, func(t *testing.T) {
			p := newPinRig(t, nil)
			commitRepo(t, p.work)
			clone := keptDraftFixClone(t, p, p.work, "kelpie")
			plantCarrier(t, clone, "git file", rel)
			_, err := relaunchDraftFix(t, p, p.work, "kelpie")
			if err == nil {
				t.Fatal("a relaunch went ahead")
			}
			msg := errText(err)
			if strings.ContainsAny(msg, "\n\r") {
				t.Errorf("refusal %q holds a raw line break", msg)
			}
			if name == "quoted" {
				if want := strconv.Quote(rel); !strings.Contains(msg, want) {
					t.Errorf("refusal = %q, want it to hold %s", msg, want)
				}
				return
			}
			if strings.Contains(msg, rel) {
				t.Errorf("refusal %q holds the whole %d-byte path", msg, len(rel))
			}
			if !strings.Contains(msg, `"`+rel[:100]) {
				t.Errorf("refusal %q does not start the path with its first 100 bytes", msg)
			}
			if len(msg) > 600 {
				t.Errorf("refusal is %d bytes, want a fixed reason plus at most 200 bytes of path", len(msg))
			}
		})
	}
}

func TestARelaunchAcceptsAKeptCloneWhoseDeepTreeOnlyHasOrdinaryFilesThatLookLikeGit(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	clone := keptDraftFixClone(t, p, p.work, "kelpie")
	for _, f := range []string{"src/deep/a.go", ".github/ci.yml", "x.git/y.txt", ".gitignore", "src/.gitmodulesx", "src/deep/.gitkeep", ".admiral/untrusted/event.md"} {
		path := filepath.Join(clone, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err != nil {
		t.Errorf("a relaunch over ordinary files was refused: %v", err)
	}
}

// plantFilterSubmodule is the planted submodule of a ship: a nested repo at sub whose own config makes a clean filter touch marker
// on the modified file its own .gitattributes covers, behind a gitlink in the clone's index.
func plantFilterSubmodule(t *testing.T, clone, marker string) {
	t.Helper()
	sub := filepath.Join(clone, "sub")
	git(t, clone, "init", "-q", sub)
	for name, body := range map[string]string{"x.txt": "one\n", ".gitattributes": "* filter=evil\n"} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, sub, "add", ".")
	git(t, sub, "commit", "-q", "-m", "x")
	git(t, sub, "config", "filter.evil.clean", "touch "+marker+" && cat")
	if err := os.WriteFile(filepath.Join(sub, "x.txt"), []byte("two, modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "update-index", "--add", "--cacheinfo", "160000,"+git(t, sub, "rev-parse", "HEAD")+",sub")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the plant itself ran the filter")
	}
}

func TestAPlantedSubmodulesCleanFilterDoesRunUnderPlainGit(t *testing.T) {
	p := newPinRig(t, nil)
	commitRepo(t, p.work)
	clone := keptDraftFixClone(t, p, p.work, "kelpie")
	marker := filepath.Join(t.TempDir(), "ran")
	plantFilterSubmodule(t, clone, marker)
	plantSubmodule(t, clone, "tree")
	_ = exec.Command("git", "-C", clone, "-c", "diff.ignoreSubmodules=none", "status").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the plant does not run its filter under plain git, so the tests below prove nothing")
	}
}

func TestARelaunchOverAPlantedSubmoduleRunsNoFilterOfItsAndIsRefused(t *testing.T) {
	for _, where := range []string{"gitlink", "tree", "index", "commit"} {
		t.Run(where, func(t *testing.T) {
			p := newPinRig(t, nil)
			commitRepo(t, p.work)
			clone := keptDraftFixClone(t, p, p.work, "kelpie")
			marker := filepath.Join(t.TempDir(), "ran")
			plantFilterSubmodule(t, clone, marker)
			plantSubmodule(t, clone, where)
			if _, err := relaunchDraftFix(t, p, p.work, "kelpie"); err == nil {
				t.Error("a relaunch went ahead over a planted submodule")
			}
			if _, err := os.Stat(marker); err == nil {
				t.Error("Admiral's git ran the submodule's clean filter")
			}
		})
	}
}

func TestADraftFixShipsGitRunsNoFilterOfAPlantedSubmodule(t *testing.T) {
	env := shipGitEnv(t)
	for _, where := range []string{"tree", "index", "commit"} {
		t.Run(where, func(t *testing.T) {
			p := newPinRig(t, nil)
			commitRepo(t, p.work)
			clone := keptDraftFixClone(t, p, p.work, "kelpie")
			marker := filepath.Join(t.TempDir(), "ran")
			plantFilterSubmodule(t, clone, marker)
			plantSubmodule(t, clone, where)
			for _, args := range [][]string{{"status"}, {"diff"}, {"status", "--porcelain=2"}, {"add", "-A"}} {
				cmd := exec.Command("git", append([]string{"-C", clone}, args...)...)
				cmd.Env = append(os.Environ(), env...)
				_ = cmd.Run()
				if _, err := os.Stat(marker); err == nil {
					t.Fatalf("git %v with the ship's environment ran the submodule's clean filter", args)
				}
			}
		})
	}
}

func TestAStartFromRefusesACloneThatGainedACarrierAfterTheCallerReadItWithTheFixedMessageAndNoPath(t *testing.T) {
	p := newPinRig(t, nil)
	clone, branch := p.restrictedClone(t)
	startWrite(t, filepath.Join(clone, "c.txt"), "the fix\n")
	startGit(t, clone, "add", "c.txt")
	startGit(t, clone, "commit", "-q", "-m", "fix")
	req := p.promoted(clone, branch)
	if req.StartFrom.Tip == "" {
		t.Fatal("the caller's read failed before the carrier was planted")
	}
	plantCarrier(t, clone, "git folder", "src/deep/.git")
	_, err := p.l.Launch(req)
	if err == nil {
		t.Fatal("Launch succeeded over a clone with a planted nested repo")
	}
	if got, want := errText(err), "could not start the ship from the restricted work"; got != want {
		t.Errorf("refusal = %q, want the fixed %q", got, want)
	}
	if left, _ := os.ReadDir(filepath.Join(p.home, "checkouts")); len(left) != 1 || left[0].Name() != "restricted-one" {
		t.Errorf("checkouts = %v, want only the restricted clone", left)
	}
}

func TestARelaunchWhileTheShipsTmuxSessionIsListedIsRefusedBeforeAnyScan(t *testing.T) {
	for _, planted := range []bool{false, true} {
		name := "clean"
		if planted {
			name = "planted"
		}
		t.Run(name, func(t *testing.T) {
			p := newPinRig(t, nil)
			commitRepo(t, p.work)
			clone := keptDraftFixClone(t, p, p.work, "kelpie")
			if planted {
				plantCarrier(t, clone, "git folder", "src/deep/.git")
			}
			if out, err := exec.Command(p.l.Tmux, "-S", p.l.Socket, "new-session", "-d", "-s", "kelpie", "sleep 300").CombinedOutput(); err != nil {
				t.Fatalf("tmux new-session: %v: %s", err, out)
			}
			_, err := relaunchDraftFix(t, p, p.work, "kelpie")
			var e *Error
			if !errors.As(err, &e) || e.Status != 409 || !strings.Contains(e.Msg, "already live") {
				t.Errorf("err = %v, want 409 ship kelpie is already live", err)
			}
		})
	}
}
