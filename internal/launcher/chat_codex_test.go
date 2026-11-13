package launcher

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The Codex chat: its own CODEX_HOME holding only the login link, the fence as -c/--disable flags,
// a ship's hook flags, and nothing of the engineer's Codex config.

// Placeholders the implementer confirms on the real Codex (one-line changes each).
var (
	// codexInstructionsKey is the config key that carries the chat prompt.
	codexInstructionsKey = "developer_instructions"
	// codexHomeAdmiralFiles are the files Admiral itself may write in the chat's codex-home besides auth.json.
	codexHomeAdmiralFiles []string
	// codexFence is every flag the chat's argv must carry, as config key and unquoted value: ADR 0023's three
	// settings, each default-on feature that adds a tool or loads plugins, and any extra key that keeps plugins and
	// connectors out (add rows once the real keys are found).
	codexFence = func() []struct{ key, want string } {
		out := []struct{ key, want string }{
			{"features.shell_tool", "false"},
			{"web_search", "disabled"},
			{"mcp_servers.admiral.default_tools_approval_mode", "approve"},
		}
		for _, f := range []string{"unified_exec", "apps", "plugins", "remote_plugin", "plugin_discovery_prefetch",
			"skill_mcp_dependency_install", "tool_suggest", "browser_use", "browser_use_external", "in_app_browser",
			"computer_use", "image_generation", "view_image", "multi_agent", "sleep_tool", "goals"} {
			out = append(out, struct{ key, want string }{"features." + f, "false"})
		}
		return out
	}()
)

const codexAuthText = "fake-codex-login"

type codexChat struct {
	*chatRun
	engineer string // the engineer's CODEX_HOME
}

func newCodexChatRun(t *testing.T) *codexChat {
	t.Helper()
	c := newChatRun(t)
	engineer := t.TempDir()
	// Neither the process nor the dock may fall back to the real ~/.codex.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", engineer)
	for rel, body := range map[string]string{
		"auth.json":                   codexAuthText,
		"config.toml":                 "[mcp_servers.engineer_only]\ncommand = \"/bin/false\"\n",
		"plugins/cache/p/plugin.txt":  "engineer plugin",
		"sessions/2026/rollout.jsonl": "{}\n",
	} {
		p := filepath.Join(engineer, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := c.l.Config()
	cfg.ChatProbe = func(fleet, harness, capability string) (string, string) { return "passed", "" }
	exe := cfg.Fleets[0].Harnesses[0].Executable
	cfg.Fleets[0].Chat = config.ChatSettings{Harness: "codex", Model: "gpt-6.1-sol", Effort: "high"}
	cfg.Fleets[0].Harnesses = []config.Harness{{Name: "codex", Executable: exe, Found: true,
		Args: []string{"--search", "--dangerously-bypass-approvals-and-sandbox", "-c", `mcp_servers.dock.command="/bin/dock-server"`},
		Env:  map[string]string{"CODEX_HOME": engineer, "ADMIRAL_SHIP": "stray-ship"}}}
	return &codexChat{chatRun: c, engineer: engineer}
}

func (c *codexChat) codexHome() string { return filepath.Join(ChatDir(c.home, "work"), "codex-home") }

// startOK starts the chat and fails the test unless it came up and its harness recorded an argv.
func (c *codexChat) startOK(t *testing.T) (argv []string, env string) {
	t.Helper()
	return c.start(t)
}

// codexConfigOf reads an argv's config the way Codex does: -c/--config key=value, --enable/--disable as
// features.<name>, and -m as model. Values are unquoted.
func codexConfigOf(argv []string) map[string]string {
	out := map[string]string{}
	unq := func(v string) string { return strings.Trim(v, `"`) }
	for i := 0; i+1 < len(argv); i++ {
		v := argv[i+1]
		switch argv[i] {
		case "-c", "--config":
			if k, val, ok := strings.Cut(v, "="); ok {
				out[strings.TrimSpace(k)] = unq(strings.TrimSpace(val))
			}
		case "--enable":
			out["features."+v] = "true"
		case "--disable":
			out["features."+v] = "false"
		case "-m", "--model":
			out["model"] = v
		}
	}
	return out
}

// chatFiles is every regular file under the chat directory, by path relative to it; links are skipped.
func chatFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(dir, p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

// runsBehindPaneOrWithoutDaemon is the one check that the chat's Codex is not left to join a shared app-server under
// the engineer's config: it runs behind codex-pane (which starts its own) or the TUI got --no-daemon.
func (c *codexChat) runsBehindPaneOrWithoutDaemon(t *testing.T, argv []string) bool {
	t.Helper()
	start := c.tmuxOut(t, "display-message", "-p", "-t", "="+sessionhost.ChatSession("work")+":", "#{pane_start_command}")
	return strings.Contains(start, "codex-pane") || slices.Contains(argv, "--no-daemon")
}

func TestCodexChatHomeHoldsOnlyTheAuthLinkAndIsTheChatsOwn(t *testing.T) {
	c := newCodexChatRun(t)
	c.startOK(t)

	want := filepath.Join(ChatDir(c.home, "work"), "codex-home")
	if got := ChatCodexHome(c.home, "work"); got != want {
		t.Errorf("ChatCodexHome = %q, want %q", got, want)
	}
	if fi, err := os.Stat(want); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("codex-home = %v, %v, want a 0700 directory", fi, err)
	}
	link := filepath.Join(want, "auth.json")
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("codex-home/auth.json = %v, %v, want a symlink to the engineer's login", fi, err)
	}
	if got, _ := os.Readlink(link); got != filepath.Join(c.engineer, "auth.json") {
		t.Errorf("auth.json points at %q, want %q", got, filepath.Join(c.engineer, "auth.json"))
	}
	allowed := append([]string{"auth.json"}, codexHomeAdmiralFiles...)
	entries, _ := os.ReadDir(want)
	for _, e := range entries {
		if !slices.Contains(allowed, e.Name()) {
			t.Errorf("codex-home holds %q: only %v may be there, never the engineer's config, plugins or sessions", e.Name(), allowed)
		}
	}
	for _, f := range chatFiles(t, ChatDir(c.home, "work")) {
		if strings.Contains(f, "engineer_only") || strings.Contains(f, "engineer plugin") {
			t.Error("a file under the chat directory carries the engineer's Codex config or plugins")
		}
	}
	if b, _ := os.ReadFile(filepath.Join(c.engineer, "config.toml")); !strings.Contains(string(b), "engineer_only") {
		t.Error("the engineer's config.toml was changed")
	}
}

func TestCodexChatKeepsTheHooksStateCodexWroteAcrossARestart(t *testing.T) {
	c := newCodexChatRun(t)
	c.startOK(t)
	cfgPath := filepath.Join(c.codexHome(), "config.toml")
	state := "[hooks.state]\n\"SessionStart:0:0\" = { trusted_hash = \"sha256:fake-hash\" }\n"
	if err := os.WriteFile(cfgPath, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	// A planted link where the login link goes is replaced, not followed.
	outside := filepath.Join(t.TempDir(), "outside-auth.json")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(c.codexHome(), "auth.json")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	c.stop(t)
	c.startOK(t)

	if b, err := os.ReadFile(cfgPath); err != nil || string(b) != state {
		t.Errorf("codex-home/config.toml after a restart = %q (%v), want Codex's hooks.state kept byte for byte", b, err)
	}
	if got, _ := os.Readlink(link); got != filepath.Join(c.engineer, "auth.json") {
		t.Errorf("after a restart auth.json points at %q, want the engineer's login", got)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Errorf("the file a planted link pointed at is %q, want it untouched", b)
	}
}

func TestCodexChatWithoutAnEngineerLoginRefusesWithTheReason(t *testing.T) {
	c := newCodexChatRun(t)
	auth := filepath.Join(c.engineer, "auth.json")
	if err := os.Remove(auth); err != nil {
		t.Fatal(err)
	}
	err := c.l.StartChat("work")
	var rerr *Error
	if !errors.As(err, &rerr) {
		t.Fatalf("StartChat without a login = %v, want a refusal", err)
	}
	if !strings.Contains(rerr.Msg, auth) {
		t.Errorf("refusal %q does not name %s", rerr.Msg, auth)
	}
	if out := c.tmuxOut(t, "list-sessions"); strings.Contains(out, sessionhost.ChatSession("work")) {
		t.Errorf("a refused start left a session: %s", out)
	}
	if _, err := os.Lstat(filepath.Join(c.codexHome(), "auth.json")); err == nil {
		t.Error("a refused start left a login link behind")
	}
}

func TestCodexChatArgvCarriesTheFenceModelEffortAndAShipsHooks(t *testing.T) {
	c := newCodexChatRun(t)
	argv, _ := c.startOK(t)
	got := codexConfigOf(argv)

	for _, f := range codexFence {
		if got[f.key] != f.want {
			t.Errorf("config %s = %q, want %q (argv %q)", f.key, got[f.key], f.want, argv)
		}
	}
	if got["model"] != "gpt-6.1-sol" {
		t.Errorf("model = %q, want the chat's gpt-6.1-sol", got["model"])
	}
	if got["model_reasoning_effort"] != "high" {
		t.Errorf("model_reasoning_effort = %q, want high", got["model_reasoning_effort"])
	}
	flags := codexFlags()
	for i := 0; i+1 < len(flags); i += 2 {
		found := false
		for j := 0; j+1 < len(argv); j++ {
			if argv[j] == flags[i] && argv[j+1] == flags[i+1] {
				found = true
			}
		}
		if !found {
			t.Errorf("argv lacks the ship's hook flag %s %s, which must be identical so Codex's hook trust holds", flags[i], flags[i+1])
		}
	}
	if v := got[codexInstructionsKey]; !strings.Contains(v, "launch_ship") || !strings.Contains(v, "ask_question") {
		t.Errorf("config %s = %q, want the chat prompt", codexInstructionsKey, v)
	}
	if !c.runsBehindPaneOrWithoutDaemon(t, argv) {
		t.Errorf("the chat neither runs behind codex-pane nor got --no-daemon: argv %q", argv)
	}
}

func TestCodexChatNeverTakesTheDocksArgsOrBypassesHookTrust(t *testing.T) {
	c := newCodexChatRun(t)
	argv, _ := c.startOK(t)
	if len(argv) == 0 {
		t.Fatal("the harness recorded no argv")
	}
	for _, banned := range []string{"--search", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", `mcp_servers.dock.command="/bin/dock-server"`} {
		if slices.Contains(argv, banned) {
			t.Errorf("argv %q carries %s", argv, banned)
		}
	}
	for _, a := range argv {
		if strings.Contains(a, "dangerously-bypass") || strings.Contains(a, "mcp_servers.dock") || strings.Contains(a, "hooks.state") {
			t.Errorf("argv element %q must not be there", a)
		}
	}
}

func TestCodexChatServesOnlyTheAdmiralMCPServer(t *testing.T) {
	c := newCodexChatRun(t)
	argv, _ := c.startOK(t)
	// The server may be defined by flags or by a file under the chat directory.
	var text strings.Builder
	for k, v := range codexConfigOf(argv) {
		text.WriteString(k + "=" + v + "\n")
	}
	for _, f := range chatFiles(t, ChatDir(c.home, "work")) {
		text.WriteString(f + "\n")
	}
	self, _ := os.Executable()
	for _, want := range []string{"mcp_servers.admiral", self, "ADMIRAL_CHAT_FLEET", "ADMIRAL_HOME", "ADMIRAL_ACTOR_TOKEN"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the chat's Codex config does not mention %q", want)
		}
	}
	if strings.Contains(text.String(), "mcp_servers.engineer_only") {
		t.Error("the engineer's MCP server is in the chat's config")
	}
}

func TestCodexChatTokenIsInAFileOnlyAndNeverInArgvOrTmux(t *testing.T) {
	c := newCodexChatRun(t)
	argv, env := c.startOK(t)
	nonce := strings.TrimSpace(c.tmuxOut(t, "show-options", "-t", "="+sessionhost.ChatSession("work")+":", "-qv", sessionhost.Prefix+sessionhost.ChatNonceOpt))
	if nonce == "" {
		t.Fatal("the chat session has no nonce option")
	}
	m, err := actor.NewMinter(c.home)
	if err != nil {
		t.Fatal(err)
	}
	token := m.Token("work", nonce)
	places := map[string]string{
		"argv":             strings.Join(argv, "\x00"),
		"the chat's env":   env,
		"tmux session env": c.tmuxOut(t, "show-environment", "-t", "="+sessionhost.ChatSession("work")+":"),
		"tmux global env":  c.tmuxOut(t, "show-environment", "-g"),
		"tmux options":     c.tmuxOut(t, "show-options", "-A", "-t", "="+sessionhost.ChatSession("work")+":"),
		"pane start":       c.tmuxOut(t, "display-message", "-p", "-t", "="+sessionhost.ChatSession("work")+":", "#{pane_start_command}"),
	}
	for where, text := range places {
		if strings.Contains(text, token) {
			t.Errorf("the actor token is in %s", where)
		}
	}
	holders := 0
	_ = filepath.WalkDir(ChatDir(c.home, "work"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if b, _ := os.ReadFile(p); strings.Contains(string(b), token) {
			holders++
			if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s holds the token and is %v, want 0600", p, fi.Mode().Perm())
			}
		}
		return nil
	})
	if holders == 0 {
		t.Error("no file under the chat directory carries the actor token, and it is not in argv: the admiral server has no identity")
	}
}

func TestCodexChatEnvHasItsOwnCodexHomeAndNoStrayAdmiralVariables(t *testing.T) {
	c := newCodexChatRun(t)
	_, env := c.startOK(t)
	vars := map[string]string{}
	for _, l := range strings.Split(env, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			vars[k] = v
		}
	}
	if vars["CODEX_HOME"] != c.codexHome() {
		t.Errorf("CODEX_HOME = %q, want the chat's %q, not the dock's engineer home", vars["CODEX_HOME"], c.codexHome())
	}
	want := map[string]string{"ADMIRAL_HOME": c.home, "ADMIRAL_CHAT_FLEET": "work", "ADMIRAL_PORT": "4242", "ADMIRAL_CHAT_HARNESS": "codex"}
	for k, v := range vars {
		if strings.HasPrefix(k, "ADMIRAL_") && want[k] != v {
			t.Errorf("env holds %s=%q: only %v belong to the chat", k, v, want)
		}
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("env %s = %q, want %q", k, vars[k], v)
		}
	}
}

func TestEachCodexChatStartLeavesTheEngineersCodexHomeAsItWas(t *testing.T) {
	c := newCodexChatRun(t)
	c.startOK(t)
	c.stop(t)
	c.startOK(t)
	var names []string
	entries, _ := os.ReadDir(c.engineer)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"auth.json", "config.toml", "plugins", "sessions"}; !slices.Equal(names, want) {
		t.Errorf("the engineer's Codex home holds %v after chat starts, want %v", names, want)
	}
	if b, _ := os.ReadFile(filepath.Join(c.engineer, "auth.json")); string(b) != codexAuthText {
		t.Errorf("the engineer's auth.json = %q, want it untouched", b)
	}
}

func TestCodexChatRefusesAHomeConfigThatLoadsCodeOrReroutesTheModel(t *testing.T) {
	for _, tc := range []struct{ key, toml string }{
		{"mcp_servers", "[mcp_servers.evil]\ncommand = \"sh\"\n"},
		{"notify", "notify = [\"sh\", \"-c\", \"id\"]\n"},
		{"model_providers", "[model_providers.x]\nbase_url = \"http://example.invalid\"\n"},
		{"profile", "profile = \"p\"\n"},
		{"profiles", "[profiles.p]\nmodel = \"x\"\n"},
	} {
		c := newCodexChatRun(t)
		c.startOK(t)
		c.stop(t)
		if err := os.WriteFile(filepath.Join(c.codexHome(), "config.toml"), []byte(tc.toml), 0o600); err != nil {
			t.Fatal(err)
		}
		err := c.l.StartChat("work")
		var rerr *Error
		if !errors.As(err, &rerr) || !strings.Contains(rerr.Msg, `"`+tc.key+`"`) {
			t.Errorf("%s: StartChat = %v, want a refusal naming the key", tc.key, err)
		}
	}
}

func TestCodexChatArgvPassesNoDaemonOnlyToACodexThatHasIt(t *testing.T) {
	chat := config.ChatSettings{Harness: "codex", Model: "gpt-6.1-sol"}
	if argv := codexChatArgv("codex", true, chat, "admiral", "/h", "work", ""); !slices.Contains(argv, "--no-daemon") {
		t.Errorf("argv %q lacks --no-daemon", argv)
	}
	if argv := codexChatArgv("codex", false, chat, "admiral", "/h", "work", ""); slices.Contains(argv, "--no-daemon") {
		t.Errorf("argv %q has --no-daemon for a Codex without it", argv)
	}
}
