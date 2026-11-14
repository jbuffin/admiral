// Reading a restricted ship's work, with real git in temp dirs.
package promote_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/promote"
)

const session = "calm-otter"

// git runs real git with the engineer's own config, hooks and signing kept out.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, file, content string) string {
	t.Helper()
	write(t, filepath.Join(dir, file), content)
	git(t, dir, "add", "-f", "--", file)
	git(t, dir, "commit", "-q", "-m", "change "+file)
	return git(t, dir, "rev-parse", "HEAD")
}

type world struct {
	dir, clone, branch string
	a, b               string
}

// newWorld is a Dir with commits A and B and a restricted ship's clone made at B by the launcher's own checkout code.
func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	w := &world{dir: dir, branch: "admiral/" + session}
	w.a = commit(t, dir, "a.txt", "a\n")
	w.b = commit(t, dir, "b.txt", "b\n")
	clone, err := launcher.GitCheckouts{}.Make(t.TempDir(), session, dir)
	if err != nil {
		t.Fatal(err)
	}
	w.clone = clone
	return w
}

func (w *world) read(t *testing.T) (promote.Work, error) {
	t.Helper()
	return promote.Read(context.Background(), w.dir, w.clone, w.branch)
}

func (w *world) mustRead(t *testing.T) promote.Work {
	t.Helper()
	got, err := w.read(t)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got
}

func TestTheBaseComesFromDirAndNotFromTheClonesRefs(t *testing.T) {
	w := newWorld(t)
	c := commit(t, w.clone, "c.txt", "c\n")
	// A ship can write its clone's refs: point the remote-tracking branch Make left at A, before B.
	git(t, w.clone, "update-ref", "refs/remotes/origin/main", w.a)
	got := w.mustRead(t)
	if got.Kind != promote.Commits || got.Base != w.b || got.Tip != c {
		t.Fatalf("Read = %+v, want commits with base %s (B, Dir's HEAD) and tip %s", got, w.b, c)
	}
	if !strings.Contains(got.Range, w.b) || !strings.Contains(got.Range, c) || !strings.Contains(got.Range, "..") {
		t.Errorf("Range = %q, want base..tip", got.Range)
	}
}

func TestTheBaseFollowsDirsMergeBaseWhenDirMovedAheadAfterTheClone(t *testing.T) {
	w := newWorld(t)
	c := commit(t, w.clone, "c.txt", "c\n")
	commit(t, w.dir, "d.txt", "d\n")
	got := w.mustRead(t)
	if got.Kind != promote.Commits || got.Base != w.b || got.Tip != c {
		t.Fatalf("Read = %+v, want base %s and tip %s", got, w.b, c)
	}
}

func TestUncommittedChangesAreADiffWithUntrackedAndBinaryFilesAndNoAdmiralPaths(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.clone, "a.txt"), "a changed\n")
	write(t, filepath.Join(w.clone, "new.txt"), "brand new\n")
	write(t, filepath.Join(w.clone, "bin.dat"), "\x00\x01\x02binary")
	write(t, filepath.Join(w.clone, ".admiral", "report.md"), "canary-report-text\n")
	write(t, filepath.Join(w.clone, ".admiral", "untrusted", "event.md"), "canary-event-text\n")
	// The ship can drop .admiral/ from its own exclude, so the read must leave it out by pathspec.
	write(t, filepath.Join(w.clone, ".git", "info", "exclude"), "")
	got := w.mustRead(t)
	if got.Kind != promote.Diff || got.Tip != "" || got.Range != "uncommitted changes" {
		t.Fatalf("Read = %+v, want a diff described as uncommitted changes", got)
	}
	for _, want := range []string{"a.txt", "new.txt", "brand new", "bin.dat", "GIT binary patch"} {
		if !strings.Contains(got.Patch, want) {
			t.Errorf("patch lacks %q:\n%s", want, got.Patch)
		}
	}
	for _, banned := range []string{".admiral", "canary-report-text", "canary-event-text"} {
		if strings.Contains(got.Patch, banned) {
			t.Errorf("patch carries %q:\n%s", banned, got.Patch)
		}
	}
}

func TestACloneWithNothingNewIsRefused(t *testing.T) {
	w := newWorld(t)
	if _, err := w.read(t); !errors.Is(err, promote.ErrNothing) {
		t.Errorf("a clean clone: err = %v, want ErrNothing", err)
	}
	write(t, filepath.Join(w.clone, ".admiral", "report.md"), "only the ship's own files\n")
	if _, err := w.read(t); !errors.Is(err, promote.ErrNothing) {
		t.Errorf("only .admiral/ changed: err = %v, want ErrNothing", err)
	}
}

func TestACommitThatTouchesAdmiralIsRefusedEvenWhenALaterOneUndoesIt(t *testing.T) {
	w := newWorld(t)
	commit(t, w.clone, "c.txt", "c\n")
	commit(t, w.clone, ".admiral/untrusted/event.md", "smuggled\n")
	git(t, w.clone, "rm", "-q", "-r", "--", ".admiral")
	git(t, w.clone, "commit", "-q", "-m", "tidy")
	if _, err := w.read(t); !errors.Is(err, promote.ErrAdmiralPaths) {
		t.Errorf("err = %v, want ErrAdmiralPaths: the net diff is clean but a commit in the range touched .admiral/", err)
	}
}

func TestChangedAlternatesAreRefusedAndTheUntouchedFileIsNot(t *testing.T) {
	w := newWorld(t)
	commit(t, w.clone, "c.txt", "c\n")
	w.mustRead(t)
	alt := filepath.Join(w.clone, ".git", "objects", "info", "alternates")
	f, err := os.OpenFile(alt, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(t.TempDir() + "/objects\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := w.read(t); !errors.Is(err, promote.ErrAlternates) {
		t.Errorf("err = %v, want ErrAlternates for an alternates file with an extra line", err)
	}
}

// plant puts a marker-writing script behind every way git runs code on its own.
func plant(t *testing.T, repo, markers string) {
	t.Helper()
	script := filepath.Join(markers, "hit.sh")
	write(t, script, "#!/bin/sh\necho hit >> "+filepath.Join(markers, "hits")+"\ncat\n")
	hooks := filepath.Join(markers, "hooks")
	for _, h := range []string{"post-checkout", "post-merge", "post-commit", "reference-transaction", "pre-auto-gc", "fsmonitor-watchman"} {
		write(t, filepath.Join(hooks, h), "#!/bin/sh\necho "+h+" >> "+filepath.Join(markers, "hits")+"\n")
		write(t, filepath.Join(repo, ".git", "hooks", h), "#!/bin/sh\necho "+h+" >> "+filepath.Join(markers, "hits")+"\n")
	}
	git(t, repo, "config", "--local", "core.hooksPath", hooks)
	git(t, repo, "config", "--local", "core.fsmonitor", script)
	git(t, repo, "config", "--local", "core.pager", script)
	git(t, repo, "config", "--local", "diff.external", script)
	git(t, repo, "config", "--local", "diff.evil.textconv", script)
	git(t, repo, "config", "--local", "filter.evil.clean", script)
	git(t, repo, "config", "--local", "filter.evil.smudge", script)
	write(t, filepath.Join(repo, ".git", "info", "attributes"), "* filter=evil diff=evil\n")
}

func hits(t *testing.T, markers string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(markers, "hits"))
	return string(b)
}

func TestReadingRunsNoCodeTheClonesOrDirsConfigHooksAttributesOrFilterNameWhenItIsACommitRange(t *testing.T) {
	w := newWorld(t)
	markers := t.TempDir()
	commit(t, w.clone, "c.txt", "c\n")
	plant(t, w.clone, markers)
	plant(t, w.dir, markers)
	got := w.mustRead(t)
	if got.Kind != promote.Commits {
		t.Fatalf("Read = %+v, want commits", got)
	}
	if h := hits(t, markers); h != "" {
		t.Errorf("planted code ran:\n%s", h)
	}
}

func TestReadingRunsNoCodeTheClonesOrDirsConfigHooksAttributesOrFilterNameWhenItIsADiff(t *testing.T) {
	w := newWorld(t)
	markers := t.TempDir()
	write(t, filepath.Join(w.clone, "a.txt"), "a changed\n")
	write(t, filepath.Join(w.clone, "new.txt"), "brand new\n")
	plant(t, w.clone, markers)
	plant(t, w.dir, markers)
	got := w.mustRead(t)
	if got.Kind != promote.Diff || !strings.Contains(got.Patch, "brand new") {
		t.Fatalf("Read = %+v, want a diff carrying the untracked file", got)
	}
	if h := hits(t, markers); h != "" {
		t.Errorf("planted code ran:\n%s", h)
	}
}

func TestThePlantedFsmonitorDoesRunUnderPlainGit(t *testing.T) {
	w := newWorld(t)
	markers := t.TempDir()
	plant(t, w.clone, markers)
	git(t, w.clone, "-c", "core.hooksPath=", "status")
	if hits(t, markers) == "" {
		t.Skip("this git does not run the planted fsmonitor, so the checks above cannot tell")
	}
}
