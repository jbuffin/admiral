package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// #259 u2: the startup migration of an old config (every source gains the first
// fleet, freeform sources go, the engineer is told in one Inbox notice) and the
// refusal of the other commands to read a config that has not been migrated.
// Observed through the binary, the files in $ADMIRAL_HOME and GET /api/inbox.

const (
	orderOne = "Fix the flaky login test\nand keep the second line"
	orderTwo = "Bump the linter to the next minor"
	// decoy is a secret in a source's settings: the notice must never copy it.
	decoy = "decoy-secret-0a1b2c3d"
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
	d.Settings["token"] = decoy
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

func (w *world) seedFreeformOrders(key string, orders ...string) {
	w.t.Helper()
	list := []map[string]string{}
	for i, text := range orders {
		list = append(list, map[string]string{"id": "o" + string(rune('1'+i)), "text": text})
	}
	b, _ := json.Marshal(list)
	w.writeRaw(filepath.Join(w.dataDir(key), "orders.json"), string(b), 0o600)
}

func TestMigrateOldConfig(t *testing.T) {
	w := newWorld(t)
	w.newFakeGH()
	old := w.oldConfig()
	writeConfig(t, w.home, old)
	w.seedFreeformOrders("notes", orderOne, orderTwo)
	w.start()

	notice := w.in.waitNotice(t)

	// The backup is the old file, byte for byte.
	backups := w.backups()
	if len(backups) != 1 {
		t.Fatalf("config.json.pre-<version> files = %v, want exactly one", backups)
	}
	if got := w.readFile(backups[0]); got != old {
		t.Errorf("the backup is not the old config.json:\n%s", got)
	}

	// Every source is on the first fleet; freeform and the resume source are gone;
	// the rest is kept.
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(w.readFile(configPath(w.home))), &cfg); err != nil {
		t.Fatalf("config.json is not JSON after the migration: %v", err)
	}
	var oldCfg map[string]json.RawMessage
	_ = json.Unmarshal([]byte(old), &oldCfg)
	srcs := w.configSources()
	if len(srcs) != 2 || srcs["tracker"] == nil || srcs["github"] == nil {
		t.Fatalf("migrated sources = %v, want tracker and github only", srcs)
	}
	for key, s := range srcs {
		if s["fleet"] != "zulu" {
			t.Errorf("source %s fleet = %v, want zulu (the first fleet in the file)", key, s["fleet"])
		}
	}
	if strings.Contains(w.readFile(configPath(w.home)), "resume_source") {
		t.Errorf("autopilot.resume_source still names the removed source:\n%s", w.readFile(configPath(w.home)))
	}
	for _, k := range []string{"version", "x_future", "favorite_skills", "fleets"} {
		if !bytes.Equal(compact(t, cfg[k]), compact(t, oldCfg[k])) {
			t.Errorf("%q changed: %s, was %s", k, cfg[k], oldCfg[k])
		}
	}
	if !strings.Contains(string(cfg["autopilot"]), `"max_ships"`) {
		t.Errorf("the other autopilot keys were dropped: %s", cfg["autopilot"])
	}
	if _, err := os.Stat(filepath.Join(w.dataDir("notes"), "orders.json")); err != nil {
		t.Errorf("the removed source's data directory was not left on disk: %v", err)
	}

	// One notice, from Admiral, that says what happened.
	if notice.Session != "_admiral" || notice.Name != "Admiral" {
		t.Errorf("notice session %q name %q, want _admiral and Admiral", notice.Session, notice.Name)
	}
	body := notice.Notice.Body
	// A multi-line order's continuation lines are indented under its list item.
	indented := strings.ReplaceAll(orderOne, "\n", "\n    ")
	for _, want := range []string{filepath.Base(backups[0]), indented, orderTwo, "tracker", "Zulu", "notes"} {
		if !strings.Contains(body, want) {
			t.Errorf("notice body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, decoy) {
		t.Errorf("the notice copied a source setting:\n%s", body)
	}
	if strings.Contains(strings.ToLower(body), "notification") {
		t.Errorf("the notice says notification, the word is notice:\n%s", body)
	}

	// A restart changes nothing and posts nothing.
	cfgBefore := w.readFile(configPath(w.home))
	w.restart()
	time.Sleep(1500 * time.Millisecond)
	if got := w.readFile(configPath(w.home)); got != cfgBefore {
		t.Errorf("a restart rewrote config.json:\n%s", got)
	}
	if got := w.backups(); !reflect.DeepEqual(got, backups) || w.readFile(backups[0]) != old {
		t.Errorf("a restart changed the backup: %v", got)
	}
	if n := w.in.notices(t); len(n) != 1 || n[0].ID != notice.ID {
		t.Errorf("after a restart the notices are %+v, want only the first one", n)
	}
}

func compact(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	b, _ := json.Marshal(v)
	return b
}

// A file that is invalid for another reason is not touched: no backup, no
// notice, and the error banner shows.
func TestMigrateLeavesAnOtherwiseInvalidConfig(t *testing.T) {
	w := newWorld(t)
	bad := `{"version":1,"fleets":[],"sources":{"notes":{"plugin":"freeform"}}}`
	writeConfig(t, w.home, bad)
	w.seedFreeformOrders("notes", orderOne)
	w.start()
	time.Sleep(1500 * time.Millisecond)

	if got := w.readFile(configPath(w.home)); got != bad {
		t.Errorf("an invalid config was rewritten: %q", got)
	}
	if b := w.backups(); len(b) != 0 {
		t.Errorf("a backup was written for a config that cannot be migrated: %v", b)
	}
	if n := w.in.notices(t); len(n) != 0 {
		t.Errorf("a notice was posted: %+v", n)
	}
	cfg, _ := fetchConfig(t, w.in.instance)
	if cfg.Error == nil {
		t.Errorf("GET /api/config has no error, want the banner for the invalid file")
	}
}

// With no fleets there is nothing to move a source to: they are removed, and
// the notice names them (the backup keeps them).
func TestMigrateWithNoFleetsRemovesTheSources(t *testing.T) {
	w := newWorld(t)
	d := w.orderSrcDef("tracker", "Tracker")
	settings, _ := json.Marshal(d.Settings)
	old := `{"version":1,"fleets":{},"sources":{"tracker":{"plugin":"testplug","name":"Tracker","settings":` + string(settings) + `}}}`
	writeConfig(t, w.home, old)
	w.start()

	notice := w.in.waitNotice(t)
	if got := w.configSources(); len(got) != 0 {
		t.Errorf("migrated sources = %v, want none", got)
	}
	if b := w.backups(); len(b) != 1 || w.readFile(b[0]) != old {
		t.Errorf("backups = %v, want one holding the old config", b)
	}
	if !strings.Contains(notice.Notice.Body, "tracker") {
		t.Errorf("notice body does not name the removed source:\n%s", notice.Notice.Body)
	}
}

// A command other than the server does not migrate: it refuses and says so.
func TestCommandsRefuseAnUnmigratedConfig(t *testing.T) {
	w := newWorld(t)
	old := w.oldConfig()
	writeConfig(t, w.home, old)
	for _, args := range [][]string{{"fleet", "list"}, {"config", "validate"}} {
		r := w.admiral(args...)
		if r.code == 0 || !strings.Contains(r.stderr, "start Admiral once") {
			t.Errorf("admiral %v: exit %d, stderr %q, want a refusal saying to start Admiral once", args, r.code, r.stderr)
		}
	}
	if got := w.readFile(configPath(w.home)); got != old {
		t.Errorf("a command changed config.json:\n%s", got)
	}
	if b := w.backups(); len(b) != 0 {
		t.Errorf("a command wrote a backup: %v", b)
	}
}
