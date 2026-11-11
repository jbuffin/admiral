package probes

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/launcher"
)

// scrubbed are the variables that would tie the probe to a real ship or pane: the guard
// would record an outcome against the ship, and the bundle's hook would mark the pane.
var scrubbed = []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX", "ADMIRAL_LOW_STAKES",
	"ADMIRAL_AUTO_MERGE", "ADMIRAL_PORT", "TMUX", "TMUX_PANE"}

// ClaudeGuard is the probe for autopilot.guard_hook on Claude Code. It proves two things
// Admiral observes itself: the guard hook got the model's tool call (its denial record for
// this run's nonce), and the harness then did not run the call (no sentinel file). Nothing
// the model says counts.
func ClaudeGuard() Probe {
	return Probe{Harness: "claude", Capability: capabilities.AutopilotGuard, Run: runClaudeGuard}
}

func runClaudeGuard(ctx context.Context, t Target) Outcome {
	h, ok := t.Fleet.Harness(t.Harness)
	if !ok {
		return Outcome{Detail: "the fleet has no claude harness"}
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
	nonce := hex.EncodeToString(raw)
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
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Outcome{Detail: "claude did not finish before the probe's time limit"}
		}
		return Outcome{Detail: "the probe was cancelled"}
	}
	_, denied := os.Stat(DeniedPath(runDir, nonce))
	_, ran := os.Stat(SentinelPath(runDir, nonce))
	switch {
	case denied == nil && ran != nil:
		return Outcome{Passed: true, Detail: "the guard hook denied a Bash call and claude did not run it"}
	case denied == nil:
		return Outcome{Detail: "the guard hook denied the call but claude ran it anyway"}
	case ran == nil:
		return Outcome{Detail: "claude ran the call without running the guard hook"}
	case runErr != nil:
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return Outcome{Detail: "claude failed: " + msg}
	}
	// With no denial and no sentinel the model may not have called the tool, or the hook
	// may not have run; its own words are the only hint to which.
	return Outcome{Detail: "the guard hook never saw a Bash call and the call did not run; claude said: " + firstLine(stdout.String())}
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
	for _, k := range scrubbed {
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
	return "nothing"
}
