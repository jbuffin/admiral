package capabilities

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// DetectVersion runs the harness's executable with --version under the fleet's env for
// that harness and returns the trimmed output. A missing executable, a nonzero exit, a
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
	cmd.WaitDelay = time.Second // a grandchild holding the pipe open must not outlive the timeout
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("%s --version: %w", harness, ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", harness, err)
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", errors.New(harness + " --version printed nothing")
	}
	return v, nil
}
