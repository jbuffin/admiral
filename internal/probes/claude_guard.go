package probes

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/launcher"
)

const defaultGuardTime = 2 * time.Minute

// ClaudeGuard is the probe for autopilot.guard_hook on Claude Code. It proves two things
// Admiral observes itself: the guard hook got the model's tool call (its denial record for
// this run's nonce), and the harness then did not run the call (no sentinel file). Nothing
// the model says counts.
func ClaudeGuard() Probe {
	return Probe{Harness: "claude", Capability: capabilities.AutopilotGuard, Run: runClaudeGuard}
}

func runClaudeGuard(ctx context.Context, t Target) Outcome {
	// The judge probes run after it in the same run and need their share of its limit.
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

	// The bundle and the binary link live beside the run directory, never in the home:
	// the probe runs before any server has started and must not change what one runs.
	root, err := os.MkdirTemp("", "admiral-probe-")
	if err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	defer os.RemoveAll(root)
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	runDir, bundle := filepath.Join(root, "run"), filepath.Join(root, "bundle")
	if err := os.Mkdir(runDir, 0o700); err != nil {
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
	nonce := hex.EncodeToString(raw) // 16 lowercase hex, what ValidEnv accepts
	command := Command(runDir, nonce)
	prompt := "Use the Bash tool to run exactly this command, then stop: " + command

	cmd := exec.CommandContext(ctx, exe, "--settings", launcher.ClaudeSettingsPath(bundle),
		"--model", "haiku", "--allowedTools", "Bash", "-p", prompt)
	cmd.Dir = runDir
	cmd.Env = probeEnv(h.Environ(), filepath.Join(bundle, "bin"), runDir, nonce)
	capabilities.InOwnGroup(cmd)
	cmd.WaitDelay = time.Second // a descendant holding the pipe open must not outlive the timeout
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	if ctx.Err() != nil {
		// A cancelled run is dropped by the runner; only the time limit is a result.
		return Outcome{Detail: "claude did not finish before the probe's time limit"}
	}
	has := func(path string) bool { _, err := os.Stat(path); return err == nil }
	denied, seen, ran := has(DeniedPath(runDir, nonce)), has(SeenPath(runDir, nonce)), has(SentinelPath(runDir, nonce))
	switch {
	case denied && !ran:
		return Outcome{Passed: true, Detail: "the guard hook denied a Bash call and claude did not run it"}
	case denied:
		return Outcome{Detail: "claude ran the call after the guard hook denied it"}
	case ran && seen:
		return Outcome{Detail: "the model called Bash with a different command than the one asked for"}
	case ran:
		return Outcome{Detail: "claude ran the call without running the guard hook"}
	case seen:
		// The guard saw a Bash call that is neither the probe's command nor on disk.
		return Outcome{Detail: "the model called Bash with a different command than the one asked for"}
	case runErr != nil:
		// Claude prints some failures (not logged in) on stdout and leaves stderr empty.
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = firstLine(stdout.String())
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return Outcome{Detail: "claude failed: " + msg}
	}
	return Outcome{Detail: "the model never called the Bash tool"}
}

// probeEnv is the fleet's env without the ship and pane variables, then Admiral's own on
// top, the way a launch applies them.
func probeEnv(base []string, bin, dir, nonce string) []string {
	env := map[string]string{}
	for _, kv := range base {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for _, k := range capabilities.ScrubbedEnv {
		delete(env, k)
	}
	path := bin
	if p := env["PATH"]; p != "" {
		path += string(os.PathListSeparator) + p
	}
	env["PATH"] = path
	env["ADMIRAL_AUTOPILOT"] = "1"
	env[NonceEnv], env[DirEnv] = nonce, dir
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return fmt.Sprintf("%.120s", l)
		}
	}
	return ""
}
