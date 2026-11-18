package launcher

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktrees makes the git worktree a ship launched with no skill works in.
type Worktrees interface {
	// Make fetches origin and returns a new worktree of dir for the session, on branch admiral/<session> cut from
	// origin's default branch. An empty path with no error means dir is not a git checkout, so the ship runs in place.
	Make(home, session, dir string) (path string, err error)
}

func worktreePath(home, session string) string { return filepath.Join(home, "worktrees", session) }

// GitWorktrees makes worktrees under $ADMIRAL_HOME/worktrees with git.
type GitWorktrees struct{}

func (GitWorktrees) Make(home, session, dir string) (string, error) {
	if session == "." || !filepath.IsLocal(session) || strings.ContainsRune(session, filepath.Separator) {
		return "", fmt.Errorf("%q is not a name a ship can have", session)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		return "", nil
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	path := worktreePath(home, session)
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	run := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	// A plain fetch doesn't set refs/remotes/origin/HEAD, so ask the remote which branch is its default.
	head, err := run("ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", fmt.Errorf("cannot reach the origin remote of %s: %w", dir, err)
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
	if _, err := run("fetch", "--quiet", "origin", "+refs/heads/"+branch+":"+tracking); err != nil {
		return "", fmt.Errorf("cannot fetch the origin remote of %s: %w", dir, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if _, err := run("worktree", "add", "--quiet", "--no-track", "-b", "admiral/"+session, path, tracking); err != nil {
		_ = os.RemoveAll(path)
		return "", err
	}
	if err := ignoreAdmiralDir(dir); err != nil {
		RemoveWorktree(home, session)
		return "", err
	}
	// Claude's cwd is the real path.
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		RemoveWorktree(home, session)
		return "", err
	}
	return real, nil
}

// ignoreAdmiralDir keeps the feedback files Admiral writes into a worktree out of the ship's git status. Git reads
// info/exclude from the repo's common dir, so it is shared with the checkout.
func ignoreAdmiralDir(dir string) error {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude").Output()
	if err != nil {
		return fmt.Errorf("git rev-parse of %s: %w", dir, err)
	}
	exclude := strings.TrimSpace(string(out))
	if b, err := os.ReadFile(exclude); err == nil && strings.Contains("\n"+string(b), "\n.admiral/\n") {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString("\n.admiral/\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// HasWorktree reports whether a worktree made for the session is still there.
func HasWorktree(home, session string) bool {
	if _, _, ok := ShipFiles(home, session); !ok {
		return false
	}
	_, err := os.Lstat(worktreePath(home, session))
	return err == nil
}

// RemoveWorktree deletes the session's worktree and its registration in the checkout, and keeps the branch.
func RemoveWorktree(home, session string) {
	if _, _, ok := ShipFiles(home, session); !ok {
		return
	}
	path := worktreePath(home, session)
	if _, err := os.Lstat(path); err != nil {
		return
	}
	common, err := exec.Command("git", "-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	_ = os.RemoveAll(path)
	if err == nil {
		_ = exec.Command("git", "--git-dir="+strings.TrimSpace(string(common)), "worktree", "prune").Run()
	}
}

// shipWorktree is the worktree a launch runs in: a relaunch reuses the one its session has, and a no-skill launch
// makes one. The path is empty to run in the checkout, and undo removes only a worktree this launch made.
func (l *Launcher) shipWorktree(req Request, session string) (path string, undo func(), err error) {
	undo = func() {}
	if l.Worktrees == nil {
		return "", undo, nil
	}
	home, err := filepath.Abs(l.Home)
	if err != nil {
		return "", undo, refuse(http.StatusInternalServerError, "could not resolve %s: %v", l.Home, err)
	}
	if req.Session != "" && HasWorktree(home, session) {
		real, err := filepath.EvalSymlinks(worktreePath(home, session))
		if err != nil {
			return "", undo, refuse(http.StatusInternalServerError, "%v", err)
		}
		return real, undo, nil
	}
	if req.Skill.Kind != "none" {
		return "", undo, nil
	}
	path, err = l.Worktrees.Make(home, session, req.Dir)
	if err != nil {
		return "", undo, refuse(http.StatusBadRequest, "no worktree for the ship: %v", err)
	}
	return path, func() { RemoveWorktree(home, session) }, nil
}
