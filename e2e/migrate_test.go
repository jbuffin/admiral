package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type noticeItem struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Session string `json:"session"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Notice  struct {
		Body string `json:"body"`
	} `json:"notice"`
}

func (in *fleetInst) notices(t *testing.T) []noticeItem {
	t.Helper()
	code, body := send(t, http.MethodGet, in.LocalURL+"/api/inbox", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/inbox = %d, want 200: %s", code, body)
	}
	var wrap struct {
		Items []noticeItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("GET /api/inbox is not JSON: %v\n%s", err, body)
	}
	var out []noticeItem
	for _, it := range wrap.Items {
		if it.Kind == "notice" {
			out = append(out, it)
		}
	}
	return out
}

func (in *fleetInst) waitNotice(t *testing.T) noticeItem {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if n := in.notices(t); len(n) > 0 {
			return n[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 10s GET /api/inbox listed no notice")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (w *world) backups() []string {
	w.t.Helper()
	m, err := filepath.Glob(filepath.Join(w.home, "config.json.pre-*"))
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

func (w *world) seedFleetsOf(first, second string) string {
	env, _ := json.Marshal(w.harnessEnv())
	one := func(key, name string) string {
		return `"` + key + `": {"name": "` + name + `", "color": "#336699", "search_roots": [` + quote(w.work) + `], ` +
			`"claude": {"executable": ` + quote(w.fakeExe("claude")) + `, "env": ` + string(env) + `}}`
	}
	return one(first, "Zulu") + ",\n    " + one(second, "Alpha")
}

// oldConfig is a config from before fleets owned sources.
func (w *world) oldConfig() string {
	w.t.Helper()
	d := w.orderSrcDef("tracker", "Tracker")
	settings, _ := json.Marshal(d.Settings)
	return `{
  "version": 1,
  "x_future": {"keep": [1, 2, {"deep": true}]},
  "favorite_skills": ["tdd"],
  "fleets": {
    ` + w.seedFleetsOf("zulu", "alpha") + `
  },
  "sources": {
    "tracker": {"plugin": "testplug", "name": "Tracker", "settings": ` + string(settings) + `},
    "github": {"plugin": "github", "name": "GitHub"},
    "notes": {"plugin": "freeform", "name": "Notes"}
  },
  "autopilot": {"max_ships": 2, "resume_source": {"source": "notes", "panel": "orders"}}
}
`
}

func TestMigrateAtStartupPostsOneNotice(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.newFakeGH()
	writeConfig(t, w.home, w.oldConfig())
	plugin := map[string]string{"plugin.json": `{"name":"github","command":"python3"}`}
	pluginDir := filepath.Join(w.home, "plugins", "github")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(plugin["plugin.json"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, ".admiral-stamp"), []byte(stampOf(plugin)), 0o644); err != nil {
		t.Fatal(err)
	}
	w.start()

	notice := w.in.waitNotice(t)
	if notice.Session != "_admiral" || !strings.Contains(notice.Notice.Body, "tracker") || !strings.Contains(notice.Notice.Body, "built in") {
		t.Errorf("notice = %+v, want one from Admiral naming the moved source and saying GitHub is built in", notice)
	}
	srcs := w.configSources()
	if srcs["github"]["fleet"] != "zulu" || srcs["github-alpha"]["fleet"] != "alpha" {
		t.Errorf("GitHub sources = %v, want github on zulu and github-alpha on alpha", srcs)
	}
	if _, err := os.Lstat(pluginDir); err == nil {
		t.Errorf("the retired plugins/github folder is still there after the start")
	}
	backups := w.backups()
	if len(backups) != 1 {
		t.Fatalf("config.json.pre-<version> files = %v, want exactly one", backups)
	}
	if cfg, _ := fetchConfig(t, w.in.instance); cfg.Error != nil {
		t.Fatalf("the migrated config does not load: %+v", cfg.Error)
	}

	cfgBefore := w.readFile(configPath(w.home))
	w.restart()
	if got := w.readFile(configPath(w.home)); got != cfgBefore {
		t.Errorf("a restart rewrote config.json:\n%s", got)
	}
	// A second migration would post before the server listens, so once the
	// inbox lists the first notice it lists every one.
	w.in.waitNotice(t)
	if n := w.in.notices(t); len(n) != 1 || n[0].ID != notice.ID {
		t.Errorf("after a restart the notices are %+v, want only the first one", n)
	}
}
