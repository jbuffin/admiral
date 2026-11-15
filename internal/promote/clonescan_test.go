// Reading a restricted clone refuses a git carrier a ship planted at any depth, naming its path, and runs no submodule filter.
package promote_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/promote"
)

func isOneOfTheOtherRefusals(err error) bool {
	for _, s := range []error{promote.ErrNothing, promote.ErrAdmiralPaths, promote.ErrAlternates, promote.ErrHarness, promote.ErrSymlink} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

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
		write(t, path, "gitdir: ../evil\n")
	case "git link":
		if err := os.Symlink("/nonexistent-target", path); err != nil {
			t.Fatal(err)
		}
	case "gitmodules":
		write(t, path, "[submodule \"sub\"]\n\tpath = sub\n\turl = x\n\tignore = none\n")
	case "gitlink":
		git(t, clone, "update-index", "--add", "--cacheinfo", "160000,"+git(t, clone, "rev-parse", "HEAD")+","+rel)
	default:
		t.Fatalf("unknown carrier %q", kind)
	}
}

func TestReadRefusesAGitCarrierAnywhereBelowTheRootAndNamesItsQuotedPath(t *testing.T) {
	for _, c := range []struct{ kind, rel string }{
		{"git folder", "src/deep/.git"},
		{"git file", "src/deep/.git"},
		{"git link", "src/deep/.git"},
		{"git file", "one/.git"},
		{"gitmodules", "src/deep/.gitmodules"},
		{"gitlink", "src/deep/sub"},
		{"git file", ".admiral/untrusted/.git"},
	} {
		for _, work := range []string{"commits", "diff"} {
			t.Run(c.kind+" at "+c.rel+" as "+work, func(t *testing.T) {
				w := newWorld(t)
				if work == "commits" {
					commit(t, w.clone, "c.txt", "c\n")
				} else {
					write(t, filepath.Join(w.clone, "c.txt"), "c\n")
				}
				plantCarrier(t, w.clone, c.kind, c.rel)
				_, err := w.read(t)
				if err == nil {
					t.Fatal("Read accepted a clone with a planted " + c.kind)
				}
				if isOneOfTheOtherRefusals(err) {
					t.Errorf("err = %v, want a refusal of its own, not one of the other refusals", err)
				}
				if want := strconv.Quote(c.rel); !strings.Contains(err.Error(), want) {
					t.Errorf("err = %q, want it to name %s", err, want)
				}
			})
		}
	}
}

func TestReadQuotesAndCapsThePathItNames(t *testing.T) {
	long := strings.Join([]string{strings.Repeat("a", 90), strings.Repeat("b", 90), strings.Repeat("c", 90), ".git"}, "/")
	for name, rel := range map[string]string{"quoted": "q\"uote\nforged: ok/.git", "capped": long} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			commit(t, w.clone, "c.txt", "c\n")
			plantCarrier(t, w.clone, "git file", rel)
			_, err := w.read(t)
			if err == nil {
				t.Fatal("Read accepted the clone")
			}
			msg := err.Error()
			if strings.ContainsAny(msg, "\n\r") {
				t.Errorf("err %q holds a raw line break", msg)
			}
			if name == "quoted" {
				if want := strconv.Quote(rel); !strings.Contains(msg, want) {
					t.Errorf("err = %q, want %s", msg, want)
				}
				return
			}
			if strings.Contains(msg, rel) || !strings.Contains(msg, `"`+rel[:100]) || len(msg) > 600 {
				t.Errorf("err (%d bytes) = %q, want the path cut to about 200 bytes", len(msg), msg)
			}
		})
	}
}

func TestReadStillAcceptsDeepOrdinaryFilesThatLookLikeGit(t *testing.T) {
	w := newWorld(t)
	for _, f := range []string{"src/deep/a.go", ".github/ci.yml", "x.git/y.txt", ".gitignore", "src/.gitmodulesx", "src/deep/.gitkeep"} {
		commit(t, w.clone, f, "x\n")
	}
	if got := w.mustRead(t); got.Kind != promote.Commits {
		t.Errorf("Read = %+v, want commits", got)
	}
}

func TestReadRunsNoCleanFilterOfAPlantedSubmoduleAndRefusesIt(t *testing.T) {
	w := newWorld(t)
	commit(t, w.clone, "c.txt", "c\n")
	marker := filepath.Join(t.TempDir(), "ran")
	sub := filepath.Join(w.clone, "sub")
	git(t, w.clone, "init", "-q", sub)
	write(t, filepath.Join(sub, "x.txt"), "one\n")
	write(t, filepath.Join(sub, ".gitattributes"), "* filter=evil\n")
	git(t, sub, "add", ".")
	git(t, sub, "commit", "-q", "-m", "x")
	git(t, sub, "config", "filter.evil.clean", "touch "+marker+" && cat")
	write(t, filepath.Join(sub, "x.txt"), "two, modified\n")
	git(t, w.clone, "update-index", "--add", "--cacheinfo", "160000,"+git(t, sub, "rev-parse", "HEAD")+",sub")
	write(t, filepath.Join(w.clone, ".gitmodules"), "[submodule \"sub\"]\n\tpath = sub\n\turl = x\n\tignore = none\n")
	if _, err := w.read(t); err == nil {
		t.Error("Read accepted a clone with a planted submodule")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("Read ran the submodule's clean filter")
	}
}

func TestReadRefusesANestedRepoBehindARootGitThatIsNotAFolder(t *testing.T) {
	for _, root := range []string{"link", "file"} {
		t.Run(root, func(t *testing.T) {
			w := newWorld(t)
			commit(t, w.clone, "c.txt", "c\n")
			real := filepath.Join(t.TempDir(), "real.git")
			if err := os.Rename(filepath.Join(w.clone, ".git"), real); err != nil {
				t.Fatal(err)
			}
			if root == "link" {
				if err := os.Symlink(real, filepath.Join(w.clone, ".git")); err != nil {
					t.Fatal(err)
				}
			} else {
				write(t, filepath.Join(w.clone, ".git"), "gitdir: "+real+"\n")
			}
			plantCarrier(t, w.clone, "git folder", "src/sub/.git")
			_, err := w.read(t)
			if err == nil {
				t.Fatal("Read accepted a clone whose root .git is not a folder")
			}
			if !errors.Is(err, promote.ErrGitCarrier) || !strings.Contains(err.Error(), `".git"`) && !strings.Contains(err.Error(), `src/sub/.git`) {
				t.Errorf("err = %v, want a git carrier refusal naming a path", err)
			}
		})
	}
}
