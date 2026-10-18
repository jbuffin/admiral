package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// Helpers for the u6 scenarios (repo finder, skill index, launch orders data,
// favorites). A dataWorld is a world whose fleets keep every identity
// directory and HOME under the world's temporary root, so nothing the world
// installs for the u4 and u5 scenarios (a fake Commodore, say) leaks into a
// scenario that counts skills.

type dataWorld struct {
	*world
	fh     string // HOME of the fleets' harnesses
	claude *claudeConfig
	codex  string // CODEX_HOME
	cop    string // COPILOT_HOME
}

func init() {
	// The engineer's own identity directories must never reach Admiral: a fleet
	// that sets none would read them for its skills.
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLAUDE_CODE_PLUGIN_CACHE_DIR"} {
		_ = os.Unsetenv(k)
	}
}

func newDataWorld(t *testing.T) *dataWorld {
	t.Helper()
	w := newWorld(t)
	d := &dataWorld{world: w, fh: filepath.Join(w.root, "fh"), codex: filepath.Join(w.root, "id", "codex"),
		cop: filepath.Join(w.root, "id", "copilot")}
	d.claude = newClaudeConfig(t, filepath.Join(w.root, "id", "claude"))
	for _, p := range []string{d.fh, d.codex, d.cop, d.claude.dir} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func (d *dataWorld) dockEnv(harness string, args ...string) map[string]any {
	dock := d.dock(harness, args...)
	env := d.harnessEnv()
	env["HOME"] = d.fh
	env["CLAUDE_CONFIG_DIR"] = d.claude.dir
	env["CODEX_HOME"] = d.codex
	env["COPILOT_HOME"] = d.cop
	dock["env"] = env
	return dock
}

// start writes fleets work and home (all docks, search root w.work) and
// nosearch (none), then starts Admiral.
func (d *dataWorld) start() *fleetInst {
	d.t.Helper()
	fleets := map[string]any{
		"work": map[string]any{"name": "Work", "search_roots": []string{d.work},
			"claude": d.dockEnv("claude", "--fa-claude"), "codex": d.dockEnv("codex", "--fa-codex"),
			"copilot": d.dockEnv("copilot", "--fa-copilot")},
		"home": map[string]any{"name": "Home", "search_roots": []string{d.work},
			"claude": d.dockEnv("claude", "--fa-claude"), "codex": d.dockEnv("codex", "--fa-codex")},
		"nosearch": map[string]any{"name": "No Search", "search_roots": []string{},
			"claude": d.dockEnv("claude", "--fa-claude")},
	}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": fleets})
	if err != nil {
		d.t.Fatal(err)
	}
	writeConfig(d.t, d.home, string(b))
	return d.world.start()
}

// ---- git checkouts ----

func (w *world) git(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") && !strings.HasPrefix(kv, "HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "HOME="+filepath.Join(w.root, "gh"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo makes a real git checkout at w.work/<rel> with the given remotes (the
// first is origin) and returns its path.
func (w *world) repo(rel string, remotes ...string) string {
	w.t.Helper()
	d := filepath.Join(w.work, rel)
	if err := os.MkdirAll(d, 0o755); err != nil {
		w.t.Fatal(err)
	}
	w.git(d, "init", "-q")
	for i, r := range remotes {
		name := "origin"
		if i > 0 {
			name = "remote" + strconv.Itoa(i)
		}
		w.git(d, "remote", "add", name, r)
	}
	return d
}

// worktree adds a linked worktree of the checkout at main, at w.work/<rel>.
// Its .git is a file pointing into the main checkout.
func (w *world) worktree(main, rel string) string {
	w.t.Helper()
	w.git(main, "commit", "-q", "--allow-empty", "-m", "x")
	d := filepath.Join(w.work, rel)
	w.git(main, "worktree", "add", "-q", "-b", "wt-"+strconv.Itoa(int(seq.Add(1))), d)
	return d
}

// ---- skills ----

// writeSkill makes <base>/<name>/SKILL.md.
func writeSkill(t *testing.T, base, name string) {
	t.Helper()
	d := filepath.Join(base, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + filepath.Base(name) + "\ndescription: a fake skill\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

const (
	decoySecret = "decoy-secret-7f3a91"
	decoyHelper = "decoy-helper-4c82d0"
)

// claudeConfig is a fake CLAUDE_CONFIG_DIR: settings.json with enabledPlugins
// (and decoy keys that must never come back), plugins/installed_plugins.json.
type claudeConfig struct {
	t         *testing.T
	dir       string
	installed map[string][]map[string]any
	enabled   map[string]bool
}

func newClaudeConfig(t *testing.T, dir string) *claudeConfig {
	return &claudeConfig{t: t, dir: dir, installed: map[string][]map[string]any{}, enabled: map[string]bool{}}
}

// plugin installs a plugin: key is "<name>@<marketplace>", name the manifest's
// name, installPath where its files are (anywhere), manifest extras such as
// {"skills": ["./extra"]}, and skills the folders under <installPath>/skills
// that hold a SKILL.md (they may be nested, like "engineering/commodore").
// It returns installPath.
func (c *claudeConfig) plugin(key, name, installPath string, extra map[string]any, skills ...string) string {
	c.t.Helper()
	manifest := map[string]any{"name": name, "version": "1.0.0"}
	for k, v := range extra {
		manifest[k] = v
	}
	writeJSON(c.t, filepath.Join(installPath, ".claude-plugin", "plugin.json"), manifest)
	for _, s := range skills {
		writeSkill(c.t, filepath.Join(installPath, "skills"), s)
	}
	c.installed[key] = append(c.installed[key], map[string]any{"scope": "user", "installPath": installPath, "version": "1.0.0"})
	c.flush()
	return installPath
}

// enable sets enabledPlugins[key] in settings.json.
func (c *claudeConfig) enable(key string, on bool) {
	c.enabled[key] = on
	c.flush()
}

func (c *claudeConfig) flush() {
	writeJSON(c.t, filepath.Join(c.dir, "plugins", "installed_plugins.json"),
		map[string]any{"version": 2, "plugins": c.installed})
	writeJSON(c.t, filepath.Join(c.dir, "settings.json"), map[string]any{
		"enabledPlugins": c.enabled,
		"env":            map[string]string{"API_SECRET": decoySecret},
		"apiKeyHelper":   "echo " + decoyHelper,
	})
}

// ---- the launch-orders API ----

type dirEntry struct {
	Path      string   `json:"path"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Remotes   []string `json:"remotes"`
	ShipsHere int      `json:"ships_here"`
}

type dirsResp struct {
	Entries  []dirEntry `json:"entries"`
	Selected *string    `json:"selected"`
	Reason   *string    `json:"reason"`
}

func (r dirsResp) paths() []string {
	p := []string{}
	for _, e := range r.Entries {
		p = append(p, e.Path)
	}
	return p
}

func (r dirsResp) selected() string {
	if r.Selected == nil {
		return ""
	}
	return *r.Selected
}

func (r dirsResp) reason() string {
	if r.Reason == nil {
		return ""
	}
	return *r.Reason
}

type skillsResp struct {
	Skills []struct {
		Name     string `json:"name"`
		Favorite bool   `json:"favorite"`
	} `json:"skills"`
	Commodore struct {
		Found bool   `json:"found"`
		Name  string `json:"name"`
	} `json:"commodore"`
}

func (r skillsResp) names() []string {
	n := []string{}
	for _, s := range r.Skills {
		n = append(n, s.Name)
	}
	return n
}

func (w *world) postDirs(fleet, harness, repo, text string) (int, string) {
	w.t.Helper()
	body := map[string]any{"fleet": fleet, "harness": harness}
	if repo != "" {
		body["repo"] = repo
	}
	if text != "" {
		body["text"] = text
	}
	return send(w.t, http.MethodPost, w.in.LocalURL+"/api/launch-orders/dirs", body)
}

func (w *world) dirs(fleet, harness, repo, text string) dirsResp {
	w.t.Helper()
	code, body := w.postDirs(fleet, harness, repo, text)
	if code != http.StatusOK {
		w.t.Fatalf("POST /api/launch-orders/dirs = %d, want 200: %s", code, body)
	}
	var r dirsResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		w.t.Fatalf("dirs response is not JSON: %v\n%s", err, body)
	}
	return r
}

func (w *world) getSkills(fleet, harness, dir string) (int, string) {
	w.t.Helper()
	q := url.Values{"fleet": {fleet}, "harness": {harness}, "dir": {dir}}
	return send(w.t, http.MethodGet, w.in.LocalURL+"/api/launch-orders/skills?"+q.Encode(), nil)
}

func (w *world) skills(t *testing.T, fleet, harness, dir string) skillsResp {
	t.Helper()
	code, body := w.getSkills(fleet, harness, dir)
	if code != http.StatusOK {
		t.Fatalf("GET /api/launch-orders/skills = %d, want 200: %s", code, body)
	}
	var r skillsResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("skills response is not JSON: %v\n%s", err, body)
	}
	return r
}

var seq atomic.Int64

// launchReq builds a launch request for an existing directory.
func (w *world) launchReq(fleet, harness, dir string, skill map[string]any, text string) map[string]any {
	return map[string]any{"fleet": fleet, "harness": harness, "dir": dir, "skill": skill,
		"source": "todo", "order_id": "o" + strconv.Itoa(int(seq.Add(1))), "text": text}
}
