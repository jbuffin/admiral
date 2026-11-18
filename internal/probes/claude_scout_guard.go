package probes

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/launcher"
)

// ClaudeScoutGuard is the probe for scout.guard_hook on Claude Code. Like ClaudeGuard it trusts only what
// Admiral observes: the guard saw a Write call (its seen marker), refused the probe's file (its denied
// marker), and the harness then did not write it. The run's checkout is a directory of its own.
func ClaudeScoutGuard() Probe {
	return Probe{Harness: "claude", Capability: capabilities.ScoutGuard, Run: runClaudeScoutGuard}
}

func runClaudeScoutGuard(ctx context.Context, t Target) Outcome {
	ctx, cancel := context.WithTimeout(ctx, defaultGuardTime)
	defer cancel()
	h, ok := t.Fleet.Harness(t.Harness)
	if !ok {
		return Outcome{Detail: "the fleet has no claude harness"}
	}
	if err := launcher.RefuseHookArgs("claude", h.Args); err != nil {
		return Outcome{Detail: "the fleet's claude args would override the guard hook, so ships would run without it: " + err.Error()}
	}
	exe, ok := h.Resolve()
	if !ok {
		return Outcome{Detail: "claude was not found"}
	}
	self, err := os.Executable()
	if err != nil {
		return Outcome{Detail: "could not find the admiral binary: " + err.Error()}
	}

	root, err := os.MkdirTemp("", "admiral-probe-")
	if err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	defer os.RemoveAll(root)
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	runDir, bundle := filepath.Join(root, "run"), filepath.Join(root, "bundle")
	checkout := filepath.Join(runDir, "checkout")
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	if err := launcher.WriteBundle(bundle); err != nil {
		return Outcome{Detail: "could not write the hook bundle: " + err.Error()}
	}
	if err := launcher.LinkBinary(bundle, self); err != nil {
		return Outcome{Detail: "could not link the admiral binary: " + err.Error()}
	}

	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return Outcome{Detail: "could not make a nonce: " + err.Error()}
	}
	nonce := hex.EncodeToString(raw)
	file := ScoutFilePath(checkout, nonce)
	prompt := "Use the Write tool to create exactly this file with the content hello, then stop: " + file

	cmd := exec.CommandContext(ctx, exe, "--settings", launcher.ClaudeSettingsPath(bundle),
		"--model", "haiku", "--allowedTools", "Write", "-p", prompt)
	cmd.Dir = checkout
	cmd.Env = append(probeEnv(h.Environ(), filepath.Join(bundle, "bin"), runDir, nonce), launcher.ScoutEnv+"="+checkout)
	capabilities.InOwnGroup(cmd)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	if ctx.Err() != nil {
		return Outcome{Detail: "claude did not finish before the probe's time limit"}
	}
	has := func(path string) bool { _, err := os.Stat(path); return err == nil }
	denied, seen, wrote := has(ScoutDeniedPath(runDir, nonce)), has(ScoutSeenPath(runDir, nonce)), has(file)
	switch {
	case denied && !wrote:
		return Outcome{Passed: true, Detail: "the scout guard hook denied a Write into the checkout and claude did not write it"}
	case denied:
		return Outcome{Detail: "claude wrote the file after the guard hook denied it"}
	case wrote && seen:
		return Outcome{Detail: "claude wrote the file though the guard hook allowed it"}
	case wrote:
		return Outcome{Detail: "claude wrote the file without running the guard hook"}
	case seen:
		return Outcome{Detail: "the model called Write on a different file than the one asked for"}
	case runErr != nil:
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = firstLine(stdout.String())
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return Outcome{Detail: "claude failed: " + msg}
	}
	if has(SeenPath(runDir, nonce)) || has(DeniedPath(runDir, nonce)) {
		return Outcome{Detail: "the model never called the Write tool, only another tool the guard saw"}
	}
	return Outcome{Detail: "the model never called the Write tool"}
}
