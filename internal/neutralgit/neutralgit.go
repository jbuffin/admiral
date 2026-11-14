// Package neutralgit runs git on a checkout a ship can write, so no program its config, hooks or attributes name can run.
package neutralgit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	callTime   = 10 * time.Second
	maxListOut = 4 << 20 // bytes read from a listing
)

type Git struct {
	Bin, Dir string
	Env      []string
	neutral  []string // -c options that blank every program the checkout's config names
}

// New builds the safe invocation: the checkout's .git/config and .gitattributes are the ship's to write,
// so no program they name may run, and nothing from Admiral's own environment reaches git.
func New(ctx context.Context, dir string) (*Git, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	g := &Git{Bin: bin, Dir: dir, Env: []string{
		"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin",
		"HOME=/nonexistent", "XDG_CONFIG_HOME=/nonexistent",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_GRAFT_FILE=/dev/null", // refs/replace and info/grafts are the ship's to write
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "LC_ALL=C",
	}}
	g.neutral = []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "core.pager=cat",
		"-c", "core.alternateRefsCommand=", "-c", "diff.external=", "-c", "diff.ignoreSubmodules=all", "-c", "core.quotePath=false"}
	// No scope flag: it would turn includes off, and a driver can come from an included file or config.worktree.
	keys, err := g.List(ctx, "config", "--name-only", "-z", "--get-regexp", `^(filter|diff)\..*\.`)
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return nil, err // fail closed: an unlisted driver could run
	}
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
	return g, nil
}

// The prefixes are explicit so diff.noprefix and diff.mnemonicPrefix in the checkout's config cannot change the path headers the client reads.
var safeDiffFlags = []string{"--no-ext-diff", "--no-textconv", "--ignore-submodules=all", "--no-color", "--src-prefix=a/", "--dst-prefix=b/"}

func (g *Git) Cmd(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{"--no-pager"}, g.neutral...)
	full = append(full, args[0])
	rest := args[1:]
	if args[0] == "diff" {
		full = append(full, safeDiffFlags...)
	}
	full = append(full, rest...)
	c := exec.CommandContext(ctx, g.Bin, full...)
	c.Dir, c.Env = g.Dir, g.Env
	c.WaitDelay = time.Second
	return c
}

// List runs git and returns its whole output (at most maxListOut). git diff --no-index exits 1 on a difference.
func (g *Git) List(ctx context.Context, args ...string) ([]byte, error) {
	out, cut, err := g.Capped(ctx, maxListOut, args...)
	if cut && err == nil {
		return out, errors.New("git output too large")
	}
	return out, err
}

// Capped reads at most limit bytes of the output, stops git when there is more, and cuts at a line end.
func (g *Git) Capped(ctx context.Context, limit int, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, callTime)
	defer cancel()
	c := g.Cmd(ctx, args...)
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
		} else {
			buf = buf[:0]
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
