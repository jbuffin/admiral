package launcher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/crew"
)

// Worktrees makes the git worktree a ship launched with no skill works in.
type Worktrees interface {
	// Fetch fetches origin's default branch into dir's repo and returns the tracking ref a worktree is cut from; a
	// checkout with no origin returns its local default branch, refs/heads/<b>, with no fetch. An empty ref with no
	// error means dir is not a git checkout, so the ship runs in place. It needs only dir, so a launch calls it before
	// taking the launch lock.
	Fetch(dir string) (tracking string, err error)
	// Add makes a worktree of dir for the session, on a new branch admiral/<session> cut from tracking, and returns its path.
	Add(home, session, dir, tracking string) (path string, err error)
}

const worktreeGitTimeout = 30 * time.Second

var errGitTimeout = errors.New("timed out")

func worktreePath(home, session string) string { return filepath.Join(home, "worktrees", session) }

// GitWorktrees makes worktrees under $ADMIRAL_HOME/worktrees with git. Timeout bounds each git call; zero means 30s.
type GitWorktrees struct{ Timeout time.Duration }

// boundedGit runs one git call in dir that ends at the deadline and can never wait on a person: no credential or
// host-key prompt, unless the engineer's environment already says how git should ask.
func boundedGit(timeout time.Duration, dir string, args ...string) (string, error) {
	if timeout == 0 {
		timeout = worktreeGitTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = os.Environ()
	if _, ok := os.LookupEnv("GIT_TERMINAL_PROMPT"); !ok {
		cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	}
	if _, ok := os.LookupEnv("GIT_SSH_COMMAND"); !ok {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s ran past %s: %w", args[0], timeout, errGitTimeout)
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g GitWorktrees) git(dir string, args ...string) (string, error) {
	return boundedGit(g.Timeout, dir, args...)
}

func fetchFailed(dir string, err error) error {
	if errors.Is(err, errGitTimeout) {
		return fmt.Errorf("fetching the origin remote of %s timed out: %w", dir, err)
	}
	return fmt.Errorf("cannot fetch the origin remote of %s: %w", dir, err)
}

func (g GitWorktrees) Fetch(dir string) (string, error) {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		return "", nil
	}
	origin, err := originURL(g.Timeout, dir)
	if err != nil {
		return "", fmt.Errorf("cannot read the origin remote of %s: %w", dir, err)
	}
	if origin == "" {
		return g.localDefault(dir)
	}
	// A plain fetch doesn't set refs/remotes/origin/HEAD, so ask the remote which branch is its default.
	head, err := g.git(dir, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", fetchFailed(dir, err)
	}
	branch := ""
	for _, line := range strings.Split(head, "\n") {
		if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			branch, _, _ = strings.Cut(ref, "\t")
		}
	}
	if branch == "" {
		return "", fmt.Errorf("the origin remote of %s names no default branch", dir)
	}
	tracking := "refs/remotes/origin/" + branch
	if _, err := g.git(dir, "fetch", "--quiet", "origin", "+refs/heads/"+branch+":"+tracking); err != nil {
		return "", fetchFailed(dir, err)
	}
	return tracking, nil
}

// localDefault is the ref a worktree of a checkout with no origin is cut from: init.defaultBranch, main or master,
// the first that exists as a local branch.
func (g GitWorktrees) localDefault(dir string) (string, error) {
	var tried []string
	if b, err := g.git(dir, "config", "--get", "init.defaultBranch"); err == nil && b != "" {
		tried = append(tried, b)
	}
	tried = append(tried, "main", "master")
	for _, b := range tried {
		if _, err := g.git(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return "refs/heads/" + b, nil
		}
	}
	return "", fmt.Errorf("%s has no origin remote and none of its local branches %s exists", dir, strings.Join(tried, ", "))
}

// originURL is the URL configured for origin, "" when there is none (git config exits 1 for a missing key); any other
// failure is an error, so a timeout never reads as "no origin". It reads the config, not git remote get-url,
// which applies url.<x>.insteadOf and so shows a rewritten GitHub origin as whatever it was rewritten to.
func originURL(timeout time.Duration, dir string) (string, error) {
	out, err := boundedGit(timeout, dir, "config", "--get", "remote.origin.url")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	return out, err
}

// LocalOnly reports whether dir is in a working git checkout with no GitHub origin: no origin at all, or one that is not a GitHub repo.
func LocalOnly(dir string) bool {
	if _, err := boundedGit(0, dir, "rev-parse", "--git-dir"); err != nil {
		return false
	}
	origin, err := originURL(0, dir)
	if err != nil {
		return false
	}
	_, github := crew.OriginSlug(origin)
	return !github
}

func (g GitWorktrees) Add(home, session, dir, tracking string) (string, error) {
	if session == "." || !filepath.IsLocal(session) || strings.ContainsRune(session, filepath.Separator) {
		return "", fmt.Errorf("%q is not a name a ship can have", session)
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	path := worktreePath(home, session)
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	branch := "refs/heads/admiral/" + session
	if _, err := g.git(dir, "show-ref", "--verify", "--quiet", branch); err == nil {
		return "", fmt.Errorf("the branch admiral/%s already exists in %s", session, dir)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if _, err := g.git(dir, "worktree", "add", "--quiet", "--no-track", "-b", "admiral/"+session, path, tracking); err != nil {
		RemoveWorktree(home, session, dir)
		// The branch did not exist before this call, so one that is there now is the one it made.
		if _, e := g.git(dir, "show-ref", "--verify", "--quiet", branch); e == nil {
			_, _ = g.git(dir, "branch", "-D", "admiral/"+session)
		}
		return "", err
	}
	// Claude's cwd is the real path.
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		RemoveWorktree(home, session, dir)
		return "", err
	}
	return real, nil
}

// HasWorktree reports whether a worktree made for the session is still there.
func HasWorktree(home, session string) bool {
	if _, _, ok := ShipFiles(home, session); !ok {
		return false
	}
	_, err := os.Lstat(worktreePath(home, session))
	return err == nil
}

// RemoveWorktree deletes the session's worktree and its registration in checkout's repo, and keeps the branch. It takes
// the repo from the ship's checkout, not from the worktree's own .git file, and works on a worktree that is damaged or gone.
func RemoveWorktree(home, session, checkout string) {
	if _, _, ok := ShipFiles(home, session); !ok {
		return
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return
	}
	path := worktreePath(home, session)
	// git records the real path.
	real := path
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		real = filepath.Join(parent, filepath.Base(path))
	}
	if checkout == "" {
		_ = os.RemoveAll(path)
		return
	}
	_, _ = boundedGit(0, checkout, "worktree", "remove", "--force", real)
	_ = os.RemoveAll(path)
	// A worktree git could not remove leaves its registration; drop only this one.
	common, err := boundedGit(0, checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return
	}
	admin := filepath.Join(common, "worktrees")
	ents, _ := os.ReadDir(admin)
	for _, e := range ents {
		if b, err := os.ReadFile(filepath.Join(admin, e.Name(), "gitdir")); err == nil && strings.TrimSpace(string(b)) == filepath.Join(real, ".git") {
			_ = os.RemoveAll(filepath.Join(admin, e.Name()))
		}
	}
}

// shipWorktree is the worktree a launch runs in. A relaunch reuses the one its session has, or runs in the checkout when
// it has none; only a fresh launch that fetched a tracking ref makes one. The path is empty to run in the checkout, and
// undo removes only a worktree this launch made.
func (l *Launcher) shipWorktree(req Request, session, tracking string) (path string, undo func(), err error) {
	undo = func() {}
	if l.Worktrees == nil {
		return "", undo, nil
	}
	home, err := filepath.Abs(l.Home)
	if err != nil {
		return "", undo, refuse(http.StatusInternalServerError, "could not resolve %s: %v", l.Home, err)
	}
	if req.Session != "" {
		if !HasWorktree(home, session) {
			return "", undo, nil
		}
		real, err := filepath.EvalSymlinks(worktreePath(home, session))
		if err != nil {
			return "", undo, refuse(http.StatusInternalServerError, "%v", err)
		}
		return real, undo, nil
	}
	if tracking == "" {
		return "", undo, nil
	}
	path, err = l.Worktrees.Add(home, session, req.Dir, tracking)
	if err != nil {
		return "", undo, refuse(http.StatusBadRequest, "no worktree for the ship: %v", err)
	}
	checkout := req.Dir
	return path, func() { RemoveWorktree(home, session, checkout) }, nil
}

// fetchForWorktree fetches origin for a fresh no-skill or drafting launch that will get a worktree, before the launch lock is taken.
func (l *Launcher) fetchForWorktree(req Request, plain bool) (string, error) {
	if l.Worktrees == nil || !plain || req.Session != "" || (req.Skill.Kind != "none" && !req.Drafting) {
		return "", nil
	}
	tracking, err := l.Worktrees.Fetch(req.Dir)
	if err != nil {
		return "", refuse(http.StatusBadRequest, "no worktree for the ship: %v", err)
	}
	return tracking, nil
}
