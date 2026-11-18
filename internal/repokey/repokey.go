// Package repokey names a checkout's repo one way for the permission log and for launch, so a rule
// added from a ship's card matches the next ship launched in that repo.
package repokey

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/crew"
)

const gitTimeout = 10 * time.Second

// OriginSlug reads the dir's origin remote as crew.py does: no shell, a timeout, and no git location
// variables, so a failure of any kind means no repo notes. The second result is whether an origin exists.
func OriginSlug(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	out, err := git(dir, "remote", "get-url", "origin")
	if err != nil {
		return "", false
	}
	url := strings.TrimSpace(out)
	slug, _ := crew.OriginSlug(url)
	return slug, url != ""
}

// Key is the repo an allow rule names: the origin's owner/repo (GitHub forms only), else the path of
// the checkout. A linked worktree names its main checkout, which is where launch looks rules up.
func Key(dir string) string {
	if slug, _ := OriginSlug(dir); slug != "" {
		return slug
	}
	path := filepath.Clean(dir)
	if main := mainCheckout(dir); main != "" {
		path = main
	}
	// Git reports real paths, so a linked worktree and its checkout agree only when both are resolved.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

func mainCheckout(dir string) string {
	out, err := git(dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] == lines[1] || filepath.Base(lines[1]) != ".git" {
		return ""
	}
	return filepath.Dir(lines[1])
}

func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var env []string
	for _, kv := range os.Environ() {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE":
		default:
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	return string(out), err
}
