package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Autopilot config and launch plumbing (#114, u1): the config's last-good rule
// for an autopilot entry, the sources writes keeping the autopilot object, a
// hand launch never carrying Admiral's autopilot variables, and one launch at
// a time per order.

// AC 6: harness other than claude fails validation and the last good config stays.
func TestAutopilotCodexEntryKeepsLastGoodConfig(t *testing.T) {
	home := newHome(t)
	entry := func(harness string) string {
		return `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},"sources":{"s":{"plugin":"freeform",` +
			`"autopilot":{"p":{"fleet":"work","harness":"` + harness + `","skill":{"kind":"none"}}}}}}`
	}
	// The good config has no autopilot entry, so it loads before and after u1.
	writeConfig(t, home, `{"version":1,"fleets":{"work":{"name":"Work","claude":{}}},"sources":{"s":{"plugin":"freeform"}}}`)
	in := startAdmiral(t, home, "127.0.0.1:0")
	waitAPI(t, in, "the good config", func(b cfgBody) bool { return b.Error == nil && reflect.DeepEqual(b.keys(), []string{"work"}) })

	writeConfig(t, home, entry("codex"))
	b := waitAPI(t, in, "an error", hasErr)
	if !strings.Contains(b.Error.Message, "autopilot runs on Claude Code only for now") {
		t.Errorf("error = %q, want the Claude Code only message", b.Error.Message)
	}
	if !reflect.DeepEqual(b.keys(), []string{"work"}) {
		t.Errorf("fleets while invalid = %v, want the last good [work]", b.keys())
	}
}

// The sources writes rebuild every source from its parsed form, so a save of
// one source, and the delete of another, must not rewrite an autopilot object.
func TestSourceWritesKeepTheAutopilotObject(t *testing.T) {
	w := newWorld(t)
	a, b := w.newTP("a"), w.newTP("b")
	ap := `{ "prs": {"skill": {"kind":"none"}, "harness":"claude", "fleet":"stay"} }`
	srcs := func(def srcDef) string {
		raw, _ := json.Marshal(def.Settings)
		return `"` + def.Key + `":{"plugin":"testplug","settings":` + string(raw) + `,"autopilot":` + ap + `}`
	}
	w.installPlugin("testplug", "testplug")
	writeConfig(t, w.home, strings.TrimSuffix(w.fleetsConfig(), "}")+`,"sources":{`+
		srcs(tpSource("a", a, nil))+`,`+srcs(tpSource("b", b, nil))+`}}`)
	w.start()
	w.waitSource("a", 10*time.Second, "state ok", isOK)

	var want bytes.Buffer
	if err := json.Compact(&want, []byte(ap)); err != nil {
		t.Fatal(err)
	}
	check := func(when, key string) {
		t.Helper()
		var cfg struct {
			Sources map[string]struct {
				Autopilot json.RawMessage `json:"autopilot"`
			} `json:"sources"`
		}
		raw, _ := os.ReadFile(configPath(w.home))
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("config.json is not JSON: %v", err)
		}
		var got bytes.Buffer
		if err := json.Compact(&got, cfg.Sources[key].Autopilot); err != nil || got.String() != want.String() {
			t.Errorf("after %s, sources.%s.autopilot = %s, want %s", when, key, cfg.Sources[key].Autopilot, want.String())
		}
	}
	code, body := w.srcReq(http.MethodPut, "/a", map[string]any{"name": "Renamed", "settings": a.settings(nil)})
	w.mustOK(code, body, "settings save")
	check("a settings save", "a")
	check("a settings save", "b")
	code, body = w.srcReq(http.MethodDelete, "/b", nil)
	w.mustOK(code, body, "delete b")
	check("deleting another source", "a")
}

// AC 8, hand half: Admiral owns ADMIRAL_AUTOPILOT and ADMIRAL_LOW_STAKES, so
// a launch by hand never has them, whether the fleet's env or Admiral's own
// environment (an Admiral running inside an autopilot ship) sets them.
func TestHandLaunchHasNoAutopilotVariables(t *testing.T) {
	vars := []string{"ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES"}
	check := func(t *testing.T, w *world) {
		t.Helper()
		ship := w.launch(w.order("work", "claude", "hand", noSkill, "x"))
		r := w.record(ship.Session)
		for _, v := range vars {
			if val, ok := r.env[v]; ok {
				t.Errorf("a ship launched by hand has %s=%q, want it unset", v, val)
			}
		}
	}
	t.Run("set by the fleet's env", func(t *testing.T) {
		w := newWorld(t)
		env := w.harnessEnv()
		env["ADMIRAL_AUTOPILOT"], env["ADMIRAL_LOW_STAKES"] = "1", "never"
		cfg, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{"work": map[string]any{
			"name": "Work", "search_roots": []string{w.work}, "claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}}})
		writeConfig(t, w.home, string(cfg))
		w.start()
		check(t, w)
	})
	t.Run("set in Admiral's environment", func(t *testing.T) {
		w := newWorld(t)
		w.extra = []string{"ADMIRAL_AUTOPILOT=1", "ADMIRAL_LOW_STAKES=advisor"}
		w.start()
		check(t, w)
	})
}

// Two launches of one order that overlap: the order is hidden only once its
// ship exists, so the refusal must hold the order while a launch runs.
func TestConcurrentLaunchesOfOneOrder(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(ord("o1", "First"))
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "an order", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 })
	dir := w.checkout("proj")

	const n = 4
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			code, _ := send(t, http.MethodPost, w.in.LocalURL+"/api/ships", map[string]any{
				"fleet": "stay", "harness": "claude", "dir": dir, "skill": noSkill, "source": "a", "order_id": "o1"})
			codes <- code
		}()
	}
	got := map[int]int{}
	for i := 0; i < n; i++ {
		got[<-codes]++
	}
	if got[http.StatusCreated] != 1 || got[http.StatusConflict] != n-1 {
		t.Errorf("%d overlapping launches of one order gave statuses %v, want one 201 and %d 409", n, got, n-1)
	}
}
