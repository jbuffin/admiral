package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A dataWorld is a world whose fleets keep every identity directory and HOME
// under the world's temporary root, so nothing of the engineer's leaks in.

type dataWorld struct {
	*world
	fh     string // HOME of the fleets' harnesses
	claude string // CLAUDE_CONFIG_DIR
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
	d.claude = filepath.Join(w.root, "id", "claude")
	for _, p := range []string{d.fh, d.codex, d.cop, d.claude} {
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
	env["CLAUDE_CONFIG_DIR"] = d.claude
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

// ---- the launch-orders API ----

type dirsResp struct {
	Selected *string `json:"selected"`
	Reason   *string `json:"reason"`
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
