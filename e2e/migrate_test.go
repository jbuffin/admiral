package e2e

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The migration's rules are unit tests in internal/config; this is the startup
// wiring: the server migrates before it loads and tells the engineer once.

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

// notices are the notice items GET /api/inbox lists.
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

// waitNotice waits for the notice the migration posts at startup.
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

// backups are the config.json.pre-<version> files beside config.json.
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

// oldConfig is a config from before fleets owned sources: two fleets, zulu
// first, an unknown top-level key, a test source (with a secret setting), a
// github source and a freeform source with no `fleet`, and a resume source
// that names the freeform one.
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
	w := newWorld(t)
	w.newFakeGH()
	writeConfig(t, w.home, w.oldConfig())
	w.start()

	notice := w.in.waitNotice(t)
	if notice.Session != "_admiral" || !strings.Contains(notice.Notice.Body, "tracker") {
		t.Errorf("notice = %+v, want one from Admiral naming the moved source", notice)
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
