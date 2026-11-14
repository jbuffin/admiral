// Package promote reads the work a restricted ship left in its clone, through neutral git only.
package promote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbuffin/admiral/internal/neutralgit"
)

var (
	ErrNothing      = errors.New("nothing to promote")
	ErrAdmiralPaths = errors.New("commits touch .admiral/")
	ErrAlternates   = errors.New("the clone's alternates were changed")
	ErrHarness      = errors.New("the work touches harness config")
	ErrSymlink      = errors.New("the work adds a symlink")
)

// Kind is how the restricted ship left its work.
type Kind string

const (
	Commits Kind = "commits"
	Diff    Kind = "diff"
)

// Work is what Promote starts from: the commits base..tip, or an uncommitted diff against Dir's HEAD.
type Work struct {
	Kind Kind
	// Base is the merge-base of the tip and Dir's HEAD; Tip is empty for a Diff.
	Base, Tip string
	// Range is "<base>..<tip>" or "uncommitted changes".
	Range string
	// Patch is the binary diff of a Diff, with .admiral/ left out.
	Patch string
}

// admiralPath keeps the ship's own files out of what a normal ship takes; icase because a checkout can sit on a case-folding disk.
const admiralPath = ":(top,icase).admiral"

// harnessPaths are the project files a harness loads as settings, hooks, servers or instructions; a normal ship with tokens
// must not start on ones an untrusted ship wrote. Case-insensitive like admiralPath.
var harnessPaths = []string{":(top,icase).claude", ":(top,icase).mcp.json", ":(top,icase)CLAUDE.md", ":(top,icase)CLAUDE.local.md",
	":(top,icase)AGENTS.md", ":(top,icase).codex", ":(top,icase).github/copilot-instructions.md"}

// Read reads the work in clone's branch against dir, refusing as the sentinels say.
func Read(ctx context.Context, dir, clone, branch string) (Work, error) {
	dg, err := neutralgit.New(ctx, dir)
	if err != nil {
		return Work{}, err
	}
	objects, err := dg.List(ctx, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return Work{}, fmt.Errorf("reading %s: %w", dir, err)
	}
	if err := checkAlternates(clone, strings.TrimSpace(string(objects))); err != nil {
		return Work{}, err
	}
	head, err := dg.List(ctx, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return Work{}, fmt.Errorf("%s has no commit: %w", dir, err)
	}
	cg, err := neutralgit.New(ctx, clone)
	if err != nil {
		return Work{}, err
	}
	cg.Env = append(cg.Env, "GIT_WORK_TREE="+clone) // beats core.worktree, which a -c cannot override
	tipOut, err := cg.List(ctx, "rev-parse", "--verify", "-q", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return Work{}, fmt.Errorf("the clone has no branch %s", branch)
	}
	tip := strings.TrimSpace(string(tipOut))
	baseOut, err := cg.List(ctx, "merge-base", tip, strings.TrimSpace(string(head)))
	if err != nil {
		return Work{}, fmt.Errorf("the branch shares no history with %s", dir)
	}
	base := strings.TrimSpace(string(baseOut))
	span := base + ".." + tip
	if tip != base {
		if err := CheckRange(ctx, cg, base, tip); err != nil {
			return Work{}, err
		}
		return Work{Kind: Commits, Base: base, Tip: tip, Range: span}, nil
	}
	patch, err := uncommitted(ctx, cg, clone, base)
	if err != nil {
		return Work{}, err
	}
	if len(patch) == 0 {
		return Work{}, ErrNothing
	}
	return Work{Kind: Diff, Base: base, Range: "uncommitted changes", Patch: string(patch)}, nil
}

// CheckRange refuses commits base..tip that touch .admiral/ or harness config, or add a symlink.
func CheckRange(ctx context.Context, g *neutralgit.Git, base, tip string) error {
	span := base + ".." + tip
	for _, c := range []struct {
		paths []string
		err   error
	}{{[]string{admiralPath}, ErrAdmiralPaths}, {harnessPaths, ErrHarness}} {
		touched, err := g.List(ctx, append([]string{"rev-list", "--full-history", "-n1", span, "--"}, c.paths...)...)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(touched)) > 0 {
			return c.err
		}
	}
	raw, err := g.List(ctx, "log", "-m", "--format=", "--raw", "--no-renames", "--diff-filter=AMT", span)
	if err != nil {
		return err
	}
	if addsSymlink(raw) {
		return ErrSymlink
	}
	return nil
}

// Verify recomputes the merge-base of tip and HEAD in g's repository and checks the range as Read does; it returns the base.
func Verify(ctx context.Context, g *neutralgit.Git, tip string) (string, error) {
	out, err := g.List(ctx, "merge-base", tip, "HEAD")
	if err != nil {
		return "", fmt.Errorf("the branch shares no history with HEAD")
	}
	base := strings.TrimSpace(string(out))
	return base, CheckRange(ctx, g, base, tip)
}

// addsSymlink reports whether git's --raw output has a path that ends as a symlink (mode 120000).
func addsSymlink(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) > 1 && strings.HasPrefix(line, ":") && f[1] == "120000" {
			return true
		}
	}
	return false
}

// checkAlternates refuses a clone whose alternates name anything but dir's objects, as Checkouts.Make wrote them.
func checkAlternates(clone, objects string) error {
	b, err := os.ReadFile(filepath.Join(clone, ".git", "objects", "info", "alternates"))
	if err != nil {
		return ErrAlternates
	}
	want, err := filepath.EvalSymlinks(objects)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		return ErrAlternates
	}
	if got, err := filepath.EvalSymlinks(lines[0]); err != nil || got != want {
		return ErrAlternates
	}
	return nil
}

// uncommitted diffs the clone's work tree, untracked files included, against base through a scratch index,
// so neither the clone's index nor its info/exclude decides what counts.
func uncommitted(ctx context.Context, g *neutralgit.Git, clone, base string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "admiral-promote-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	g.Env = append(g.Env, "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))
	for _, args := range [][]string{
		{"read-tree", base},
		{"add", "-A"}, // an exclude pathspec here makes git exit 1 over an ignored .admiral; the diff leaves it out
	} {
		if _, err := g.List(ctx, args...); err != nil {
			return nil, fmt.Errorf("git %s in %s: %w", args[0], clone, err)
		}
	}
	notAdmiral := []string{"--", ".", ":(exclude,top,icase).admiral"}
	touched, err := g.List(ctx, append([]string{"diff", "--cached", "--name-only", base, "--"}, harnessPaths...)...)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(touched)) > 0 {
		return nil, ErrHarness
	}
	raw, err := g.List(ctx, append([]string{"diff", "--cached", "--raw", "--no-renames", "--diff-filter=AMT", base}, notAdmiral...)...)
	if err != nil {
		return nil, err
	}
	if addsSymlink(raw) {
		return nil, ErrSymlink
	}
	return g.List(ctx, append([]string{"diff", "--cached", "--binary", "--full-index", base}, notAdmiral...)...)
}
