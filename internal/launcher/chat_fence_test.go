package launcher

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// chatRun is one chat start against a real tmux and a harness that records its argv and env.
type chatRun struct {
	l    *Launcher
	tmux string
	sock string
	rec  string
	home string
}

func newChatRun(t *testing.T) *chatRun {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	tmp, err := os.MkdirTemp("", "ad")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(tmp, "t.sock")
	t.Cleanup(func() {
		exec.Command(tmux, "-S", sock, "kill-server").Run()
		os.RemoveAll(tmp)
	})
	rec := filepath.Join(tmp, "rec")
	exe := filepath.Join(tmp, "harness")
	script := "#!/bin/sh\nrm -rf " + rec + "\nmkdir -p " + rec + ".part\nenv >" + rec + ".part/env\n" +
		"printf '%s\\0' \"$@\" >" + rec + ".part/argv\nmv " + rec + ".part " + rec + "\nsleep 30\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{t.TempDir()},
		Chat: config.ChatSettings{Harness: "claude", Model: "opus"},
		Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true,
			Args: []string{"--allowedTools", "Bash(git *)", "--mcp-config", "/elsewhere/mcp.json"}}}}}}
	l := &Launcher{Config: func() *config.Config { return cfg }, Home: t.TempDir(), Tmux: tmux, Socket: sock, Port: "4242"}
	installPassThroughWrapper(t, l.Home)
	if err := WriteBundle(l.Home); err != nil {
		t.Fatal(err)
	}
	return &chatRun{l: l, tmux: tmux, sock: sock, rec: rec, home: l.Home}
}

// start starts the chat and returns what the harness recorded.
func (c *chatRun) start(t *testing.T) (argv []string, env string) {
	t.Helper()
	if err := c.l.StartChat("work"); err != nil {
		t.Fatalf("StartChat: %v", err)
	}
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		a, aerr := os.ReadFile(filepath.Join(c.rec, "argv"))
		e, eerr := os.ReadFile(filepath.Join(c.rec, "env"))
		if aerr == nil && eerr == nil {
			return strings.Split(strings.TrimSuffix(string(a), "\x00"), "\x00"), string(e)
		}
		if time.Now().After(end) {
			t.Fatal("the chat's harness never started")
		}
	}
}

func (c *chatRun) stop(t *testing.T) {
	t.Helper()
	if out, err := exec.Command(c.tmux, "-S", c.sock, "kill-session", "-t", "="+sessionhost.ChatSession("work")+":").CombinedOutput(); err != nil {
		t.Fatalf("kill-session: %v: %s", err, out)
	}
	if err := os.RemoveAll(c.rec); err != nil {
		t.Fatal(err)
	}
}

func (c *chatRun) tmuxOut(t *testing.T, args ...string) string {
	t.Helper()
	out, _ := exec.Command(c.tmux, append([]string{"-S", c.sock}, args...)...).CombinedOutput()
	return string(out)
}

func (c *chatRun) mcpPath() string { return filepath.Join(ChatDir(c.home, "work"), "mcp.json") }

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func (c *chatRun) mcpConfig(t *testing.T) map[string]mcpServer {
	t.Helper()
	raw, err := os.ReadFile(c.mcpPath())
	if err != nil {
		t.Fatalf("the chat's MCP config: %v", err)
	}
	var cfg struct {
		Servers map[string]mcpServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("%s is not an MCP config: %v\n%s", c.mcpPath(), err, raw)
	}
	return cfg.Servers
}

func TestChatRunsWithNoBuiltInToolsAndOnlyTheMCPConfig(t *testing.T) {
	c := newChatRun(t)
	argv, _ := c.start(t)

	if i := slices.Index(argv, "--tools"); i < 0 || i+1 >= len(argv) || argv[i+1] != "" {
		t.Errorf("argv = %q, want --tools followed by an empty element: no built-in tool at all", argv)
	}
	if !slices.Contains(argv, "--strict-mcp-config") {
		t.Errorf("argv = %q, want --strict-mcp-config so the engineer's own MCP servers stay out", argv)
	}
	var configs []string
	for i, a := range argv {
		if a == "--mcp-config" && i+1 < len(argv) {
			configs = append(configs, argv[i+1])
		}
	}
	if want := c.mcpPath(); !slices.Equal(configs, []string{want}) {
		t.Errorf("--mcp-config values = %q, want only %q (the dock's own were dropped)", configs, want)
	}
	if v, _ := flagValue(argv, "--permission-mode"); v != "dontAsk" {
		t.Errorf("--permission-mode = %q, want dontAsk", v)
	}
	if v, _ := flagValue(argv, "--setting-sources"); v != "project" {
		t.Errorf("--setting-sources = %q, want project", v)
	}
	for _, a := range argv {
		if strings.Contains(a, "Bash") || strings.Contains(a, "--allowedTools") {
			t.Errorf("argv = %q still holds %q: the chat takes neither the old tool list nor the dock's allow rules", argv, a)
		}
	}
}

func TestChatSettingsAllowOnlyTheAdmiralServerAndKeepTheDenyBackstop(t *testing.T) {
	c := newChatRun(t)
	argv, _ := c.start(t)
	path, ok := flagValue(argv, "--settings")
	if !ok {
		t.Fatalf("argv = %q has no --settings", argv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if want := []string{"mcp__admiral__*"}; !slices.Equal(s.Permissions.Allow, want) {
		t.Errorf("permissions.allow = %q, want exactly %q: no CLI allow rule, no Skill", s.Permissions.Allow, want)
	}
	deny := slices.Clone(s.Permissions.Deny)
	slices.Sort(deny)
	if want := []string{"Edit", "NotebookEdit", "Write"}; !slices.Equal(deny, want) {
		t.Errorf("permissions.deny = %q, want %q kept as a backstop", deny, want)
	}
}

func TestChatMCPConfigNamesOnlyAdmiralMCPWithThisExecutableAndTheChatsEnv(t *testing.T) {
	c := newChatRun(t)
	c.start(t)

	fi, err := os.Stat(c.mcpPath())
	if err != nil {
		t.Fatalf("the MCP config: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mcp.json mode = %v, want 0600: it holds the actor token", fi.Mode().Perm())
	}
	if di, _ := os.Stat(filepath.Dir(c.mcpPath())); di == nil || di.Mode().Perm() != 0o700 {
		t.Errorf("the chat directory is not 0700")
	}

	servers := c.mcpConfig(t)
	if len(servers) != 1 {
		t.Fatalf("servers = %v, want only admiral", servers)
	}
	srv, ok := servers["admiral"]
	if !ok {
		t.Fatalf("servers = %v, want one named admiral (the allow rule is mcp__admiral__*)", servers)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if srv.Command != self {
		t.Errorf("command = %q, want this Admiral's own executable %q, never a PATH lookup", srv.Command, self)
	}
	if !slices.Equal(srv.Args, []string{"mcp"}) {
		t.Errorf("args = %q, want [mcp]", srv.Args)
	}

	nonce := strings.TrimSpace(c.tmuxOut(t, "show-options", "-t", "="+sessionhost.ChatSession("work")+":", "-qv", sessionhost.Prefix+sessionhost.ChatNonceOpt))
	m, err := actor.NewMinter(c.home)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ADMIRAL_ACTOR_TOKEN": m.Token("work", nonce), "ADMIRAL_PORT": "4242", "ADMIRAL_HOME": c.home, "ADMIRAL_CHAT_FLEET": "work"}
	if nonce == "" {
		t.Fatal("the chat session has no nonce option to derive the token from")
	}
	for k, v := range want {
		if srv.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, srv.Env[k], v)
		}
	}
	if len(srv.Env) != len(want) {
		t.Errorf("env = %v, want exactly the four keys %v", srv.Env, want)
	}
}

func TestChatTokenReachesOnlyTheMCPConfig(t *testing.T) {
	c := newChatRun(t)
	argv, env := c.start(t)
	token := c.mcpConfig(t)["admiral"].Env["ADMIRAL_ACTOR_TOKEN"]
	if token == "" {
		t.Fatal("the MCP config carries no ADMIRAL_ACTOR_TOKEN")
	}
	for _, want := range []string{"ADMIRAL_CHAT_FLEET=work", "ADMIRAL_HOME=" + c.home, "ADMIRAL_PORT=4242"} {
		if !strings.Contains("\n"+env, "\n"+want+"\n") {
			t.Errorf("the chat's env lacks %s: the hooks and status line still read it", want)
		}
	}
	settings, _ := flagValue(argv, "--settings")
	settingsRaw, _ := os.ReadFile(settings)
	places := map[string]string{
		"argv":                  strings.Join(argv, "\x00"),
		"the chat's env":        env,
		"the settings file":     string(settingsRaw),
		"tmux session env":      c.tmuxOut(t, "show-environment", "-t", "="+sessionhost.ChatSession("work")+":"),
		"tmux global env":       c.tmuxOut(t, "show-environment", "-g"),
		"tmux session options":  c.tmuxOut(t, "show-options", "-t", "="+sessionhost.ChatSession("work")+":"),
		"tmux global options":   c.tmuxOut(t, "show-options", "-g"),
		"tmux window options":   c.tmuxOut(t, "show-options", "-w", "-t", "="+sessionhost.ChatSession("work")+":"),
		"tmux server options":   c.tmuxOut(t, "show-options", "-s"),
		"the chat's own env":    c.tmuxOut(t, "show-environment"),
		"the pane's start cmd":  c.tmuxOut(t, "display-message", "-p", "-t", "="+sessionhost.ChatSession("work")+":", "#{pane_start_command}"),
		"the pane's current":    c.tmuxOut(t, "display-message", "-p", "-t", "="+sessionhost.ChatSession("work")+":", "#{pane_current_command}"),
		"the session's options": c.tmuxOut(t, "show-options", "-A", "-t", "="+sessionhost.ChatSession("work")+":"),
	}
	for where, text := range places {
		if strings.Contains(text, token) {
			t.Errorf("the actor token is in %s", where)
		}
	}
}

func TestEachChatStartWritesAConfigWithTheNewSessionsToken(t *testing.T) {
	c := newChatRun(t)
	c.start(t)
	first := c.mcpConfig(t)["admiral"].Env["ADMIRAL_ACTOR_TOKEN"]
	c.stop(t)
	c.start(t)
	second := c.mcpConfig(t)["admiral"].Env["ADMIRAL_ACTOR_TOKEN"]
	if first == "" || second == "" {
		t.Fatalf("tokens = %q, %q, want one in each start's config", first, second)
	}
	if first == second {
		t.Error("a new chat session got the old session's token: the config was not rewritten")
	}
	entries, _ := os.ReadDir(filepath.Dir(c.mcpPath()))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("%s was left in the chat directory", e.Name())
		}
	}
}

func TestChatPromptTeachesTheToolsAndNoCLI(t *testing.T) {
	c := newChatRun(t)
	argv, _ := c.start(t)
	prompt, ok := flagValue(argv, "--append-system-prompt")
	if !ok {
		t.Fatalf("argv = %q has no --append-system-prompt", argv)
	}
	text := prompt
	// A skill the fence's empty tool list can't load must not be where the guidance lives.
	_ = filepath.WalkDir(filepath.Join(ChatDir(c.home, "work"), ".claude"), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if b, rerr := os.ReadFile(p); rerr == nil {
				text += "\n" + string(b)
			}
		}
		return nil
	})
	for _, banned := range []string{"admiral orders", "admiral chat ask", "admiral ships", "admiral inbox", "admiral outcomes", "Bash"} {
		if strings.Contains(text, banned) {
			t.Errorf("the chat's guidance still teaches %q", banned)
		}
	}
	for _, want := range []string{"launch_ship", "stop_ship", "ask_question", "autopilot", "with me"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the appended prompt does not mention %q: the skill can't load under --tools \"\", so the prompt carries it", want)
		}
	}
}

func TestEachChatStartRemovesPlantedProjectSettingsWithoutFollowingLinks(t *testing.T) {
	c := newChatRun(t)
	dir := ChatDir(c.home, "work")
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	planted := []string{filepath.Join(dir, ".claude", "settings.local.json"), filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, ".claude", "CLAUDE.md")}
	for _, p := range planted {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, ".claude", "settings.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	c.start(t)
	for _, p := range append(planted, link) {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s survived a chat start", p)
		}
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "{}" {
		t.Errorf("the file a planted link pointed at is %q (%v), want it untouched", b, err)
	}
}

func TestChatPromptLeavesTheLaunchKeyToAdmiral(t *testing.T) {
	c := newChatRun(t)
	argv, _ := c.start(t)
	prompt, _ := flagValue(argv, "--append-system-prompt")
	if strings.Contains(prompt, "chat_message") {
		t.Error("the prompt still tells the model to pass a chat message id it never sees")
	}
	if !strings.Contains(prompt, "pass anyway on the second, and only then") {
		t.Error("the prompt does not say that two different freeform ships from one message need anyway")
	}
}

func TestChatStartIsRefusedWhenItsClaudeDirectoryIsALinkOutOfTheHome(t *testing.T) {
	c := newChatRun(t)
	dir := ChatDir(c.home, "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := c.l.StartChat("work"); err == nil {
		t.Fatal("the chat started with a .claude link that Claude Code would read settings through")
	}
	if _, err := os.Stat(filepath.Join(outside, "settings.json")); err != nil {
		t.Errorf("the linked directory's file is gone: %v", err)
	}
}
