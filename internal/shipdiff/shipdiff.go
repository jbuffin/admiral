// Package shipdiff reads what a ship has changed in its checkout: a diff against the merge-base with the base branch.
package shipdiff

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/neutralgit"
)

const (
	StateOK   = "ok"
	StateNone = "none"

	MaxDiff   = 2 << 20 // bytes of unified diff returned
	MaxFiles  = 2000
	totalTime = 20 * time.Second

	maxCountRead  = 64 << 20        // bytes read from one untracked file to count its lines
	deadlineSlack = 3 * time.Second // below this much time left, per-file diffs stop
)

// maxDiff is MaxDiff, a variable so a test can spend the budget with few files.
var maxDiff = MaxDiff

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
	// The work tree is where the .git entry is, not wherever the ship's core.worktree says.
	top, ok := workTreeOf(dir)
	if !ok {
		return none, nil
	}
	dir = top
	g, err := neutralgit.New(ctx, dir)
	if err != nil {
		return none, err
	}
	g.Env = append(g.Env, "GIT_WORK_TREE="+dir) // beats core.worktree, which a -c cannot override
	if _, err := g.List(ctx, "rev-parse", "--is-inside-work-tree"); err != nil {
		return none, nil
	}
	head, err := g.List(ctx, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return none, nil // no commit yet
	}
	base := strings.TrimSpace(string(head))
	if ref := baseRef(ctx, g); ref != "" {
		if mb, err := g.List(ctx, "merge-base", ref, "HEAD"); err == nil && strings.TrimSpace(string(mb)) != "" {
			base = strings.TrimSpace(string(mb))
		}
	}

	res := Result{State: StateOK, Files: []File{}}
	status, err := g.List(ctx, "diff", "-M", "--name-status", "-z", base, "--")
	if err != nil {
		return none, err
	}
	numstat, err := g.List(ctx, "diff", "-M", "--numstat", "-z", base, "--")
	if err != nil {
		return none, err
	}
	res.Files = mergeFiles(parseNameStatus(status), parseNumstat(numstat))
	untracked, err := g.List(ctx, "ls-files", "--others", "--exclude-standard", "-z")
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
	patch, cut, err := g.Capped(ctx, maxDiff, "diff", "-M", base, "--")
	if err != nil {
		return none, err
	}
	diff.Write(patch)
	res.Truncated = cut
	root, err := os.OpenRoot(dir)
	if err != nil {
		return none, nil
	}
	defer root.Close()
	for _, p := range paths {
		if len(res.Files) >= MaxFiles {
			res.Truncated = true
			break
		}
		res.Files = append(res.Files, File{Path: p, Status: "untracked", Added: countLines(root, p)})
		if diff.Len() >= maxDiff {
			res.Truncated = true
			continue
		}
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < deadlineSlack {
			res.Truncated = true
			continue
		}
		part, cut, err := g.Capped(ctx, maxDiff-diff.Len(), "diff", "--no-index", "--", os.DevNull, p)
		if err != nil {
			if ctx.Err() != nil {
				res.Truncated = true
				continue
			}
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

// workTreeOf walks up from dir to the directory holding a .git entry (a directory, or a file in a linked worktree).
func workTreeOf(dir string) (string, bool) {
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// baseRef is the repo's default branch: origin/HEAD, else main.
func baseRef(ctx context.Context, g *neutralgit.Git) string {
	if out, err := g.List(ctx, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(string(out)); strings.HasPrefix(ref, "refs/") {
			if _, err := g.List(ctx, "rev-parse", "--verify", "-q", ref); err == nil {
				return ref
			}
		}
	}
	for _, ref := range []string{"refs/heads/main", "refs/remotes/origin/main"} {
		if _, err := g.List(ctx, "rev-parse", "--verify", "-q", ref); err == nil {
			return ref
		}
	}
	return ""
}

// countLines is what git would count for an untracked file: 0 for a binary or an unreadable one. It never
// follows a symlink, which counts as the one line of its target.
func countLines(root *os.Root, p string) int {
	st, err := root.Lstat(p)
	if err != nil {
		return 0
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return 1
	}
	if !st.Mode().IsRegular() {
		return 0
	}
	f, err := root.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n, total, first, last := 0, 0, true, byte('\n')
	for total < maxCountRead {
		r, err := f.Read(buf)
		if r > 0 {
			chunk := buf[:r]
			if first && bytes.IndexByte(chunk[:min(r, 8000)], 0) >= 0 {
				return 0
			}
			first = false
			n += bytes.Count(chunk, []byte{'\n'})
			last = chunk[r-1]
			total += r
		}
		if err != nil {
			break
		}
	}
	if total > 0 && last != '\n' {
		n++
	}
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
