// Package gitleaks runs the engineer's own gitleaks on bytes Admiral is about to publish.
package gitleaks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
	installDocs    = "https://github.com/gitleaks/gitleaks#installing"
)

// minVersion is the first gitleaks whose stdin scan and exit codes this runner relies on.
var minVersion = [3]int{8, 19, 0}

const (
	Clean  = "clean"
	Hit    = "hit"
	Failed = "failed"
	// Missing and TooOld mean no scan ran.
	Missing = "missing"
	TooOld  = "too-old"
)

type Finding struct {
	Line int    `json:"line"`
	Rule string `json:"rule"`
}

// Result is the design's scan object.
type Result struct {
	State    string    `json:"state"`
	Findings []Finding `json:"findings"`
	Error    string    `json:"error"`
	Version  string    `json:"version"`
	Install  string    `json:"install"`
}

// Runner finds and runs gitleaks. The zero value of Timeout, LookPath and Command means 30s, a PATH search of Env and exec.
type Runner struct {
	Env      []string // NAME=value, the fleet's environment, PATH included
	Timeout  time.Duration
	LookPath func(file string, env []string) (string, error)
	Command  func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// Scan checks data and never returns the text of a secret.
func (r Runner) Scan(ctx context.Context, data []byte) Result {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if err := ctx.Err(); err != nil {
		return failed("", err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	env := scrubbed(r.Env)
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = searchPath
	}
	bin, err := lookPath("gitleaks", r.Env)
	if err != nil {
		return Result{State: Missing, Findings: []Finding{}, Install: "brew install gitleaks (" + installDocs + ")"}
	}

	dir, err := os.MkdirTemp("", "admiral-gitleaks-")
	if err != nil {
		return failed("", err.Error())
	}
	defer os.RemoveAll(dir)

	version, err := r.version(ctx, bin, dir, env, timeout)
	if err != nil {
		return failed(version, err.Error())
	}
	if older(version) {
		return Result{State: TooOld, Findings: []Finding{}, Version: version, Install: "brew upgrade gitleaks (" + installDocs + ")"}
	}

	args := []string{"stdin", "--no-banner", "--redact", "--log-level", "error", "--report-format", "json", "--report-path", "-", "--exit-code", "3"}
	out, stderr, err := r.run(ctx, bin, dir, env, data, args)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return Result{State: Clean, Findings: []Finding{}, Version: version}
	case errors.As(err, &exit) && exit.ExitCode() == 3:
		var report []struct {
			RuleID    string
			StartLine int
		}
		if json.Unmarshal(out, &report) != nil || len(report) == 0 {
			return failed(version, "gitleaks reported a leak but its report couldn't be read")
		}
		findings := make([]Finding, len(report))
		for i, f := range report {
			findings[i] = Finding{Line: f.StartLine, Rule: f.RuleID}
		}
		return Result{State: Hit, Findings: findings, Version: version}
	default:
		return failed(version, failure(ctx, err, stderr, timeout))
	}
}

func (r Runner) version(ctx context.Context, bin, dir string, env []string, timeout time.Duration) (string, error) {
	out, stderr, err := r.run(ctx, bin, dir, env, nil, []string{"version"})
	if err != nil {
		return "", errors.New(failure(ctx, err, stderr, timeout))
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	if _, ok := parseVersion(version); !ok {
		return "", errors.New("can't read the gitleaks version from " + strconv.Quote(version))
	}
	return version, nil
}

func (r Runner) run(ctx context.Context, bin, dir string, env []string, stdin []byte, args []string) (stdout, stderr []byte, err error) {
	command := r.Command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(ctx, bin, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	// A killed gitleaks may leave a child holding the pipes; don't wait on them forever.
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

func failure(ctx context.Context, err error, stderr []byte, timeout time.Duration) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timed out after " + timeout.String()
	}
	if line, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n"); line != "" {
		return line
	}
	return err.Error()
}

func failed(version, msg string) Result {
	return Result{State: Failed, Findings: []Finding{}, Error: msg, Version: version}
}

func scrubbed(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GITLEAKS_CONFIG=") || strings.HasPrefix(kv, "GITLEAKS_CONFIG_TOML=") {
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}

// searchPath finds file on the PATH in env, not the process's own.
func searchPath(file string, env []string) (string, error) {
	for _, kv := range env {
		list, ok := strings.CutPrefix(kv, "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(list) {
			if dir == "" {
				continue
			}
			p := filepath.Join(dir, file)
			if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				return p, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func older(version string) bool {
	have, _ := parseVersion(version)
	for i := range have {
		if have[i] != minVersion[i] {
			return have[i] < minVersion[i]
		}
	}
	return false
}
