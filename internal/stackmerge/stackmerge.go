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
	now   func() time.Time

	mu      sync.Mutex
	repos   map[string]*sync.Mutex
	cached  map[answer]bool
	backoff map[string]backoff
}

// backoff is a repository's wait after a failed network call, so a repository that cannot be reached does not hold every poll.
type backoff struct {
	until time.Time
	delay time.Duration
	err   error
}

const (
	firstBackoff = 30 * time.Second
	maxBackoff   = 4 * time.Minute
)

// New keeps its bare repositories under home; remote is where a check fetches from.
func New(home string, remote func(moor.StackCheck) string) *Git {
	return &Git{home: home, remote: remote, now: time.Now, repos: map[string]*sync.Mutex{}, cached: map[answer]bool{}, backoff: map[string]backoff{}}
}

// Conflicts asks git whether the top PR's head merges into the base branch in the repository's bare clone, in memory.
// It reads the base's SHA first and fetches only when the answer is not cached and an object is missing. An error means it could not tell.
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
	key := strings.ToLower(c.Host + "/" + c.Repo)
	if wait, ok := g.waiting(key); ok {
		return false, wait
	}
	ctx, cancel := context.WithTimeout(ctx, checkTime)
	defer cancel()

	dir := filepath.Join(g.home, "stackcheck", strings.ToLower(c.Host), strings.ToLower(c.Repo)+".git")
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		if _, err := g.run(ctx, bin, "", nil, nil, "init", "--bare", "--quiet", "--", dir); err != nil {
			return false, err
		}
	}
	url := g.remote(c)
	if strings.HasPrefix(url, "-") {
		return false, errors.New("the remote is not a URL")
	}
	// The token reaches git only through the child's environment, and the helper never follows a redirect to another host.
	cfg := []string{"-c", "credential.helper=", "-c", "http.followRedirects=false"}
	var env, secrets []string
	if g.Token != nil {
		token, err := g.Token(ctx, c)
		if err != nil {
			return false, err
		}
		if token != "" {
			cfg = append(cfg, "-c", `credential.helper=!f() { echo username=x-access-token; echo "password=$ADMIRAL_FETCH_TOKEN"; }; f`)
			env, secrets = []string{"ADMIRAL_FETCH_TOKEN=" + token}, []string{token}
		}
	}
	network := func(args ...string) (string, error) {
		out, err := g.run(ctx, bin, dir, env, secrets, append(append([]string{}, cfg...), args...)...)
		if err != nil {
			g.fail(key, err)
		}
		return out, err
	}

	listed, err := network("ls-remote", "--", url, "refs/heads/"+c.Base)
	if err != nil {
		return false, err
	}
	base, ok := strings.CutSuffix(strings.TrimSpace(listed), "\trefs/heads/"+c.Base)
	if !ok || !shaRe.MatchString(base) {
		return false, fmt.Errorf("the base branch %s was not found", c.Base)
	}
	top := strings.ToLower(c.Head)
	if v, hit := g.answerFor(answer{base, top}); hit {
		g.succeed(key)
		return v, nil
	}
	if !g.has(ctx, bin, dir, base) || !g.has(ctx, bin, dir, top) {
		// A full fetch: a partial clone would ask for blobs later, when the merge has no credentials. Force-updating two fixed refs
		// keeps the clone's reachable set to the base's history and one PR head; gc below prunes the rest.
		if _, err := network("fetch", "--quiet", "--no-tags", "--force", "--no-recurse-submodules", "--", url,
			"refs/heads/"+c.Base+":refs/admiral/base", "refs/pull/"+strconv.Itoa(c.PR)+"/head:refs/admiral/top"); err != nil {
			return false, err
		}
		fetchedTop, err := g.run(ctx, bin, dir, nil, nil, "rev-parse", "--verify", "refs/admiral/top^{commit}")
		if err != nil {
			return false, err
		}
		if !strings.EqualFold(fetchedTop, c.Head) {
			return false, fmt.Errorf("the PR's head is %s, not %s", fetchedTop, c.Head)
		}
		if base, err = g.run(ctx, bin, dir, nil, nil, "rev-parse", "--verify", "refs/admiral/base^{commit}"); err != nil {
			return false, err
		}
		// Unreachable objects, such as an older PR head or merge-tree's results, go after an hour; the answer cache needs none of them.
		_, _ = g.run(ctx, bin, dir, nil, nil, "-c", "gc.pruneExpire=1.hour.ago", "-c", "gc.autoDetach=false", "gc", "--auto", "--quiet")
	}
	g.succeed(key)

	// Exit 1 is a conflict; any other failure, such as git older than 2.38, could not tell.
	_, err = g.run(ctx, bin, dir, nil, nil, "merge-tree", "--write-tree", "--no-messages", base, top)
	conflicts := false
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		conflicts = true
	default:
		return false, err
	}
	g.mu.Lock()
	if len(g.cached) >= maxCached {
		g.cached = map[answer]bool{}
	}
	g.cached[answer{base, top}] = conflicts
	g.mu.Unlock()
	return conflicts, nil
}

func (g *Git) answerFor(k answer) (bool, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.cached[k]
	return v, ok
}

// has says the commit is in the bare clone.
func (g *Git) has(ctx context.Context, bin, dir, sha string) bool {
	_, err := g.run(ctx, bin, dir, nil, nil, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// waiting is the error of a repository still backing off after a failed network call.
func (g *Git) waiting(key string) (error, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	b, ok := g.backoff[key]
	if !ok || !g.now().Before(b.until) {
		return nil, false
	}
	return fmt.Errorf("backing off after a failure: %w", b.err), true
}

func (g *Git) fail(key string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.backoff[key].delay * 2
	if d < firstBackoff {
		d = firstBackoff
	}
	d = min(d, maxBackoff)
	g.backoff[key] = backoff{until: g.now().Add(d), delay: d, err: err}
}

func (g *Git) succeed(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.backoff, key)
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

// gitError is a failed git call with what git printed, never a token.
type gitError struct {
	op  string
	out string
	err error
}

func (e *gitError) Error() string { return "git " + e.op + ": " + e.err.Error() + ": " + e.out }
func (e *gitError) Unwrap() error { return e.err }

// run runs git with nothing of Admiral's environment and no config but its own flags, and returns its trimmed output.
// secrets are blanked out of anything it reports.
func (g *Git) run(ctx context.Context, bin, dir string, env, secrets []string, args ...string) (string, error) {
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
	text := strings.TrimSpace(out.String())
	if err == nil {
		return text, nil
	}
	for _, s := range secrets {
		text = strings.ReplaceAll(text, s, "***")
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 400 {
		text = text[:400]
	}
	op := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		op = args[i]
		break
	}
	return text, &gitError{op: op, out: text, err: err}
}
