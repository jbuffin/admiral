package capabilities

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// DetectVersion runs the harness's executable with --version under the fleet's env for
// that harness and returns the first non-empty line of its output. A missing executable, a nonzero exit, a
// timeout or empty output is an error, never an empty version that could match a stored one.
func DetectVersion(ctx context.Context, fleet config.Fleet, harness string, timeout time.Duration) (string, error) {
	h, ok := fleet.Harness(harness)
	if !ok {
		return "", fmt.Errorf("fleet %s has no harness %s", fleet.Key, harness)
	}
	exe, ok := h.Resolve()
	if !ok {
		return "", fmt.Errorf("%s not found", harness)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--version")
	cmd.Env = h.Environ()
	InOwnGroup(cmd)
	cmd.WaitDelay = time.Second // a descendant holding the pipe open must not outlive the timeout
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s --version: %w", harness, ctx.Err())
	}
	if err != nil {
		if msg := firstLine(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s --version: %w: %s", harness, err, msg)
		}
		return "", fmt.Errorf("%s --version: %w", harness, err)
	}
	// The first line only, so an update notice after it doesn't change the version key.
	v := firstLine(stdout.String())
	if v == "" {
		return "", errors.New(harness + " --version printed nothing")
	}
	return v, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 200 {
				l = l[:200]
			}
			return l
		}
	}
	return ""
}
