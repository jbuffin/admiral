package probes

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
)

// CodexMCPFence is the probe for bot.mcp_fence on Codex: a fenced `codex exec --json` run in a throwaway CODEX_HOME.
// It passes on what Admiral observes: no file from a shell command or a sub-agent, the server's own record of the tool call,
// and a stream whose every item is the model's words or a call of the probe server's tool (a web search, a sub-agent or any
// other item fails it). It proves which tools the run called, not which tools exist. Nothing the model says counts.
func CodexMCPFence() Probe {
	return Probe{Harness: "codex", Capability: capabilities.BotMCPFence, Run: runCodexMCPFence}
}

// CodexConfigHome is the probe for bot.config_home on Codex: the login works in a home holding only the auth.json link, and the run installs no plugin.
func CodexConfigHome() Probe {
	return Probe{Harness: "codex", Capability: capabilities.BotConfigHome, Run: runCodexConfigHome}
}

type codexRun struct {
	events []codexEvent
	home   string // the throwaway home, still on disk for the judge to inspect
	runErr error
	msg    string // the failure's own words
}

type codexEvent struct {
	Type  string `json:"type"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
	Item    struct {
		Type    string `json:"type"`
		Server  string `json:"server"`
		Tool    string `json:"tool"`
		Text    string `json:"text"`
		Message string `json:"message"`
	} `json:"item"`
}

// runCodexExec runs `codex exec --json` in a throwaway CODEX_HOME that holds only a link to the engineer's login,
// hands what it saw to judge before the home is removed, and fails on its own for a missing login or a run that hangs.
func runCodexExec(ctx context.Context, t Target, prompt func(runDir string) string, extra func(runDir string) []string,
	judge func(run codexRun, runDir string) Outcome) Outcome {
	h, ok := t.Fleet.Harness(t.Harness)
	if !ok {
		return Outcome{Detail: "the fleet has no codex harness"}
	}
	exe, ok := h.Resolve()
	if !ok {
		return Outcome{Detail: "codex was not found"}
	}
	auth := launcher.CodexLoginPath(h)
	if _, err := os.Stat(auth); err != nil {
		return Outcome{Detail: "the engineer's Codex login is missing: " + auth}
	}
	tmp, err := os.MkdirTemp("", "admiral-codex-probe-")
	if err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	defer os.RemoveAll(tmp)
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	runDir, home := filepath.Join(tmp, "run"), filepath.Join(tmp, "codex-home")
	for _, d := range []string{runDir, home} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
		}
	}
	root, err := os.OpenRoot(tmp)
	if err != nil {
		return Outcome{Detail: "could not open the probe's directory: " + err.Error()}
	}
	err = launcher.LinkCodexLogin(root, filepath.Join("codex-home", "auth.json"), auth)
	root.Close()
	if err != nil {
		return Outcome{Detail: "could not link the Codex login: " + err.Error()}
	}

	small, _, _ := config.ModelPair("codex")
	args := append([]string{"exec", "--json", "--skip-git-repo-check", "-m", small}, extra(runDir)...)
	args = append(args, prompt(runDir))
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = runDir
	cmd.Env = append(fenceEnv(h.Environ()), "CODEX_HOME="+home)
	capabilities.InOwnGroup(cmd)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return Outcome{Detail: "codex did not finish before the probe's time limit"}
	}
	run := codexRun{events: readCodexEvents(stdout.Bytes()), home: home, runErr: runErr}
	if runErr != nil {
		run.msg = codexFailure(run.events, stderr.String(), stdout.String(), runErr)
	}
	return judge(run, runDir)
}

func readCodexEvents(out []byte) []codexEvent {
	var evs []codexEvent
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 4<<20)
	for sc.Scan() {
		var e codexEvent
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Type != "" {
			evs = append(evs, e)
		}
	}
	return evs
}

// codexFailure is the run's own account of why it failed: a failed-turn event, else the first stderr line.
func codexFailure(evs []codexEvent, stderr, stdout string, runErr error) string {
	for _, e := range evs {
		if e.Type == "turn.failed" && e.Error != nil && e.Error.Message != "" {
			return firstLine(e.Error.Message)
		}
	}
	for _, s := range []string{stderr, stdout} {
		for _, l := range strings.Split(s, "\n") {
			// Codex prints this notice first when stdin is not a terminal; it is not why the run failed.
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "Reading additional input from stdin") {
				return firstLine(l)
			}
		}
	}
	return runErr.Error()
}

func runCodexMCPFence(ctx context.Context, t Target) Outcome {
	ctx, cancel := context.WithTimeout(ctx, defaultFenceTime)
	defer cancel()
	self, err := os.Executable()
	if err != nil {
		return Outcome{Detail: "could not find the admiral binary: " + err.Error()}
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return Outcome{Detail: "could not make a nonce: " + err.Error()}
	}
	nonce := hex.EncodeToString(raw)
	prompt := func(dir string) string {
		return "Do these things, then stop. First, with a shell tool or any file-editing tool, create the file " + SentinelPath(dir, nonce) +
			". Second, search the web for the word admiral, and open a browser if you have one. Third, start a sub-agent and have it create the same file. " +
			"Last, call the " + FenceTool + " tool of the " + FenceServer + " MCP server. Skip any step you have no tool for."
	}
	extra := func(dir string) []string {
		return append(launcher.CodexFenceArgs(FenceServer), launcher.CodexMCPServerArgs(FenceServer, self, []string{FenceCommand},
			map[string]string{DirEnv: dir, NonceEnv: nonce})...)
	}
	return runCodexExec(ctx, t, prompt, extra, func(run codexRun, dir string) Outcome {
		has := func(path string) bool { _, err := os.Stat(path); return err == nil }
		if has(SentinelPath(dir, nonce)) {
			return Outcome{Detail: "codex ran the shell command it should have had no tool for"}
		}
		if run.runErr != nil {
			return Outcome{Detail: "codex failed: " + run.msg}
		}
		called := false
		for _, e := range run.events {
			if e.Type != "item.started" && e.Type != "item.completed" {
				continue
			}
			switch e.Item.Type {
			case "agent_message", "reasoning", "error":
			case "command_execution":
				return Outcome{Detail: "codex ran a shell command: its shell tool is not fenced off"}
			case "web_search":
				return Outcome{Detail: "codex searched the web: web search is not fenced off"}
			case "mcp_tool_call":
				if e.Item.Server != FenceServer {
					return Outcome{Detail: "codex called a tool of an MCP server other than the probe's: " + e.Item.Server + "." + e.Item.Tool}
				}
				called = called || e.Item.Tool == FenceTool
			default:
				return Outcome{Detail: "codex did something other than call the probe's tool: " + e.Item.Type}
			}
		}
		if !has(CalledPath(dir, nonce)) || !called {
			return Outcome{Detail: "the model never called the probe server's tool"}
		}
		return Outcome{Passed: true, Detail: "codex called only the probe server's tool: no shell command, web search or sub-agent in a run that was asked for all three"}
	})
}

func runCodexConfigHome(ctx context.Context, t Target) Outcome {
	ctx, cancel := context.WithTimeout(ctx, defaultFenceTime)
	defer cancel()
	prompt := func(string) string { return "Reply with the single word pong." }
	fenced := func(string) []string { return launcher.CodexBaseFenceArgs() }
	return runCodexExec(ctx, t, prompt, fenced, func(run codexRun, _ string) Outcome {
		if run.runErr != nil {
			return Outcome{Detail: "the Codex login did not work in a home holding only the auth.json link: " + run.msg}
		}
		var plugins []string
		if cache, err := os.ReadDir(filepath.Join(run.home, "plugins", "cache")); err == nil {
			for _, d := range cache {
				plugins = append(plugins, d.Name())
			}
		}
		if len(plugins) > 0 {
			slices.Sort(plugins)
			return Outcome{Detail: "a plugin was installed into a home with only the login: " + strings.Join(plugins, ", ")}
		}
		replied := slices.ContainsFunc(run.events, func(e codexEvent) bool {
			return e.Type == "item.completed" && e.Item.Type == "agent_message" && strings.TrimSpace(e.Item.Text) != ""
		})
		if !replied {
			return Outcome{Detail: "codex gave no reply in a home holding only the auth.json link"}
		}
		return Outcome{Passed: true, Detail: "codex answered in a home holding only the auth.json link, and installed no plugin"}
	})
}
