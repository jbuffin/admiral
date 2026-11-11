package shipdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, s string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	d := t.TempDir()
	run(t, d, "init", "-q", "-b", "main")
	write(t, d, "base.txt", "one\ntwo\n")
	write(t, d, "edit.txt", "keep\nold\n")
	write(t, d, "gone.txt", "bye\n")
	write(t, d, "mv.txt", "a\nb\nc\nd\ne\nf\n")
	run(t, d, "add", ".")
	run(t, d, "commit", "-q", "-m", "base")
	run(t, d, "checkout", "-q", "-b", "feature")
	write(t, d, "committed.txt", "c1\nc2\n")
	run(t, d, "add", ".")
	run(t, d, "commit", "-q", "-m", "committed")
	write(t, d, "edit.txt", "keep\nnew\nmore\n")
	write(t, d, "untracked.txt", "u1\n")
	return d
}

func byPath(r Result) map[string]File {
	m := map[string]File{}
	for _, f := range r.Files {
		m[f.Path] = f
	}
	return m
}

func TestComputeCoversCommittedUncommittedAndUntracked(t *testing.T) {
	d := repo(t)
	if err := os.Remove(filepath.Join(d, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	run(t, d, "mv", "mv.txt", "moved.txt")
	r, err := Compute(context.Background(), d)
	if err != nil || r.State != StateOK {
		t.Fatalf("Compute = %+v, %v", r, err)
	}
	f := byPath(r)
	want := map[string]File{
		"committed.txt": {Status: "added", Added: 2},
		"edit.txt":      {Status: "modified", Added: 2, Removed: 1},
		"untracked.txt": {Status: "untracked", Added: 1},
		"gone.txt":      {Status: "deleted", Removed: 1},
		"moved.txt":     {Status: "renamed"},
	}
	for p, w := range want {
		got := f[p]
		if got.Status != w.Status || got.Added != w.Added || got.Removed != w.Removed {
			t.Errorf("%s = %+v, want %+v", p, got, w)
		}
	}
	for _, s := range []string{"+c1", "-old", "+new", "+u1", "-bye"} {
		if !strings.Contains(r.Diff, s) {
			t.Errorf("diff lacks %q", s)
		}
	}
	if strings.Contains(r.Diff, "base.txt") {
		t.Errorf("diff mentions an unchanged file")
	}
}

func TestComputeNotACheckoutIsNone(t *testing.T) {
	for _, d := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		r, err := Compute(context.Background(), d)
		if err != nil || r.State != StateNone || len(r.Files) != 0 {
			t.Errorf("Compute(%s) = %+v, %v, want state none", d, r, err)
		}
	}
}

func TestComputeCutsAnOversizedDiff(t *testing.T) {
	d := repo(t)
	write(t, d, "huge.txt", strings.Repeat("0123456789abcdef0123456789abcdef0123456789abcdef\n", 60000))
	r, err := Compute(context.Background(), d)
	if err != nil || !r.Truncated || len(r.Diff) > MaxDiff || len(r.Diff) == 0 {
		t.Fatalf("truncated=%v diff=%d err=%v", r.Truncated, len(r.Diff), err)
	}
	if byPath(r)["huge.txt"].Added != 60000 {
		t.Errorf("huge.txt = %+v, want 60000 added lines listed", byPath(r)["huge.txt"])
	}
}

func TestComputeRunsNoProgramTheCheckoutNames(t *testing.T) {
	d := repo(t)
	marker := filepath.Join(t.TempDir(), "MARKER")
	script := filepath.Join(t.TempDir(), "trap.sh")
	write(t, filepath.Dir(script), "trap.sh", "#!/bin/sh\necho ran >> "+marker+"\ncat \"$@\"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"diff.external": script, "diff.trap.textconv": script, "diff.trap.command": script,
		"core.fsmonitor": script, "core.pager": script,
		"filter.trap.clean": script, "filter.trap.smudge": script, "filter.trap.process": script,
	} {
		run(t, d, "config", k, v)
	}
	write(t, d, ".gitattributes", "*.txt diff=trap filter=trap\n")
	r, err := Compute(context.Background(), d)
	if err != nil || r.State != StateOK || !strings.Contains(r.Diff, "+new") || !strings.Contains(r.Diff, "+u1") {
		t.Fatalf("Compute = %q, %v, want the diff despite the config", r.Diff, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("a program named by the checkout ran")
	}
}

func TestBaseIsOriginHead(t *testing.T) {
	d := repo(t)
	run(t, d, "branch", "develop", "main")
	run(t, d, "update-ref", "refs/remotes/origin/develop", "develop")
	run(t, d, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	r, err := Compute(context.Background(), d)
	if err != nil || byPath(r)["committed.txt"].Status != "added" {
		t.Fatalf("Compute = %+v, %v", r, err)
	}
}

func trapScript(t *testing.T) (script, marker string) {
	t.Helper()
	d := t.TempDir()
	marker = filepath.Join(d, "MARKER")
	write(t, d, "trap.sh", "#!/bin/sh\necho ran >> "+marker+"\ncat\n")
	script = filepath.Join(d, "trap.sh")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	return script, marker
}

func assertNoMarker(t *testing.T, d, marker string) {
	t.Helper()
	r, err := Compute(context.Background(), d)
	if err != nil || r.State != StateOK || !strings.Contains(r.Diff, "+new") {
		t.Fatalf("Compute = %q, %v, want the diff despite the config", r.Diff, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("a program named by the checkout ran")
	}
}

func TestComputeRunsNoFilterFromAnIncludedConfig(t *testing.T) {
	d := repo(t)
	script, marker := trapScript(t)
	write(t, d, "evil.cfg", "[filter \"x\"]\n\tclean = "+script+"\n")
	run(t, d, "config", "include.path", "../evil.cfg")
	write(t, d, ".gitattributes", "*.txt filter=x\n")
	assertNoMarker(t, d, marker)
}

func TestComputeRunsNoFilterFromWorktreeConfig(t *testing.T) {
	d := repo(t)
	script, marker := trapScript(t)
	run(t, d, "config", "extensions.worktreeConfig", "true")
	write(t, filepath.Join(d, ".git"), "config.worktree", "[filter \"x\"]\n\tclean = "+script+"\n")
	write(t, d, ".gitattributes", "*.txt filter=x\n")
	assertNoMarker(t, d, marker)
}

func TestComputeIgnoresACoreWorktreeOutsideTheDir(t *testing.T) {
	d := repo(t)
	other := t.TempDir()
	write(t, other, "secret.txt", "leak\n")
	run(t, d, "config", "core.worktree", other)
	r, err := Compute(context.Background(), d)
	if err != nil || r.State != StateOK || strings.Contains(r.Diff, "leak") || byPath(r)["secret.txt"].Path != "" || byPath(r)["edit.txt"].Status != "modified" {
		t.Fatalf("Compute = %+v, %v, want the checkout's own changes and nothing from the outside dir", r, err)
	}
}

func TestComputeInASubdirectoryShowsTheWholeRepo(t *testing.T) {
	d := repo(t)
	sub := filepath.Join(d, "pkg", "inner")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Compute(context.Background(), sub)
	if err != nil || r.State != StateOK || byPath(r)["edit.txt"].Status != "modified" || byPath(r)["untracked.txt"].Status != "untracked" {
		t.Fatalf("Compute = %+v, %v, want the repo's changes with repo-relative paths", r, err)
	}
}

func TestComputeManyUntrackedFilesTruncateInsteadOfFailing(t *testing.T) {
	d := repo(t)
	for i := 0; i < 200; i++ {
		write(t, d, "u"+strconv.Itoa(1000+i)+".dat", strings.Repeat("x\n", 20))
	}
	old := maxDiff
	maxDiff = 4000
	t.Cleanup(func() { maxDiff = old })
	r, err := Compute(context.Background(), d)
	if err != nil || r.State != StateOK || !r.Truncated || len(r.Diff) > 4000 {
		t.Fatalf("Compute ok=%v truncated=%v diff=%d err=%v, want ok and truncated", r.State, r.Truncated, len(r.Diff), err)
	}
	if got := byPath(r)["u1199.dat"]; got.Status != "untracked" || got.Added != 20 {
		t.Errorf("u1199.dat = %+v, want listed with 20 lines past the diff budget", got)
	}
}

func TestComputeNearTheDeadlineReturnsWhatItHas(t *testing.T) {
	d := repo(t)
	ctx, cancel := context.WithTimeout(context.Background(), deadlineSlack-time.Second)
	defer cancel()
	r, err := Compute(ctx, d)
	if err != nil || r.State != StateOK || !r.Truncated || byPath(r)["untracked.txt"].Added != 1 {
		t.Fatalf("Compute = %+v, %v, want ok, truncated, the untracked file still listed", r, err)
	}
}

func TestComputeDiffHeadersIgnoreTheCheckoutsPrefixConfigAndKeepNonASCIIPaths(t *testing.T) {
	d := repo(t)
	run(t, d, "config", "diff.noprefix", "true")
	run(t, d, "config", "diff.mnemonicPrefix", "true")
	write(t, d, "café.txt", "tracked later\n")
	write(t, d, "edit.txt", "keep\nnew\nmore\n")
	r, err := Compute(context.Background(), d)
	if err != nil || r.State != StateOK {
		t.Fatalf("Compute = %+v, %v", r, err)
	}
	for _, h := range []string{"diff --git a/edit.txt b/edit.txt", "--- a/edit.txt", "+++ b/edit.txt", "diff --git a/café.txt b/café.txt", "+++ b/café.txt"} {
		if !strings.Contains(r.Diff, h) {
			t.Errorf("diff lacks %q:\n%s", h, r.Diff)
		}
	}
	if byPath(r)["café.txt"].Status != "untracked" {
		t.Errorf("files = %+v", r.Files)
	}
}
