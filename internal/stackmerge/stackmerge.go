// Package stackmerge checks in an Admiral-owned bare repository whether a stack's top merges into its base.
package stackmerge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

const (
	checkTime = 90 * time.Second
	maxCached = 256
)

var (
	shaRe  = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]+)?$`)
)

type answer struct{ base, top string }

// Git checks stacks with the git on PATH.
type Git struct {
	home   string
	remote func(moor.StackCheck) string
	// Token is the fleet's token for the fetch; nil or an empty token fetches without credentials.
	Token func(context.Context, moor.StackCheck) (string, error)

	mu     sync.Mutex
	repos  map[string]*sync.Mutex
	cached map[answer]bool
}

// New keeps its bare repositories under home; remote is where a check fetches from.
func New(home string, remote func(moor.StackCheck) string) *Git {
	return &Git{home: home, remote: remote, repos: map[string]*sync.Mutex{}, cached: map[answer]bool{}}
}

// Conflicts fetches the base branch and the top PR's head into the repository's bare clone and asks git to merge them
// in memory. An error means it could not tell.
func (g *Git) Conflicts(ctx context.Context, c moor.StackCheck) (bool, error) {
	if err := validate(c); err != nil {
		return false, err
	}
	bin, err := exec.LookPath("git")
	if err != nil {
		return false, err
	}
	lock := g.repoLock(c)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(ctx, checkTime)
	defer cancel()

	dir := filepath.Join(g.home, "stackcheck", strings.ToLower(c.Host), strings.ToLower(c.Repo)+".git")
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		if _, err := g.run(ctx, bin, "", nil, "init", "--bare", "--quiet", "--", dir); err != nil {
			return false, err
		}
	}
	var env []string
	url := g.remote(c)
	if strings.HasPrefix(url, "-") {
		return false, errors.New("the remote is not a URL")
	}
	args := []string{"-c", "credential.helper="}
	if g.Token != nil {
		token, err := g.Token(ctx, c)
		if err != nil {
			return false, err
		}
		if token != "" {
			args = append(args, "-c", `credential.helper=!f() { echo username=x-access-token; echo "password=$ADMIRAL_FETCH_TOKEN"; }; f`)
			env = append(env, "ADMIRAL_FETCH_TOKEN="+token)
		}
	}
	// A full fetch: a partial clone would ask for blobs later, when the merge has no credentials.
	args = append(args, "fetch", "--quiet", "--no-tags", "--force", "--no-recurse-submodules", "--", url,
		"refs/heads/"+c.Base+":refs/admiral/base",
		"refs/pull/"+strconv.Itoa(c.PR)+"/head:refs/admiral/top")
	if _, err := g.run(ctx, bin, dir, env, args...); err != nil {
		return false, err
	}
	top, err := g.run(ctx, bin, dir, nil, "rev-parse", "--verify", "refs/admiral/top^{commit}")
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(top, c.Head) {
		return false, fmt.Errorf("the PR's head is %s, not %s", top, c.Head)
	}
	base, err := g.run(ctx, bin, dir, nil, "rev-parse", "--verify", "refs/admiral/base^{commit}")
	if err != nil {
		return false, err
	}
	key := answer{base, top}
	g.mu.Lock()
	if v, ok := g.cached[key]; ok {
		g.mu.Unlock()
		return v, nil
	}
	g.mu.Unlock()

	// Exit 1 is a conflict; any other failure, such as git older than 2.38, could not tell.
	out, err := g.run(ctx, bin, dir, nil, "merge-tree", "--write-tree", "--no-messages", base, top)
	conflicts := false
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		conflicts = true
	default:
		return false, fmt.Errorf("git merge-tree: %w (%s)", err, out)
	}
	g.mu.Lock()
	if len(g.cached) >= maxCached {
		g.cached = map[answer]bool{}
	}
	g.cached[key] = conflicts
	g.mu.Unlock()
	return conflicts, nil
}

func (g *Git) repoLock(c moor.StackCheck) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := strings.ToLower(c.Host + "/" + c.Repo)
	if g.repos[key] == nil {
		g.repos[key] = &sync.Mutex{}
	}
	return g.repos[key]
}

func validate(c moor.StackCheck) error {
	switch {
	case !hostRe.MatchString(c.Host):
		return fmt.Errorf("host %q is not a host", c.Host)
	case !repoRe.MatchString(c.Repo) || strings.Contains(c.Repo, ".."):
		return fmt.Errorf("repository %q is not owner/name", c.Repo)
	case !shaRe.MatchString(strings.ToLower(c.Head)):
		return fmt.Errorf("head %q is not a commit SHA", c.Head)
	case c.PR <= 0:
		return errors.New("no pull request number")
	case c.Base == "" || strings.HasPrefix(c.Base, "-") || strings.HasPrefix(c.Base, "/") || strings.HasSuffix(c.Base, "/") ||
		strings.HasSuffix(c.Base, ".") || strings.HasSuffix(c.Base, ".lock") || strings.Contains(c.Base, "..") || strings.Contains(c.Base, "//") ||
		strings.Contains(c.Base, "@{") || strings.ContainsAny(c.Base, " ~^:?*[\\\x7f") || strings.IndexFunc(c.Base, func(r rune) bool { return r < ' ' }) >= 0:
		return fmt.Errorf("base %q is not a branch name", c.Base)
	}
	return nil
}

// run runs git with nothing of Admiral's environment and no config but its own flags, and returns its trimmed output.
func (g *Git) run(ctx context.Context, bin, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin",
		"HOME=/nonexistent", "XDG_CONFIG_HOME=/nonexistent",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C",
	}, env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}
