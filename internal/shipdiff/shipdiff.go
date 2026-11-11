// Package shipdiff reads what a ship has changed in its checkout: a diff against the merge-base with the base branch.
package shipdiff

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	StateOK   = "ok"
	StateNone = "none"

	MaxDiff    = 2 << 20 // bytes of unified diff returned
	MaxFiles   = 2000
	callTime   = 10 * time.Second
	totalTime  = 20 * time.Second
	maxListOut = 4 << 20 // bytes read from a listing
)

type File struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

type Result struct {
	State     string `json:"state"`
	Files     []File `json:"files"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

// Compute diffs dir's work tree, uncommitted and untracked files included, against its merge-base with the
// repo's default branch. A directory that is not a git checkout, or one with no commit, reads as StateNone.
func Compute(ctx context.Context, dir string) (Result, error) {
	none := Result{State: StateNone, Files: []File{}}
	ctx, cancel := context.WithTimeout(ctx, totalTime)
	defer cancel()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return none, nil
	}
	g, err := newGit(ctx, dir)
	if err != nil {
		return none, err
	}
	if _, err := g.list(ctx, "rev-parse", "--is-inside-work-tree"); err != nil {
		return none, nil
	}
	head, err := g.list(ctx, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return none, nil // no commit yet
	}
	base := strings.TrimSpace(string(head))
	if ref := g.baseRef(ctx); ref != "" {
		if mb, err := g.list(ctx, "merge-base", ref, "HEAD"); err == nil && strings.TrimSpace(string(mb)) != "" {
			base = strings.TrimSpace(string(mb))
		}
	}

	res := Result{State: StateOK, Files: []File{}}
	status, err := g.list(ctx, "diff", "-M", "--name-status", "-z", base)
	if err != nil {
		return none, err
	}
	numstat, err := g.list(ctx, "diff", "-M", "--numstat", "-z", base)
	if err != nil {
		return none, err
	}
	res.Files = mergeFiles(parseNameStatus(status), parseNumstat(numstat))
	untracked, err := g.list(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return none, err
	}
	var paths []string
	for _, p := range strings.Split(string(untracked), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	var diff bytes.Buffer
	patch, cut, err := g.capped(ctx, MaxDiff, "diff", "-M", base)
	if err != nil {
		return none, err
	}
	diff.Write(patch)
	res.Truncated = cut
	for _, p := range paths {
		if len(res.Files) >= MaxFiles {
			res.Truncated = true
			break
		}
		added := g.untrackedLines(ctx, p)
		res.Files = append(res.Files, File{Path: p, Status: "untracked", Added: added})
		if diff.Len() >= MaxDiff {
			res.Truncated = true
			continue
		}
		part, cut, err := g.capped(ctx, MaxDiff-diff.Len(), "diff", "--no-index", "--", os.DevNull, p)
		if err != nil {
			return none, err
		}
		diff.Write(part)
		if cut {
			res.Truncated = true
		}
	}
	if len(res.Files) > MaxFiles {
		res.Files, res.Truncated = res.Files[:MaxFiles], true
	}
	res.Diff = diff.String()
	return res, nil
}

type git struct {
	bin, dir string
	env      []string
	neutral  []string // -c options that blank every program the checkout's config names
}

// newGit builds the safe invocation: the checkout's .git/config and .gitattributes are the ship's to write,
// so no program they name may run, and nothing from Admiral's own environment reaches git.
func newGit(ctx context.Context, dir string) (*git, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	g := &git{bin: bin, dir: dir, env: []string{
		"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin",
		"HOME=/nonexistent", "XDG_CONFIG_HOME=/nonexistent",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "LC_ALL=C",
	}}
	g.neutral = []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "core.pager=cat",
		"-c", "core.alternateRefsCommand=", "-c", "diff.external=", "-c", "diff.ignoreSubmodules=all"}
	if keys, err := g.list(ctx, "config", "--local", "--name-only", "-z", "--get-regexp", `^(filter|diff)\..*\.`); err == nil {
		seen := map[string]bool{}
		for _, k := range strings.Split(string(keys), "\x00") {
			i := strings.LastIndex(k, ".")
			if i < 0 || seen[k[:i]] {
				continue
			}
			seen[k[:i]] = true
			prefix := k[:i]
			vars := []string{"textconv", "command"}
			if strings.HasPrefix(prefix, "filter.") {
				vars = []string{"clean", "smudge", "process"}
			}
			for _, v := range vars {
				g.neutral = append(g.neutral, "-c", prefix+"."+v+"=")
			}
		}
	}
	return g, nil
}

var safeDiffFlags = []string{"--no-ext-diff", "--no-textconv", "--ignore-submodules=all", "--no-color"}

func (g *git) cmd(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{"--no-pager"}, g.neutral...)
	full = append(full, args[0])
	rest := args[1:]
	if args[0] == "diff" {
		full = append(full, safeDiffFlags...)
	}
	full = append(full, rest...)
	c := exec.CommandContext(ctx, g.bin, full...)
	c.Dir, c.Env = g.dir, g.env
	c.WaitDelay = time.Second
	return c
}

// list runs git and returns its whole output (at most maxListOut). git diff --no-index exits 1 on a difference.
func (g *git) list(ctx context.Context, args ...string) ([]byte, error) {
	out, cut, err := g.capped(ctx, maxListOut, args...)
	if cut && err == nil {
		return out, errors.New("git output too large")
	}
	return out, err
}

// capped reads at most limit bytes of the output, stops git when there is more, and cuts at a line end.
func (g *git) capped(ctx context.Context, limit int, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, callTime)
	defer cancel()
	c := g.cmd(ctx, args...)
	out, err := c.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := c.Start(); err != nil {
		return nil, false, err
	}
	buf, _ := io.ReadAll(io.LimitReader(out, int64(limit)+1))
	cut := len(buf) > limit
	if cut {
		cancel()
		buf = buf[:limit]
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			buf = buf[:i+1]
		}
	}
	err = c.Wait()
	if cut {
		return buf, true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(args) > 1 && args[0] == "diff" {
		err = nil // --no-index and --exit-code style: a difference
	}
	if err != nil {
		return nil, false, err
	}
	return buf, false, nil
}

// baseRef is the repo's default branch: origin/HEAD, else main.
func (g *git) baseRef(ctx context.Context) string {
	if out, err := g.list(ctx, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(string(out)); strings.HasPrefix(ref, "refs/") {
			if _, err := g.list(ctx, "rev-parse", "--verify", "-q", ref); err == nil {
				return ref
			}
		}
	}
	for _, ref := range []string{"refs/heads/main", "refs/remotes/origin/main"} {
		if _, err := g.list(ctx, "rev-parse", "--verify", "-q", ref); err == nil {
			return ref
		}
	}
	return ""
}

func (g *git) untrackedLines(ctx context.Context, p string) int {
	out, err := g.list(ctx, "diff", "--no-index", "--numstat", "--", os.DevNull, p)
	if err != nil {
		return 0
	}
	f := strings.SplitN(string(out), "\t", 3)
	if len(f) < 3 {
		return 0
	}
	n, _ := strconv.Atoi(f[0])
	return n
}

func statusName(code string) string {
	switch code[0] {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "typechange"
	case 'U':
		return "unmerged"
	}
	return "modified"
}

func parseNameStatus(b []byte) []File {
	tok := strings.Split(string(b), "\x00")
	var out []File
	for i := 0; i < len(tok); i++ {
		code := tok[i]
		if code == "" {
			continue
		}
		n := 1
		if code[0] == 'R' || code[0] == 'C' {
			n = 2
		}
		if i+n >= len(tok) {
			break
		}
		out = append(out, File{Path: tok[i+n], Status: statusName(code)})
		i += n
	}
	return out
}

func parseNumstat(b []byte) map[string][2]int {
	tok := strings.Split(string(b), "\x00")
	m := map[string][2]int{}
	for i := 0; i < len(tok); i++ {
		f := strings.SplitN(tok[i], "\t", 3)
		if len(f) < 3 {
			continue
		}
		path := f[2]
		if path == "" { // a rename: the next two tokens are the old and the new path
			if i+2 >= len(tok) {
				break
			}
			path = tok[i+2]
			i += 2
		}
		a, _ := strconv.Atoi(f[0]) // "-" for a binary file reads 0
		r, _ := strconv.Atoi(f[1])
		m[path] = [2]int{a, r}
	}
	return m
}

func mergeFiles(files []File, counts map[string][2]int) []File {
	for i := range files {
		c := counts[files[i].Path]
		files[i].Added, files[i].Removed = c[0], c[1]
	}
	if files == nil {
		files = []File{}
	}
	return files
}
