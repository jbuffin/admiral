package launcher

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The Copilot chat: its own COPILOT_HOME with nothing of the engineer's linked, the fence as flags and env,
// a ship's hooks through --plugin-dir, and nothing of the engineer's Copilot config.

// Placeholders the implementer confirms on the real Copilot 1.0.109 (each a one-line change).
var (
	// copilotBareFenceFlags are the fence flags that take no value.
	copilotBareFenceFlags = []string{"--disable-builtin-mcps", "--no-ask-user"}
	// copilotAllowedToolValues are the values --allow-tool must carry.
	copilotAllowedToolValues = []string{"admiral"}
	// copilotIsAdmiralTool says whether an --available-tools entry names a tool of the admiral MCP server and nothing else.
	copilotIsAdmiralTool = func(v string) bool { return strings.HasPrefix(v, "admiral-") }
	// copilotBuiltinTools are built-in tools --available-tools must not list.
	copilotBuiltinTools = []string{"bash", "shell", "view", "edit", "write", "create", "glob", "grep", "web_fetch", "web_search",
		"task", "sql", "ask_user", "skill", "read_bash", "write_bash", "stop_bash"}
	// copilotLinkedFiles are the files of the engineer's Copilot home the chat's home may link: none while the login is in the Keychain.
	copilotLinkedFiles []string
	// copilotHomeAdmiralFiles are the files Admiral itself may write in the chat's copilot-home (a prompt route may add one).
	copilotHomeAdmiralFiles = []string{"copilot-instructions.md"}
	// copilotDroppedEnv are the variables the chat must not carry from the dock or from Admiral's own environment.
	copilotDroppedEnv = []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_API_KEY",
		"COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDERS_CONFIG", "COPILOT_SKILLS_DIRS", "COPILOT_ALLOW_ALL", "OTEL_EXPORTER_OTLP_ENDPOINT"}
	// copilotBannedArgs are what the dock's args carry and the chat must not.
	copilotBannedArgs = []string{"--allow-all", "--yolo", "--allow-all-tools", "--allow-all-paths", "--add-dir", "/elsewhere"}
)

const (
	copilotMCPFlag      = "--additional-mcp-config"
	copilotFakeToken    = "fake-token-copilot-gh"
	copilotEngineerTag  = "engineer_only"
	copilotEngineerHook = "engineer-hook"
)

var copilotUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type copilotChat struct {
	*chatRun
	engineer string // the engineer's COPILOT_HOME
}

func newCopilotChatRun(t *testing.T) *copilotChat {
	t.Helper()
	c := newChatRun(t)
	engineer := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", engineer)
	// Admiral's own environment is a second way the engineer's login or provider could reach the chat.
	t.Setenv("GH_TOKEN", copilotFakeToken+"-ambient")
	t.Setenv("COPILOT_PROVIDER_BASE_URL", "http://example.invalid/ambient")
	for rel, body := range map[string]string{
		"config.json":                     `{"loggedInUsers":[{"host":"github.com","login":"engineer"}]}`,
		"mcp-config.json":                 `{"mcpServers":{"` + copilotEngineerTag + `":{"command":"/bin/false"}}}`,
		"settings.json":                   `{"hooks":{"sessionStart":[{"command":"` + copilotEngineerHook + `"}]}}`,
		"installed-plugins/p/plugin.json": `{"name":"engineer plugin"}`,
		"session-state/" + testSessionA + "/events.jsonl": "{}\n",
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
	cfg.Fleets[0].Chat = config.ChatSettings{Harness: "copilot", Model: "gpt-5.6-sol"}
	dockEnv := map[string]string{"COPILOT_HOME": engineer, "ADMIRAL_SHIP": "stray-ship", "COPILOT_CUSTOM_INSTRUCTIONS_DIRS": "/elsewhere/instructions"}
	for _, k := range copilotDroppedEnv {
		dockEnv[k] = "fake-" + strings.ToLower(k)
	}
	cfg.Fleets[0].Harnesses = []config.Harness{{Name: "copilot", Executable: exe, Found: true,
		Args: []string{"--allow-all", "--yolo", "--allow-all-tools", "--allow-all-paths", "--add-dir", "/elsewhere",
			copilotMCPFlag, `{"mcpServers":{"dock":{"command":"/bin/dock-server"}}}`},
		Env: dockEnv}}
	return &copilotChat{chatRun: c, engineer: engineer}
}

const testSessionA = "5d1f7a3e-2b4c-4e8a-9c61-3f0a7b8d9e12"

func (c *copilotChat) copilotHome() string {
	return filepath.Join(ChatDir(c.home, "work"), "copilot-home")
}

// flagValues reads an argv the way Copilot does for a repeatable flag: --flag=a,b, or --flag a b up to the next flag.
func flagValues(argv []string, flag string) (vals []string, present bool) {
	for i := 0; i < len(argv); i++ {
		switch {
		case argv[i] == flag:
			present = true
			for j := i + 1; j < len(argv) && !strings.HasPrefix(argv[j], "-"); j++ {
				vals = append(vals, argv[j])
			}
		case strings.HasPrefix(argv[i], flag+"="):
			present = true
			vals = append(vals, strings.Split(strings.TrimPrefix(argv[i], flag+"="), ",")...)
		}
	}
	return vals, present
}

func envMap(env string) map[string]string {
	vars := map[string]string{}
	for _, l := range strings.Split(env, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			vars[k] = v
		}
	}
	return vars
}

func TestCopilotChatHomeIsTheChatsOwnHoldsNothingOfTheEngineersAndSurvivesARestart(t *testing.T) {
	c := newCopilotChatRun(t)
	c.start(t)

	want := filepath.Join(ChatDir(c.home, "work"), "copilot-home")
	if got := ChatCopilotHome(c.home, "work"); got != want {
		t.Fatalf("ChatCopilotHome = %q, want %q", got, want)
	}
	if fi, err := os.Stat(want); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("copilot-home = %v, %v, want a 0700 directory", fi, err)
	}
	allowed := append(slices.Clone(copilotLinkedFiles), copilotHomeAdmiralFiles...)
	entries, _ := os.ReadDir(want)
	for _, e := range entries {
		if !slices.Contains(allowed, e.Name()) {
			t.Errorf("copilot-home holds %q: only %v may be there, never the engineer's config, plugins, hooks or sessions", e.Name(), allowed)
		}
	}
	for rel, f := range chatFiles(t, ChatDir(c.home, "work")) {
		if strings.Contains(f, copilotEngineerTag) || strings.Contains(f, "engineer plugin") || strings.Contains(f, copilotEngineerHook) {
			t.Errorf("%s carries the engineer's Copilot config, plugins or hooks", rel)
		}
	}
	// Copilot writes config.json (login users, trustedFolders) and session-state/ itself: both stay across a restart.
	written := map[string]string{"config.json": `{"trustedFolders":["x"]}`, "session-state/" + testSessionA + "/events.jsonl": "{\"type\":\"session.start\"}\n"}
	for rel, body := range written {
		p := filepath.Join(want, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.stop(t)
	c.start(t)
	for rel, body := range written {
		if b, err := os.ReadFile(filepath.Join(want, rel)); err != nil || string(b) != body {
			t.Errorf("copilot-home/%s after a restart = %q (%v), want what Copilot wrote kept byte for byte", rel, b, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(c.engineer, "mcp-config.json")); !strings.Contains(string(b), copilotEngineerTag) {
		t.Error("the engineer's mcp-config.json was changed")
	}
	var names []string
	engEntries, _ := os.ReadDir(c.engineer)
	for _, e := range engEntries {
		names = append(names, e.Name())
	}
	if w := []string{"config.json", "installed-plugins", "mcp-config.json", "session-state", "settings.json"}; !slices.Equal(names, w) {
		t.Errorf("the engineer's Copilot home holds %v after chat starts, want %v", names, w)
	}
}

func TestCopilotChatArgvCarriesTheFenceModelSessionIDAndAShipsHooks(t *testing.T) {
	c := newCopilotChatRun(t)
	argv, _ := c.start(t)

	for _, f := range copilotBareFenceFlags {
		if !slices.Contains(argv, f) {
			t.Errorf("argv lacks %s (argv %q)", f, argv)
		}
	}
	allow, ok := flagValues(argv, "--allow-tool")
	if !ok {
		t.Fatalf("argv lacks --allow-tool (argv %q)", argv)
	}
	for _, w := range copilotAllowedToolValues {
		if !slices.Contains(allow, w) {
			t.Errorf("--allow-tool values %q lack %q", allow, w)
		}
	}
	avail, ok := flagValues(argv, "--available-tools")
	if !ok || len(avail) == 0 {
		t.Fatalf("argv lacks --available-tools with the admiral server's tools: %q", argv)
	}
	for _, v := range avail {
		if !copilotIsAdmiralTool(v) {
			t.Errorf("--available-tools lists %q, which is not a tool of the admiral server", v)
		}
		if slices.Contains(copilotBuiltinTools, v) {
			t.Errorf("--available-tools lists the built-in tool %q", v)
		}
	}
	if m, ok := flagValues(argv, "--model"); !ok || len(m) != 1 || m[0] != "gpt-5.6-sol" {
		t.Errorf("--model = %q, want the chat's gpt-5.6-sol", m)
	}
	id, ok := flagValues(argv, "--session-id")
	if !ok || len(id) != 1 || !copilotUUID.MatchString(id[0]) {
		t.Fatalf("--session-id = %q, want one UUID so the transcript is known at start", id)
	}
	if dirs, ok := flagValues(argv, "--plugin-dir"); !ok || !slices.Contains(dirs, copilotPluginDir(c.home)) {
		t.Errorf("--plugin-dir = %q, want %q: the hooks come from Admiral's plugin as a ship's do", dirs, copilotPluginDir(c.home))
	}

	// Each start is a new session: its own id, so the reader never follows the last one's transcript.
	c.stop(t)
	argv2, _ := c.start(t)
	if id2, _ := flagValues(argv2, "--session-id"); len(id2) != 1 || id2[0] == id[0] {
		t.Errorf("a second start reused --session-id %q", id)
	}
}

func TestCopilotChatNeverTakesTheDocksArgs(t *testing.T) {
	c := newCopilotChatRun(t)
	argv, _ := c.start(t)
	for _, a := range argv {
		if slices.Contains(copilotBannedArgs, a) || strings.Contains(a, "dock-server") {
			t.Errorf("argv %q carries %q from the dock", argv, a)
		}
	}
	for _, a := range []string{"--allow-all", "--yolo", "--allow-all-tools", "--allow-all-paths", "--add-dir"} {
		if slices.Contains(argv, a) {
			t.Errorf("argv carries %s", a)
		}
	}
}

// copilotMCPServers is the admiral server definition, from --additional-mcp-config given inline or as @file.
func copilotMCPServers(t *testing.T, argv []string, chatDir string) map[string]mcpServer {
	t.Helper()
	vals, _ := flagValues(argv, copilotMCPFlag)
	out := map[string]mcpServer{}
	for _, v := range vals {
		raw := v
		if rest, ok := strings.CutPrefix(v, "@"); ok {
			if !filepath.IsAbs(rest) {
				rest = filepath.Join(chatDir, rest)
			}
			b, err := os.ReadFile(rest)
			if err != nil {
				t.Fatalf("the chat's MCP config file: %v", err)
			}
			raw = string(b)
		}
		var cfg struct {
			Servers map[string]mcpServer `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("%s is not an MCP config: %v", v, err)
		}
		for k, s := range cfg.Servers {
			out[k] = s
		}
	}
	return out
}

func TestCopilotChatServesOnlyTheAdmiralMCPServer(t *testing.T) {
	c := newCopilotChatRun(t)
	argv, _ := c.start(t)
	servers := copilotMCPServers(t, argv, ChatDir(c.home, "work"))
	s, ok := servers["admiral"]
	if !ok || len(servers) != 1 {
		t.Fatalf("the chat's MCP servers = %v, want exactly admiral", servers)
	}
	self, _ := os.Executable()
	if s.Command != self || !slices.Contains(s.Args, "mcp") {
		t.Errorf("admiral server = %+v, want the admiral binary run as `mcp`", s)
	}
	for _, k := range []string{"ADMIRAL_HOME", "ADMIRAL_CHAT_FLEET", "ADMIRAL_ACTOR_TOKEN_FILE"} {
		if s.Env[k] == "" {
			t.Errorf("admiral server env lacks %s: %v", k, s.Env)
		}
	}
	if _, has := s.Env["ADMIRAL_ACTOR_TOKEN"]; has {
		t.Error("the server's env carries the token itself, which argv would then show")
	}
}

func TestCopilotChatTokenIsInAFileOnlyAndNeverInArgvOrTmux(t *testing.T) {
	c := newCopilotChatRun(t)
	argv, env := c.start(t)
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

func TestCopilotChatEnvHasItsOwnHomeThePluginOnlyFlagAndNoOtherConfigOrLogin(t *testing.T) {
	c := newCopilotChatRun(t)
	_, env := c.start(t)
	vars := envMap(env)
	if vars["COPILOT_HOME"] != c.copilotHome() {
		t.Errorf("COPILOT_HOME = %q, want the chat's %q, not the dock's engineer home", vars["COPILOT_HOME"], c.copilotHome())
	}
	if vars["COPILOT_PLUGIN_DIR_ONLY"] != "true" {
		t.Errorf("COPILOT_PLUGIN_DIR_ONLY = %q, want true: only --plugin-dir plugins may load", vars["COPILOT_PLUGIN_DIR_ONLY"])
	}
	for _, k := range copilotDroppedEnv {
		if v, ok := vars[k]; ok {
			t.Errorf("env holds %s=%q: it would carry another login, provider or config into the chat", k, v)
		}
	}
	if v, ok := vars["COPILOT_CUSTOM_INSTRUCTIONS_DIRS"]; ok && !strings.HasPrefix(v, ChatDir(c.home, "work")) {
		t.Errorf("COPILOT_CUSTOM_INSTRUCTIONS_DIRS = %q: only a directory of Admiral's own may be there", v)
	}
	want := map[string]string{"ADMIRAL_HOME": c.home, "ADMIRAL_CHAT_FLEET": "work", "ADMIRAL_PORT": "4242", "ADMIRAL_CHAT_HARNESS": "copilot"}
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

func TestCopilotChatCarriesTheChatPromptByARouteAdmiralWrites(t *testing.T) {
	c := newCopilotChatRun(t)
	argv, env := c.start(t)
	var text strings.Builder
	text.WriteString(strings.Join(argv, "\n") + "\n" + env + "\n")
	for _, f := range chatFiles(t, ChatDir(c.home, "work")) {
		text.WriteString(f + "\n")
	}
	for _, want := range []string{"launch_ship", "ask_question", "AdmiralBot"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("neither argv, the env nor a file of the chat directory carries the chat prompt (%q missing)", want)
		}
	}
}

func TestCopilotChatClearsWhatAFolderTrustWouldLoadFromItsWorkingDirectory(t *testing.T) {
	c := newCopilotChatRun(t)
	dir := ChatDir(c.home, "work")
	planted := []string{".mcp.json", ".github/mcp.json", ".github/hooks/h.json", ".claude/settings.json", "CLAUDE.md", ".github/copilot-instructions.md"}
	for _, rel := range planted {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("planted"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.start(t)
	for _, rel := range planted {
		if _, err := os.Lstat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("%s is still in the chat's working directory after a start: a trusted folder would load it", rel)
		}
	}
}

func TestCopilotChatRefusesAHomeConfigThatAddsAServerPluginHookProviderAgentOrSkill(t *testing.T) {
	for _, tc := range []struct{ name, rel, body string }{
		{"mcp server", "mcp-config.json", `{"mcpServers":{"evil":{"command":"sh"}}}`},
		{"hook in settings", "settings.json", `{"hooks":{"sessionStart":[{"command":"sh"}]}}`},
		{"plugin in settings", "settings.json", `{"enabledPlugins":{"evil@market":true}}`},
		{"provider", "providers.json", `{"providers":[{"baseUrl":"http://example.invalid"}]}`},
		{"installed plugin", "installed-plugins/evil/plugin.json", `{}`},
		{"agent", "agents/evil.md", "evil"},
		{"skill", "skills/evil/SKILL.md", "evil"},
		{"hook file", "hooks/evil.json", `{}`},
		{"a file Copilot does not create", "extra.json", `{}`},
		{"unknown key in config", "config.json", `{"loggedInUsers":[],"mcpServers":{"evil":{"command":"sh"}}}`},
		{"unknown key in settings", "settings.json", `{"model":"x","allowedUrls":["*"]}`},
		{"config that is not an object", "config.json", `[1]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCopilotChatRun(t)
			c.start(t)
			c.stop(t)
			p := filepath.Join(c.copilotHome(), tc.rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			err := c.l.StartChat("work")
			var rerr *Error
			if !errors.As(err, &rerr) {
				t.Fatalf("StartChat = %v, want a refusal", err)
			}
			if rerr.Status != 409 || !strings.Contains(rerr.Msg, strings.Split(tc.rel, "/")[0]) {
				t.Errorf("refusal = %d %q, want a 409 naming %s", rerr.Status, rerr.Msg, strings.Split(tc.rel, "/")[0])
			}
			if out := c.tmuxOut(t, "list-sessions"); strings.Contains(out, sessionhost.ChatSession("work")) {
				t.Errorf("a refused start left a session: %s", out)
			}
		})
	}
}

func TestCopilotChatRefusesAHomeEntryItCannotReadOrThatIsASymlink(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T, home string)
	}{
		{"symlink", func(t *testing.T, home string) {
			target := filepath.Join(t.TempDir(), "elsewhere.json")
			if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(home, "config.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"unreadable", func(t *testing.T, home string) {
			if err := os.Mkdir(filepath.Join(home, "config.json"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCopilotChatRun(t)
			c.start(t)
			c.stop(t)
			tc.make(t, c.copilotHome())
			var rerr *Error
			if err := c.l.StartChat("work"); !errors.As(err, &rerr) || rerr.Status != 409 || !strings.Contains(rerr.Msg, "config.json") {
				t.Fatalf("StartChat = %v, want a 409 naming config.json", err)
			}
		})
	}
}

func TestCopilotChatRefusesToStartInsideAGitWorkTree(t *testing.T) {
	c := newCopilotChatRun(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(repo, "admiral-home")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteBundle(inside); err != nil {
		t.Fatal(err)
	}
	installPassThroughWrapper(t, inside)
	c.l.Home = inside
	var rerr *Error
	err := c.l.StartChat("work")
	if !errors.As(err, &rerr) || rerr.Status != 409 || !strings.Contains(rerr.Msg, "git work tree at "+repo) {
		t.Fatalf("StartChat = %v, want a 409 naming the work tree at %s", err, repo)
	}
	if out := c.tmuxOut(t, "list-sessions"); strings.Contains(out, sessionhost.ChatSession("work")) {
		t.Errorf("a refused start left a session: %s", out)
	}
}

func TestCopilotChatUnsetsWhatAnOutlivingTmuxServerHoldsGloballyThatTheFenceDrops(t *testing.T) {
	c := newCopilotChatRun(t)
	// A tmux server started before Admiral's, with a login and a collector in its global env, as an old run leaves it.
	c.tmuxOut(t, "new-session", "-d", "-s", "other", "sleep 60")
	// Names the dock and Admiral's own env do not set, so only the global rule can drop them.
	globals := map[string]string{"GH_ENTERPRISE_TOKEN": "fake-token-global", "COPILOT_GITHUB_TOKEN_OLD": "fake-token-global-2",
		"COPILOT_API_URL": "http://example.invalid/global", "OTEL_EXPORTER_OTLP_HEADERS": "authorization=fake-token-global-3", "KEEP_ME": "yes"}
	for k, v := range globals {
		if out := c.tmuxOut(t, "set-environment", "-g", k, v); out != "" {
			t.Fatalf("set-environment %s: %s", k, out)
		}
	}
	_, env := c.start(t)
	vars := envMap(env)
	for k := range globals {
		if k == "KEEP_ME" {
			if vars[k] != "yes" {
				t.Errorf("the chat lost %s, which the fence does not drop", k)
			}
			continue
		}
		if v, ok := vars[k]; ok {
			t.Errorf("the chat's pane has %s=%q from the tmux server's global env", k, v)
		}
	}
	if vars["COPILOT_HOME"] != c.copilotHome() {
		t.Errorf("COPILOT_HOME = %q, want the chat's own", vars["COPILOT_HOME"])
	}
}

func TestCopilotChatExcludesEveryBuiltinToolAsASecondLayer(t *testing.T) {
	c := newCopilotChatRun(t)
	argv, _ := c.start(t)
	excl, ok := flagValues(argv, "--excluded-tools")
	if !ok {
		t.Fatalf("argv lacks --excluded-tools: %q", argv)
	}
	for _, b := range []string{"bash", "apply_patch", "web_fetch", "task", "view", "edit", "create", "sql", "ask_user"} {
		if !slices.Contains(excl, b) {
			t.Errorf("--excluded-tools %q lacks %s", excl, b)
		}
	}
	for _, e := range excl {
		if strings.HasPrefix(e, "admiral-") {
			t.Errorf("--excluded-tools lists the admiral tool %s", e)
		}
	}
	for _, f := range []string{"--disallow-temp-dir", "--sync-session=off"} {
		if !slices.Contains(argv, f) {
			t.Errorf("argv lacks %s", f)
		}
	}
	for _, a := range argv {
		if strings.HasPrefix(a, "@") {
			t.Errorf("argv carries %q: the admiral server's config goes inline, with only the token's file path", a)
		}
	}
}

func TestCopilotChatStartsWithTheConfigAndStateCopilotWritesItself(t *testing.T) {
	c := newCopilotChatRun(t)
	c.start(t)
	c.stop(t)
	for rel, body := range map[string]string{
		"config.json":   "// User settings belong in settings.json.\n// This file is managed automatically.\n{\"firstLaunchAt\":\"x\",\"trustedFolders\":[\"/x\"],\"loggedInUsers\":[{\"login\":\"engineer\"}]}",
		"settings.json": `{"model":"gpt-5.6-sol"}`,
		"session-state/" + testSessionA + "/events.jsonl": "{}\n",
		"logs/process.log":     "x",
		"session-store.db-wal": "x", "ide/lock": "x", "servers/s": "x", "plugin-data/p": "x", "open-sessions-state.json": "{}",
	} {
		p := filepath.Join(c.copilotHome(), rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.start(t)
}

func TestCopilotChatArgvCarriesEveryFlagOfTheSharedFenceTable(t *testing.T) {
	fence := CopilotFenceArgs("admiral")
	if len(fence) == 0 {
		t.Fatal("CopilotFenceArgs(admiral) is empty: the fence is one table the chat and the probes share")
	}
	c := newCopilotChatRun(t)
	argv, _ := c.start(t)
	for _, a := range fence {
		if !slices.Contains(argv, a) {
			t.Errorf("argv %q lacks %q from the shared fence table", argv, a)
		}
	}
}
