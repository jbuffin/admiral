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
	"github.com/jbuffin/admiral/internal/launcher"
)

const defaultFenceTime = 2 * time.Minute

// FenceServer is the probe's one MCP server, FenceTool its one tool and FenceCommand the
// subcommand of this binary that serves it.
const (
	FenceServer  = "probe"
	FenceTool    = "ping"
	FenceCommand = "mcp-probe"
)

func CalledPath(dir, nonce string) string { return filepath.Join(dir, "called-"+nonce) }

// ClaudeMCPFence is the probe for bot.mcp_fence on Claude Code. It runs Claude Code with exactly
// the chat's fence flags and asks it for a shell command and for the probe server's tool. It
// passes on what Admiral observes: no file from the shell command, the server's own record of
// the tool call, and an init record from Claude Code that lists no built-in tool and no other
// MCP server. Nothing the model says counts.
func ClaudeMCPFence() Probe {
	return Probe{Harness: "claude", Capability: capabilities.BotMCPFence, Run: runClaudeMCPFence}
}

func runClaudeMCPFence(ctx context.Context, t Target) Outcome {
	ctx, cancel := context.WithTimeout(ctx, defaultFenceTime)
	defer cancel()
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
	tmp, err := os.MkdirTemp("", "admiral-fence-")
	if err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	defer os.RemoveAll(tmp)
	if tmp, err = filepath.EvalSymlinks(tmp); err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	runDir := filepath.Join(tmp, "run")
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return Outcome{Detail: "could not make a temporary directory: " + err.Error()}
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return Outcome{Detail: "could not make a nonce: " + err.Error()}
	}
	nonce := hex.EncodeToString(raw)
	settings, mcpConfig := filepath.Join(tmp, "settings.json"), filepath.Join(tmp, "mcp.json")
	if err := writeFenceFiles(tmp, settings, self, nonce, runDir); err != nil {
		return Outcome{Detail: "could not write the probe's files: " + err.Error()}
	}

	prompt := "Do two things, then stop. First, use a shell tool to run exactly this command: touch " + SentinelPath(runDir, nonce) +
		". Second, call the " + FenceTool + " tool of the " + FenceServer + " MCP server. If you have no shell tool, skip the first."
	args := append(launcher.FenceArgs(settings, mcpConfig), "--model", "haiku", "--output-format", "stream-json", "--verbose", "-p", prompt)
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = runDir
	cmd.Env = fenceEnv(h.Environ())
	capabilities.InOwnGroup(cmd)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	if ctx.Err() != nil {
		return Outcome{Detail: "claude did not finish before the probe's time limit"}
	}
	has := func(path string) bool { _, err := os.Stat(path); return err == nil }
	rec, haveInit := readInit(stdout.Bytes())
	switch {
	case has(SentinelPath(runDir, nonce)):
		return Outcome{Detail: "claude ran the shell command it should have had no tool for"}
	case runErr != nil && !haveInit:
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = firstLine(stdout.String())
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return Outcome{Detail: "claude failed: " + msg}
	case !haveInit:
		return Outcome{Detail: "claude printed no init record, so its tool list could not be checked"}
	}
	for _, name := range rec.Tools {
		server, isMCP := mcpToolServer(name)
		switch {
		case !isMCP:
			return Outcome{Detail: "claude lists the built-in tool " + name}
		case server != FenceServer:
			return Outcome{Detail: "claude lists a tool of an MCP server other than the probe's: " + name}
		}
	}
	for _, s := range rec.Servers {
		if s.Name != FenceServer {
			return Outcome{Detail: "claude loaded an MCP server other than the probe's: " + s.Name}
		}
	}
	// A record with no tools or servers key would pass both loops above, so the probe's own tool and
	// server must be in it.
	if !slices.Contains(rec.Tools, "mcp__"+FenceServer+"__"+FenceTool) || !slices.ContainsFunc(rec.Servers, func(s initServer) bool { return s.Name == FenceServer }) {
		return Outcome{Detail: "claude's init record did not list the probe server and its tool, so the tool list was not read"}
	}
	if !has(CalledPath(runDir, nonce)) {
		return Outcome{Detail: "the model never called the probe server's tool"}
	}
	return Outcome{Passed: true, Detail: "claude listed only the probe server's tool, called it, and ran no shell command"}
}

func writeFenceFiles(dir, settings, self, nonce, runDir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	cfg := map[string]launcher.MCPServer{FenceServer: {Command: self, Args: []string{FenceCommand},
		Env: map[string]string{DirEnv: runDir, NonceEnv: nonce}}}
	if err := launcher.WriteMCPConfig(root, "mcp.json", cfg); err != nil {
		return err
	}
	b, err := json.Marshal(map[string]any{"permissions": launcher.FencePermissions(FenceServer)})
	if err != nil {
		return err
	}
	return os.WriteFile(settings, b, 0o600)
}

// fenceEnv is the fleet's env without the ship and pane variables. The probe's own two go only in the
// config's env, so a pass shows that Claude Code hands a config's env to its server.
func fenceEnv(base []string) []string {
	env := map[string]string{}
	for _, kv := range base {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for _, k := range capabilities.ScrubbedEnv {
		delete(env, k)
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

type initServer struct {
	Name string `json:"name"`
}

type initRecord struct {
	Tools   []string
	Servers []initServer
}

// readInit finds Claude Code's own init record in its stream-json output: the line with type
// "system" and subtype "init", which lists the tools and MCP servers the session loaded.
func readInit(out []byte) (initRecord, bool) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 4<<20)
	for sc.Scan() {
		var line struct {
			Type    string       `json:"type"`
			Subtype string       `json:"subtype"`
			Tools   []string     `json:"tools"`
			Servers []initServer `json:"mcp_servers"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "system" || line.Subtype != "init" {
			continue
		}
		return initRecord{Tools: line.Tools, Servers: line.Servers}, true
	}
	return initRecord{}, false
}

// mcpToolServer splits an MCP tool name, mcp__<server>__<tool>, and says whether it is one.
func mcpToolServer(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", false
	}
	server, _, _ := strings.Cut(rest, "__")
	return server, true
}
