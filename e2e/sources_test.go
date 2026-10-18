package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Order sources (u7): config validation, the fresh install, the real freeform
// plugin and the plugins API, observed over HTTP, the WebSocket and the files
// in $ADMIRAL_HOME. The misbehaving plugin scenarios are in
// sources_plugin_test.go.

func sortedCopy(s []string) []string {
	c := append([]string{}, s...)
	sort.Strings(c)
	return c
}

func (w *world) configSources() map[string]map[string]any {
	w.t.Helper()
	var cfg struct {
		Sources map[string]map[string]any `json:"sources"`
	}
	if err := json.Unmarshal([]byte(w.readFile(configPath(w.home))), &cfg); err != nil {
		w.t.Fatalf("config.json is not JSON: %v\n%s", err, w.readFile(configPath(w.home)))
	}
	return cfg.Sources
}

func (w *world) panelID(key string) string {
	w.t.Helper()
	s, ok := find(w.sources(), key)
	if !ok || len(s.Panels) == 0 {
		w.t.Fatalf("source %q has no panel: %s", key, dump(w.sources()))
	}
	return s.Panels[0].ID
}

// S1: a fresh install has the freeform plugin written, the "orders" source in
// config.json and one empty "Orders" panel.
func TestFreshInstall(t *testing.T) {
	w := newWorld(t)
	w.startFresh()

	var man struct {
		Name    string `json:"name"`
		Command string `json:"command"`
	}
	raw := w.readFile(filepath.Join(w.home, "plugins", "freeform", "plugin.json"))
	if err := json.Unmarshal([]byte(raw), &man); err != nil || man.Name != "freeform" || man.Command == "" {
		t.Fatalf("plugins/freeform/plugin.json must be a manifest named freeform with a command, got %q (%v)", raw, err)
	}
	want := map[string]any{"plugin": "freeform", "name": "Orders"}
	if got := w.configSources()["orders"]; !reflect.DeepEqual(got, want) {
		t.Errorf(`config.json sources.orders = %v, want %v (written through the write API on the first start)`, got, want)
	}

	s := w.waitSource("orders", 10*time.Second, "state ok", isOK)
	if s.Name != "Orders" || s.Plugin != "freeform" {
		t.Errorf("source = %s, want name Orders, plugin freeform", dump(s))
	}
	if !reflect.DeepEqual(sortedCopy(s.Capabilities), []string{"add", "edit", "remove"}) {
		t.Errorf("freeform capabilities = %v, want add, edit, remove", s.Capabilities)
	}
	if len(s.Panels) != 1 || s.Panels[0].Title != "Orders" || len(s.Panels[0].Orders) != 0 {
		t.Errorf(`freeform panels = %s, want one empty "Orders" panel`, dump(s.Panels))
	}
	if s.IconURL == "" {
		t.Errorf("source has no icon_url")
	}

	code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/plugins", nil)
	if code != http.StatusOK || !strings.Contains(body, `"freeform"`) {
		t.Errorf("GET /api/plugins = %d %s, want 200 listing freeform", code, body)
	}
}

// S1: when sources are left alone, and what a restart does to the plugin folder.
func TestFreshInstallLeavesWhatItShould(t *testing.T) {
	t.Run("an empty sources object gets no source added", func(t *testing.T) {
		w := newWorld(t)
		writeConfig(t, w.home, `{"version":1,"fleets":{},"sources":{}}`)
		w.start()
		w.waitSources(5*time.Second, "an empty list", func(l []sourceJSON) bool { return len(l) == 0 })
		if got := w.configSources(); len(got) != 0 {
			t.Errorf("config sources = %v, want {} left alone", got)
		}
	})
	t.Run("an invalid config gets nothing added", func(t *testing.T) {
		w := newWorld(t)
		bad := `{"version":1,"fleets":[]}`
		writeConfig(t, w.home, bad)
		w.start()
		if got := w.readFile(configPath(w.home)); got != bad {
			t.Errorf("an invalid config at startup was rewritten: %q", got)
		}
	})
	t.Run("a sources key deleted by hand is not undone until the next start", func(t *testing.T) {
		w := newWorld(t)
		w.startFresh()
		w.waitSource("orders", 10*time.Second, "state ok", isOK)
		replaceConfig(t, w.home, `{"version":1,"fleets":{}}`)
		w.waitSources(10*time.Second, "no sources after the hand edit", func(l []sourceJSON) bool { return len(l) == 0 })
		time.Sleep(1500 * time.Millisecond)
		if got := w.readFile(configPath(w.home)); strings.Contains(got, "sources") {
			t.Errorf("a reload re-added sources: %s", got)
		}
		w.in.Stop()
		w.startFresh()
		w.waitSource("orders", 10*time.Second, "the orders source back after a start", isOK)
	})
	t.Run("an edited plugin folder is left alone and a missing one is written again", func(t *testing.T) {
		w := newWorld(t)
		w.startFresh()
		w.waitSource("orders", 10*time.Second, "state ok", isOK)
		manifest := filepath.Join(w.home, "plugins", "freeform", "plugin.json")
		var m map[string]any
		if err := json.Unmarshal([]byte(w.readFile(manifest)), &m); err != nil {
			t.Fatal(err)
		}
		m["description"] = "edited by the engineer"
		writeJSON(t, manifest, m)
		w.in.Stop()
		w.startFresh()
		w.waitSource("orders", 10*time.Second, "state ok", isOK)
		if got := w.readFile(manifest); !strings.Contains(got, "edited by the engineer") {
			t.Errorf("an edited freeform folder was overwritten: %s", got)
		}
		w.in.Stop()
		if err := os.RemoveAll(filepath.Join(w.home, "plugins", "freeform")); err != nil {
			t.Fatal(err)
		}
		w.startFresh()
		w.waitSource("orders", 10*time.Second, "state ok", isOK)
		if got := w.readFile(manifest); got == "" || strings.Contains(got, "edited by the engineer") {
			t.Errorf("a missing freeform folder was not written again: %q", got)
		}
	})
}

// Sources in config.json are validated by the config parser: an invalid file
// keeps the banner, with the path of the fault.
func TestSourcesConfigValidation(t *testing.T) {
	cases := []struct{ name, config, want string }{
		{"sources null", `{"version":1,"fleets":{},"sources":null}`, "sources"},
		{"sources an array", `{"version":1,"fleets":{},"sources":[]}`, "sources"},
		{"bad source key", `{"version":1,"fleets":{},"sources":{"my source":{"plugin":"freeform"}}}`, "my source"},
		{"unknown key in a source", `{"version":1,"fleets":{},"sources":{"s":{"plugin":"freeform","colour":"red"}}}`, "sources.s.colour"},
		{"plugin missing", `{"version":1,"fleets":{},"sources":{"s":{"name":"S"}}}`, "sources.s.plugin"},
		{"settings not an object", `{"version":1,"fleets":{},"sources":{"s":{"plugin":"freeform","settings":[]}}}`, "sources.s.settings"},
		{"duplicate source key", `{"version":1,"fleets":{},"sources":{"dupkey":{"plugin":"freeform"},"dupkey":{"plugin":"freeform"}}}`, "dupkey"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			writeConfig(t, w.home, c.config)
			w.start()
			code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/config", nil)
			if code != http.StatusOK {
				t.Fatalf("GET /api/config = %d: %s", code, body)
			}
			var cfg cfgBody
			if err := json.Unmarshal([]byte(body), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Error == nil || !strings.Contains(cfg.Error.Message, c.want) {
				t.Errorf("config error = %+v, want a banner naming %q", cfg.Error, c.want)
			}
		})
	}
}

// S2: add, edit and remove multi-line orders in the freeform list, through the
// source API, and see them survive a restart.
func TestFreeformOrders(t *testing.T) {
	w := newWorld(t)
	w.startFresh()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	panel := w.panelID("orders")
	ws := w.ws()
	waitSourcesMsg(t, ws, 5*time.Second, "the orders source on connect", func(l []sourceJSON) bool {
		_, ok := find(l, "orders")
		return ok
	})
	store := filepath.Join(w.dataDir("orders"), "orders.json")

	text := "\n\n  Fix the flaky test  \nsecond line\n\tindented"
	code, body := w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": text})
	w.mustOK(code, body, "POST /api/sources/orders/orders")
	var first orderJSON
	waitSourcesMsg(t, ws, 5*time.Second, "the new order pushed to the client", func(l []sourceJSON) bool {
		s, _ := find(l, "orders")
		for _, p := range s.Panels {
			if len(p.Orders) == 1 {
				first = p.Orders[0]
				return true
			}
		}
		return false
	})
	if first.ID == "" || first.Title != "Fix the flaky test" || first.Text != text {
		t.Errorf("order = %+v, want title %q (first non-empty line, trimmed) and the whole text %q", first, "Fix the flaky test", text)
	}
	if got := w.readFile(store); !strings.Contains(got, "Fix the flaky test") {
		t.Errorf("data/orders/orders.json = %q, want it to hold the order", got)
	}

	code, body = w.srcReq(http.MethodPut, "/orders/orders/"+first.ID, map[string]any{"text": "Rewrite the parser\nwith care"})
	w.mustOK(code, body, "PUT /api/sources/orders/orders/<id>")
	w.waitSource("orders", 5*time.Second, "the edited order", func(s sourceJSON) bool {
		o, ok := s.order(first.ID)
		return ok && o.Title == "Rewrite the parser" && o.Text == "Rewrite the parser\nwith care"
	})
	if got := w.readFile(store); strings.Contains(got, "flaky") || !strings.Contains(got, "Rewrite the parser") {
		t.Errorf("data/orders/orders.json = %q, want the edit and not the old text", got)
	}

	// limits: empty after trimming and over 12,000 bytes are refused with a message;
	// exactly 12,000 bytes is fine.
	for name, bad := range map[string]string{"blank": " \n\t ", "too long": strings.Repeat("a", 12001)} {
		code, body = w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": bad})
		if code/100 != 4 || strings.TrimSpace(body) == "" {
			t.Errorf("POST of a %s order = %d %q, want 4xx with a message", name, code, body)
		}
		code, body = w.srcReq(http.MethodPut, "/orders/orders/"+first.ID, map[string]any{"text": bad})
		if code/100 != 4 || strings.TrimSpace(body) == "" {
			t.Errorf("PUT of a %s text = %d %q, want 4xx with a message", name, code, body)
		}
	}
	code, body = w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": strings.Repeat("b", 12000)})
	w.mustOK(code, body, "POST of exactly 12,000 bytes")
	s := w.waitSource("orders", 5*time.Second, "two orders", func(s sourceJSON) bool { return len(s.orderIDs()) == 2 })
	if o, _ := s.order(first.ID); o.Title != "Rewrite the parser" {
		t.Errorf("a refused edit changed the order: %+v", o)
	}

	// it survives a restart with the same ids
	before := sortedCopy(s.orderIDs())
	w.restart()
	s = w.waitSource("orders", 10*time.Second, "both orders after a restart", func(s sourceJSON) bool { return len(s.orderIDs()) == 2 })
	if !reflect.DeepEqual(sortedCopy(s.orderIDs()), before) {
		t.Errorf("order ids after a restart = %v, want %v", s.orderIDs(), before)
	}

	code, body = w.srcReq(http.MethodDelete, "/orders/orders/"+first.ID, nil)
	w.mustOK(code, body, "DELETE /api/sources/orders/orders/<id>")
	w.waitSource("orders", 5*time.Second, "the order removed", func(s sourceJSON) bool { return !s.hasOrder(first.ID) && len(s.orderIDs()) == 1 })
	if got := w.readFile(store); strings.Contains(got, "Rewrite the parser") {
		t.Errorf("data/orders/orders.json still holds the removed order: %q", got)
	}
}

// S3: a launch from a freeform order carries the full text and the plugin
// deletes the order afterwards.
func TestFreeformLaunch(t *testing.T) {
	w := newWorld(t)
	writeConfig(t, w.home, w.fleetsConfig()) // fleets and no sources key: the fresh install adds the source
	w.startFresh()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	panel := w.panelID("orders")
	text := "Fix the flaky test\n\n\tsee the CI log\nthen rerun it"
	code, body := w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": text})
	w.mustOK(code, body, "add order")
	s := w.waitSource("orders", 5*time.Second, "the order", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 })
	id := s.orderIDs()[0]

	// the client's own text is ignored when a source names the order
	code, body = send(t, http.MethodPost, w.in.LocalURL+"/api/ships", map[string]any{
		"fleet": "sail", "harness": "claude", "dir": w.checkout("proj"), "skill": noSkill,
		"source": "orders", "order_id": id, "text": "NOT THE ORDER"})
	if code != http.StatusCreated {
		t.Fatalf("POST /api/ships = %d, want 201: %s", code, body)
	}
	ship := decodeShip(t, body)
	if ship.Source != "orders" || ship.OrderID != id {
		t.Errorf("ship source/order = %q/%q, want orders/%s", ship.Source, ship.OrderID, id)
	}
	if got := promptOf(t, w, w.record(ship.Session)); got != text {
		t.Errorf("the harness prompt = %q, want the order's whole text %q", got, text)
	}
	// the fake exits 0 and the ship sails off; the order must not come back
	w.in.waitShipsList(t, 10*time.Second, "the ship gone", func(l []shipJSON) bool { return len(l) == 0 })
	w.waitSource("orders", 5*time.Second, "no orders left", func(s sourceJSON) bool { return len(s.orderIDs()) == 0 })
	if got := w.readFile(filepath.Join(w.dataDir("orders"), "orders.json")); strings.Contains(got, "Fix the flaky test") {
		t.Errorf("orders.json still holds the launched order: %q", got)
	}
}

// Plugins on disk: the list, the icon and its path-traversal guard, and a
// manifest whose name differs from its folder.
func TestPluginsAPI(t *testing.T) {
	w := newWorld(t)
	w.installPlugin("testplug", "testplug")
	w.installPlugin("mismatch", "other")
	trav := w.installPlugin("traversal", "traversal")
	if err := os.WriteFile(filepath.Join(w.home, "secret.svg"), []byte("SECRET-MARKER"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(trav, "plugin.json"), map[string]any{"name": "traversal", "description": "d",
		"command": "./plugin.py", "args": []string{}, "icon": "../../secret.svg"})
	writeConfig(t, w.home, w.srcConfig())
	w.start()

	code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/plugins", nil)
	if code != http.StatusOK || !strings.Contains(body, `"testplug"`) || !strings.Contains(body, "Test plugin testplug") {
		t.Errorf("GET /api/plugins = %d %s, want 200 listing testplug with its description", code, body)
	}

	resp, icon := get(t, w.in.LocalURL+"/api/plugins/testplug/icon", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(icon, "<svg") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/svg+xml") {
		t.Errorf("GET icon = %d %q (%s), want the svg served as image/svg+xml", resp.StatusCode, icon, resp.Header.Get("Content-Type"))
	}
	for _, p := range []string{"/api/plugins/traversal/icon", "/api/plugins/%2e%2e/icon", "/api/plugins/nosuch/icon"} {
		resp, body := get(t, w.in.LocalURL+p, nil)
		if resp.StatusCode == http.StatusOK || strings.Contains(body, "SECRET-MARKER") {
			t.Errorf("GET %s = %d %q, want a refusal that does not serve files outside the plugin folder", p, resp.StatusCode, body)
		}
	}

	for _, plugin := range []string{"mismatch", "nosuch"} {
		code, body := w.srcReq(http.MethodPost, "", map[string]any{"plugin": plugin})
		if code/100 != 4 {
			t.Errorf("POST /api/sources with plugin %q = %d %s, want 4xx (invalid or missing plugin)", plugin, code, body)
		}
	}
}
