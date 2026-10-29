package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
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

// decodeID is the id in an order-add response.
func decodeID(body string) string {
	var r struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &r)
	return r.ID
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

	// The GitHub plugin is written and listed too, and is not made a source.
	var gh struct {
		Name string `json:"name"`
	}
	raw = w.readFile(filepath.Join(w.home, "plugins", "github", "plugin.json"))
	if err := json.Unmarshal([]byte(raw), &gh); err != nil || gh.Name != "github" {
		t.Fatalf("plugins/github/plugin.json must be a manifest named github, got %q (%v)", raw, err)
	}
	var listed []struct {
		Name  string `json:"name"`
		Valid bool   `json:"valid"`
	}
	_ = json.Unmarshal([]byte(body), &listed)
	if !slices.ContainsFunc(listed, func(p struct {
		Name  string `json:"name"`
		Valid bool   `json:"valid"`
	}) bool {
		return p.Name == "github" && p.Valid
	}) {
		t.Errorf("GET /api/plugins = %s, want github listed as valid", body)
	}
	if got := w.configSources(); len(got) != 1 {
		t.Errorf("config.json sources = %v, want only orders: GitHub is a plugin to add, not a source", got)
	}
	if list := w.sources(); len(list) != 1 {
		t.Errorf("GET /api/sources lists %s, want only orders", keysOf(list))
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
	for _, name := range embeddedPlugins {
		t.Run("an edited "+name+" folder is left alone and a missing one is written again", func(t *testing.T) {
			w := newWorld(t)
			w.startFresh()
			w.waitSource("orders", 10*time.Second, "state ok", isOK)
			manifest := filepath.Join(w.home, "plugins", name, "plugin.json")
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
				t.Errorf("an edited %s folder was overwritten: %s", name, got)
			}
			w.in.Stop()
			if err := os.RemoveAll(filepath.Join(w.home, "plugins", name)); err != nil {
				t.Fatal(err)
			}
			w.startFresh()
			w.waitSource("orders", 10*time.Second, "state ok", isOK)
			if got := w.readFile(manifest); got == "" || strings.Contains(got, "edited by the engineer") {
				t.Errorf("a missing %s folder was not written again: %q", name, got)
			}
		})
	}
}

// embeddedPlugins are the plugins Admiral ships inside the binary and installs.
var embeddedPlugins = []string{"freeform", "github"}

// stampOf is the hash Admiral keeps in plugins/<name>/.admiral-stamp: SHA-256
// over the files sorted by path, each as path, a zero byte, content, a zero byte.
func stampOf(files map[string]string) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n + "\x00" + files[n] + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// S1: a newer embedded copy replaces a plugin folder that still matches its
// stamp, and leaves one that was edited after the stamp was written. Both
// plugins Admiral ships, freeform and github, follow the rule.
func TestPluginUpgrade(t *testing.T) {
	for _, name := range embeddedPlugins {
		t.Run(name, func(t *testing.T) { testPluginUpgrade(t, name) })
	}
}

func testPluginUpgrade(t *testing.T, name string) {
	writeOld := func(t *testing.T, w *world, stampedAs map[string]string, onDisk map[string]string) {
		dir := filepath.Join(w.home, "plugins", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range onDisk {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, ".admiral-stamp"), []byte(stampOf(stampedAs)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := map[string]string{
		"plugin.json": `{"name":"` + name + `","description":"an older copy","command":"python3","args":["plugin.py"]}`,
		"plugin.py":   "# an older copy\n",
		"notes.txt":   "a file the newer copy no longer has",
	}
	t.Run("an unedited older copy is replaced", func(t *testing.T) {
		w := newWorld(t)
		// Finder leaves a .DS_Store; go:embed never carried one, so it is not an edit.
		withFinder := map[string]string{".DS_Store": "finder"}
		for k, v := range old {
			withFinder[k] = v
		}
		writeOld(t, w, old, withFinder)
		w.startFresh()
		w.waitSource("orders", 10*time.Second, "state ok", isOK)
		dir := filepath.Join(w.home, "plugins", name)
		if got := w.readFile(filepath.Join(dir, "plugin.json")); strings.Contains(got, "an older copy") {
			t.Errorf("an unedited older "+name+" folder was not replaced: %s", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err == nil {
			t.Errorf("a file of the older copy is still in the folder")
		}
		if got := w.readFile(filepath.Join(dir, ".admiral-stamp")); got == stampOf(old) || len(got) != 64 {
			t.Errorf("the stamp is %q, want the hash of the newer copy", got)
		}
	})
	t.Run("an older copy edited after its stamp is left alone", func(t *testing.T) {
		w := newWorld(t)
		edited := map[string]string{}
		for k, v := range old {
			edited[k] = v
		}
		edited["plugin.py"] = "# edited by the engineer\n"
		writeOld(t, w, old, edited)
		w.startFresh()
		dir := filepath.Join(w.home, "plugins", name)
		if got := w.readFile(filepath.Join(dir, "plugin.py")); got != edited["plugin.py"] {
			t.Errorf("an edited "+name+" folder was replaced: plugin.py = %q", got)
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
	// pasted terminal output must not get in and freeze the panel
	code, body = w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": "red \x1b[31mtext\x1b[0m"})
	if code/100 != 4 || !strings.Contains(body, "control character") {
		t.Errorf("POST of an order with an escape code = %d %q, want 4xx saying control characters are refused", code, body)
	}
	code, body = w.srcReq(http.MethodPut, "/orders/orders/"+first.ID, map[string]any{"text": "x\x07y"})
	if code/100 != 4 {
		t.Errorf("PUT of a text with a bell = %d %q, want 4xx", code, body)
	}
	code, body = w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": "crlf\r\ntext\tis fine"})
	w.mustOK(code, body, "POST of CRLF text")
	crlfID := decodeID(body)
	// the remove route looks the id up in the source's last list, which the add only asks to refresh
	w.waitSource("orders", 5*time.Second, "the CRLF order", func(s sourceJSON) bool { return s.hasOrder(crlfID) })
	code, body = w.srcReq(http.MethodDelete, "/orders/orders/"+crlfID, nil)
	w.mustOK(code, body, "DELETE of the CRLF order")
	code, body = w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": strings.Repeat("b", 12000)})
	w.mustOK(code, body, "POST of exactly 12,000 bytes")
	s := w.waitSource("orders", 5*time.Second, "two orders", func(s sourceJSON) bool { return len(s.orderIDs()) == 2 && !s.hasOrder(crlfID) })
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

// The settings view hands the form its starting values, and a stored password
// never leaves the server: the form gets a marker, and saving the marker back
// keeps the stored value.
func TestSourceSettingsHidePasswords(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("pw")
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"flagdir": map[string]any{"type": "string"},
		"token":   map[string]any{"type": "string", "format": "password"},
		"empty":   map[string]any{"type": "string", "format": "password"},
	}}
	w.startSources(tpSource("pw", p, map[string]any{"settingsSchema": schema, "token": "s3cret"}))
	w.waitSource("pw", 10*time.Second, "state ok", isOK)

	view := func() (string, map[string]any) {
		code, body := w.srcReq(http.MethodGet, "/pw/settings", nil)
		if code != http.StatusOK {
			t.Fatalf("GET settings = %d %s, want 200", code, body)
		}
		var v struct {
			Name     string         `json:"name"`
			Settings map[string]any `json:"settings"`
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("settings view is not JSON: %v\n%s", err, body)
		}
		return v.Name, v.Settings
	}
	stored := func() string {
		raw, err := os.ReadFile(filepath.Join(w.home, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	name, settings := view()
	if name != "Test pw" || settings["flagdir"] != p.dir || settings["token"] != "__admiral_unchanged__" {
		t.Errorf("settings view = %q %v, want the name, the flagdir, the marker for the stored token and nothing for the unset one", name, settings)
	}
	if _, there := settings["empty"]; there {
		t.Errorf("settings view = %v, want a password that was never set to stay absent", settings)
	}
	if code, _ := w.srcReq(http.MethodGet, "/nosuch/settings", nil); code != http.StatusNotFound {
		t.Errorf("GET settings of an unknown source = %d, want 404", code)
	}

	// Saving the marker back keeps the stored secret.
	keep := map[string]any{"flagdir": p.dir, "token": "__admiral_unchanged__", "settingsSchema": schema}
	if code, body := w.srcReq(http.MethodPut, "/pw", map[string]any{"settings": keep}); code != http.StatusOK {
		t.Fatalf("PUT with the marker = %d %s, want 200", code, body)
	}
	if !strings.Contains(stored(), "s3cret") || strings.Contains(stored(), "__admiral_unchanged__") {
		t.Errorf("after a save with the marker config.json = %s, want the stored token kept and no marker", stored())
	}

	// A new value replaces it.
	keep["token"] = "n3w"
	if code, body := w.srcReq(http.MethodPut, "/pw", map[string]any{"settings": keep}); code != http.StatusOK {
		t.Fatalf("PUT with a new token = %d %s, want 200", code, body)
	}
	if !strings.Contains(stored(), "n3w") || strings.Contains(stored(), "s3cret") {
		t.Errorf("after a save with a new token config.json = %s, want n3w and no s3cret", stored())
	}
}

// A secret never reaches the page when the schema is not known (the source
// crashed before it answered initialize) or when it sits inside an object, and
// a save cannot write the marker with text added to it.
func TestSourceSettingsMaskUnknownAndNestedSecrets(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("nest")
	p.put("crash-on-initialize", "")
	nested := map[string]any{"type": "object", "properties": map[string]any{
		"flagdir": map[string]any{"type": "string"},
		"auth": map[string]any{"type": "object", "properties": map[string]any{
			"token": map[string]any{"type": "string", "format": "password"},
			"user":  map[string]any{"type": "string"},
		}},
	}}
	w.startSources(tpSource("nest", p, map[string]any{
		"settingsSchema": nested, "auth": map[string]any{"token": "s3cret", "user": "me"},
	}))
	w.waitSource("nest", 10*time.Second, "state error", func(s sourceJSON) bool { return s.State == "error" })

	code, body := w.srcReq(http.MethodGet, "/nest/settings", nil)
	if code != http.StatusOK || strings.Contains(body, "s3cret") || strings.Contains(body, "\"me\"") {
		t.Fatalf("GET settings of a source with no schema = %d %s, want 200 with every string masked", code, body)
	}

	p.clear("crash-on-initialize")
	if code, body := w.srcReq(http.MethodPost, "/nest/restart", nil); code != http.StatusOK {
		t.Fatalf("restart = %d %s", code, body)
	}
	w.waitSource("nest", 10*time.Second, "state ok", isOK)
	code, body = w.srcReq(http.MethodGet, "/nest/settings", nil)
	if code != http.StatusOK || strings.Contains(body, "s3cret") || !strings.Contains(body, `"user":"me"`) {
		t.Errorf("GET settings with the schema known = %d %s, want only the nested password masked", code, body)
	}

	bad := map[string]any{"flagdir": p.dir, "settingsSchema": nested, "auth": map[string]any{"token": "__admiral_unchanged__n3w"}}
	if code, _ := w.srcReq(http.MethodPut, "/nest", map[string]any{"settings": bad}); code != http.StatusBadRequest {
		t.Errorf("PUT with text added to the marker = %d, want 400", code)
	}
	keep := map[string]any{"flagdir": p.dir, "settingsSchema": nested, "auth": map[string]any{"token": "__admiral_unchanged__", "user": "me"}}
	if code, body := w.srcReq(http.MethodPut, "/nest", map[string]any{"settings": keep}); code != http.StatusOK {
		t.Fatalf("PUT with the nested marker = %d %s, want 200", code, body)
	}
	raw, err := os.ReadFile(filepath.Join(w.home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "s3cret") || strings.Contains(string(raw), "__admiral_unchanged__") {
		t.Errorf("config.json = %s, want the nested token kept and no marker", raw)
	}
}

// The settings view fails closed: a value comes back in clear only when the
// schema declares it as a plain string or number, however the schema is wrong,
// missing or shaped.
func TestSourceSettingsFailClosed(t *testing.T) {
	w := newWorld(t)
	cases := []struct {
		key    string
		extra  map[string]any
		crash  bool
		secret string
	}{
		{"noschema", map[string]any{"noSettingsSchema": true, "token": "s3cretA"}, false, "s3cretA"},
		{"notobject", map[string]any{"settingsSchema": "not an object", "token": "s3cretB"}, false, "s3cretB"},
		{"items", map[string]any{"settingsSchema": map[string]any{"type": "object", "properties": map[string]any{
			"flagdir":  map[string]any{"type": "string"},
			"accounts": map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "password"}},
		}}, "accounts": []string{"s3cretC"}}, false, "s3cretC"},
		{"undeclared", map[string]any{"token": "s3cretD"}, false, "s3cretD"},
		{"badprops", map[string]any{"settingsSchema": map[string]any{"properties": "x"}, "token": "s3cretE"}, false, "s3cretE"},
		{"numeric", map[string]any{"pin": 987654}, true, "987654"},
		{"public", map[string]any{"settingsSchema": map[string]any{"type": "object", "additionalProperties": true,
			"properties": map[string]any{
				"url":  map[string]any{"type": "string"},
				"mode": map[string]any{"enum": []string{"fast", "slow"}},
			}}, "url": "https://example.com/pubvisible", "mode": "slow", "other": "s3cretI"}, false, "s3cretI"},
		{"prefix", map[string]any{"settingsSchema": map[string]any{"type": "object", "properties": map[string]any{
			"accounts": map[string]any{"type": "array", "prefixItems": []any{map[string]any{"type": "string", "format": "password"}}},
		}}, "accounts": []string{"s3cretF"}}, false, "s3cretF"},
		{"dependent", map[string]any{"settingsSchema": map[string]any{"type": "object", "dependentSchemas": map[string]any{"a": map[string]any{}},
			"properties": map[string]any{"token": map[string]any{"type": "string"}}}, "token": "s3cretG"}, false, "s3cretG"},
		{"writeonly", map[string]any{"settingsSchema": map[string]any{"type": "object", "properties": map[string]any{
			"token": map[string]any{"type": "string", "writeOnly": true},
		}}, "token": "s3cretH"}, false, "s3cretH"},
	}
	var defs []srcDef
	plugs := map[string]*tplug{}
	for _, c := range cases {
		p := w.newTP(c.key)
		if c.crash {
			p.put("crash-on-initialize", "")
		}
		plugs[c.key] = p
		defs = append(defs, tpSource(c.key, p, c.extra))
	}
	w.startSources(defs...)
	for _, c := range cases {
		want := "state ok"
		pred := isOK
		if c.crash {
			want, pred = "state error", func(s sourceJSON) bool { return s.State == "error" }
		}
		w.waitSource(c.key, 10*time.Second, want, pred)
		code, body := w.srcReq(http.MethodGet, "/"+c.key+"/settings", nil)
		if code != http.StatusOK || strings.Contains(body, c.secret) {
			t.Errorf("GET settings of %s = %d %s, want 200 without %s", c.key, code, body, c.secret)
		}
	}
	// What the schema makes public is still shown, however the node is closed.
	_, pubBody := w.srcReq(http.MethodGet, "/public/settings", nil)
	if !strings.Contains(pubBody, "pubvisible") || !strings.Contains(pubBody, `"mode":"slow"`) {
		t.Errorf("GET settings = %s, want the declared url and the enum-only mode in clear", pubBody)
	}
	// What the schema declares plain is still shown.
	_, body := w.srcReq(http.MethodGet, "/undeclared/settings", nil)
	if !strings.Contains(body, plugs["undeclared"].dir) {
		t.Errorf("GET settings = %s, want the declared flagdir in clear", body)
	}
}

// holdsAny reports whether any file under dir holds one of the strings.
func holdsAny(t *testing.T, dir string, secrets ...string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, s := range secrets {
			if strings.Contains(string(b), s) {
				found = p
			}
		}
		return nil
	})
	return found
}

// The GitHub plugin, the real one, through Admiral's API with a fake gh first
// on PATH: panels and plugin-side defaults, settings, orders, the token, signing
// in, a failing search, a refresh and a missing gh.
func TestGitHubPlugin(t *testing.T) {
	const (
		defaultToken = "fake-token-default-1"
		workToken    = "fake-token-work-2"
		workHost     = "git.example.test"
		notReachable = "error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com"
	)
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", defaultToken)
	// The "work" account is signed out until the test signs it in.

	// "gh": no settings, so the plugin's own defaults. The first panel holds
	// two issues and a pull request; the PR is also in the second panel, which
	// holds more than 50 matches.
	g.search("assigned", "assignee:@me",
		ghNode("github.com", "acme/widgets", 12, false, "Flaky login test", "p0", "bug"),
		ghNode("github.com", "acme/widgets", 7, true, "Bump deps", "dependencies"),
		ghNode("github.com", "acme/api", 3, false, "Slow query", "Priority: High"))
	review := []map[string]any{ghNode("github.com", "acme/widgets", 7, true, "Bump deps", "dependencies")}
	for n := 100; n < 151; n++ {
		review = append(review, ghNode("github.com", "acme/widgets", n, true, "Review me "+strconv.Itoa(n)))
	}
	g.search("review", "review-requested:@me", review...)

	// "work": a host, an account, panels with blank entries between the good
	// ones, hot labels that replace the defaults, and a refreshMinutes that is
	// not a count.
	g.search("a-sorted", "sort:created-asc",
		ghNode(workHost, "acme/widgets", 21, false, "Oldest bug", "Needs-Attention", "P0"))
	g.search("b-bugs", "label:bug", ghNode(workHost, "acme/widgets", 22, false, "Any bug"))
	workSettings := map[string]any{
		"host": workHost, "account": "work", "hotLabels": []string{"needs-attention"}, "refreshMinutes": 0,
		"panels": []map[string]any{
			{"title": "Bugs", "query": "repo:acme/widgets is:open label:bug sort:created-asc"},
			{"title": "", "query": "is:open"},
			{"title": "No query"},
			{"title": "  ", "query": "is:open"},
			{"title": "Blank query", "query": "  "},
			{"title": "Bugs", "query": "is:open label:bug"},
		},
	}
	writeConfig(t, w.home, w.srcConfig(
		srcDef{Key: "gh", Plugin: "github", Name: "GitHub"},
		srcDef{Key: "work", Plugin: "github", Name: "Work", Settings: workSettings}))
	w.start()

	// default panels and their orders
	s := w.waitSource("gh", 15*time.Second, "the two default panels with orders", func(s sourceJSON) bool {
		a, okA := s.panel("Assigned to me")
		r, okR := s.panel("Review requested")
		return s.State == "ok" && len(s.Panels) == 2 && okA && okR && len(a.Orders) > 0 && len(r.Orders) > 0
	})
	if !reflect.DeepEqual(s.Capabilities, []string{"refresh"}) {
		t.Errorf("capabilities = %v, want only refresh", s.Capabilities)
	}
	assigned, _ := s.panel("Assigned to me")
	review2, _ := s.panel("Review requested")
	if !strings.HasSuffix(assigned.ID, "assigned-to-me") || !strings.HasSuffix(review2.ID, "review-requested") {
		t.Errorf("panel ids = %q, %q, want slugs of the titles", assigned.ID, review2.ID)
	}
	wantAssigned := []orderJSON{
		{ID: "acme/widgets#12", Title: "Flaky login test", Text: "https://github.com/acme/widgets/issues/12", URL: "https://github.com/acme/widgets/issues/12", Repo: "https://github.com/acme/widgets", Hot: true, Labels: []string{"p0", "bug"}},
		{ID: "acme/widgets#7", Title: "Bump deps", Text: "https://github.com/acme/widgets/pull/7", URL: "https://github.com/acme/widgets/pull/7", Repo: "https://github.com/acme/widgets", Labels: []string{"dependencies"}},
		{ID: "acme/api#3", Title: "Slow query", Text: "https://github.com/acme/api/issues/3", URL: "https://github.com/acme/api/issues/3", Repo: "https://github.com/acme/api", Hot: true, Labels: []string{"Priority: High"}},
	}
	if !reflect.DeepEqual(assigned.Orders, wantAssigned) {
		t.Errorf("Assigned to me orders = %s\nwant %s", dump(assigned.Orders), dump(wantAssigned))
	}
	if len(review2.Orders) != 50 || review2.Orders[0].ID != "acme/widgets#7" {
		t.Errorf("Review requested has %d orders starting %q, want the first 50 of the 52 matches", len(review2.Orders), review2.Orders[0].ID)
	}

	// the calls: a token for the host, then searches that carry it
	if !slices.ContainsFunc(g.calls(), func(c ghCall) bool { return slices.Equal(c.Argv, []string{"auth", "token", "-h", "github.com"}) }) {
		t.Errorf("no `gh auth token -h github.com` call (the default host, no account); calls: %+v", g.calls())
	}
	searches := g.searchesWith(defaultToken)
	if len(searches) < 2 {
		t.Fatalf("searches with the default account's token = %d, want one per panel; calls: %+v", len(searches), g.calls())
	}
	for _, c := range searches {
		if !c.has("--hostname", "github.com") {
			t.Errorf("search %q lacks --hostname github.com", c.Argv)
		}
		if c.Env["GH_NO_UPDATE_NOTIFIER"] != "1" || c.Env["GH_PROMPT_DISABLED"] != "1" {
			t.Errorf("search env = %v, want GH_NO_UPDATE_NOTIFIER=1 and GH_PROMPT_DISABLED=1", c.Env)
		}
		if c.mentions("assignee:@me") && !c.mentions("sort:updated-desc") {
			t.Errorf("a query with no sort: qualifier was searched without sort:updated-desc: %q", c.Argv)
		}
	}

	// the work source: signed out, so the sign-in card, with its panels
	s = w.waitSource("work", 15*time.Second, "auth_required", func(s sourceJSON) bool { return s.State == "auth_required" })
	if s.Auth == nil || s.Auth.Message != "Run gh auth login -h "+workHost {
		t.Errorf("work source auth = %+v, want the message %q", s.Auth, "Run gh auth login -h "+workHost)
	}
	if !slices.ContainsFunc(g.calls(), func(c ghCall) bool {
		return slices.Equal(c.Argv, []string{"auth", "token", "-h", workHost, "-u", "work"})
	}) {
		t.Errorf("no `gh auth token -h %s -u work` call (host and account from the settings); calls: %+v", workHost, g.calls())
	}
	if n := len(g.searchesWith(workToken)); n != 0 {
		t.Errorf("the signed-out work source searched %d times", n)
	}
	// ... then sign in, refresh, and the card clears by itself
	g.token("work", workToken)
	code, body := w.srcReq(http.MethodPost, "/work/refresh", nil)
	w.mustOK(code, body, "POST /api/sources/work/refresh")
	s = w.waitSource("work", 15*time.Second, "ok with the bug orders", func(s sourceJSON) bool {
		p, ok := s.panel("Bugs")
		return s.State == "ok" && s.Auth == nil && ok && len(p.Orders) > 0 && len(s.Panels) == 2 && len(s.Panels[1].Orders) > 0
	})
	if s.Panels[0].Title != "Bugs" || s.Panels[1].Title != "Bugs" ||
		!strings.HasSuffix(s.Panels[0].ID, "bugs") || !strings.HasSuffix(s.Panels[1].ID, "bugs-2") {
		t.Errorf("work panels = %s, want only the two usable entries, with ids bugs and bugs-2", dump(s.Panels))
	}
	wantSorted := []orderJSON{{ID: "acme/widgets#21", Title: "Oldest bug", Text: "https://git.example.test/acme/widgets/issues/21",
		URL: "https://git.example.test/acme/widgets/issues/21", Repo: "https://git.example.test/acme/widgets", Hot: true,
		Labels: []string{"Needs-Attention", "P0"}}}
	if !reflect.DeepEqual(s.Panels[0].Orders, wantSorted) {
		t.Errorf("work panel 1 orders = %s, want %s (hot because hotLabels is needs-attention, not because of P0)", dump(s.Panels[0].Orders), dump(wantSorted))
	}
	for _, c := range g.searchesWith(workToken) {
		if !c.has("--hostname", workHost) {
			t.Errorf("work search %q lacks --hostname %s", c.Argv, workHost)
		}
		if c.mentions("sort:created-asc") && c.mentions("sort:updated-desc") {
			t.Errorf("a query with its own sort: was given a second one: %q", c.Argv)
		}
	}

	// a failing search: its error on its panel, the last orders stay, the other
	// panel is untouched; a network failure reads differently; a good answer
	// clears it and a changed result shows up.
	pollPanel := func(what string, pred func(a, r panelJSON) bool) (panelJSON, panelJSON) {
		t.Helper()
		var a, r panelJSON
		w.waitSource("gh", 15*time.Second, what, func(s sourceJSON) bool {
			a, _ = s.panel("Assigned to me")
			r, _ = s.panel("Review requested")
			return pred(a, r)
		})
		return a, r
	}
	refresh := func() {
		t.Helper()
		code, body := w.srcReq(http.MethodPost, "/gh/refresh", nil)
		w.mustOK(code, body, "POST /api/sources/gh/refresh")
	}
	g.fail("assigned", "gh: The search is invalid: unexpected end of query (HTTP 422)")
	refresh()
	a, r := pollPanel("a search error on Assigned to me", func(a, r panelJSON) bool { return a.Error != nil })
	if !strings.HasPrefix(a.errText(), "Search failed:") || !strings.Contains(a.errText(), "unexpected end of query") {
		t.Errorf("panel error = %q, want \"Search failed: \" and GitHub's message", a.errText())
	}
	if !reflect.DeepEqual(a.Orders, wantAssigned) || r.Error != nil || len(r.Orders) != 50 {
		t.Errorf("after a failed search: Assigned to me = %s, Review requested error %v with %d orders; want the last orders kept and the other panel clean", dump(a.Orders), r.Error, len(r.Orders))
	}
	g.fail("assigned", notReachable)
	refresh()
	a, _ = pollPanel("the unreachable error", func(a, r panelJSON) bool { return strings.Contains(a.errText(), "reach") })
	if a.errText() != "Couldn't reach github.com" || len(a.Orders) != 3 {
		t.Errorf("panel error = %q with %d orders, want \"Couldn't reach github.com\" and the 3 last orders", a.errText(), len(a.Orders))
	}
	g.search("assigned", "assignee:@me", ghNode("github.com", "acme/widgets", 13, true, "Fresh PR"))
	refresh()
	a, _ = pollPanel("the error gone and the new result", func(a, r panelJSON) bool { return a.Error == nil && len(a.Orders) == 1 })
	// a transport error that is not a DNS one reads as unreachable too
	g.fail("assigned", "Post \"https://api.github.com/graphql\": dial tcp 140.82.112.6:443: connect: connection refused")
	refresh()
	a, _ = pollPanel("the connection refused error", func(a, r panelJSON) bool { return a.Error != nil })
	if a.errText() != "Couldn't reach github.com" || len(a.Orders) != 1 {
		t.Errorf("panel error = %q with %d orders after a refused connection, want \"Couldn't reach github.com\" and the last order", a.errText(), len(a.Orders))
	}
	g.search("assigned", "assignee:@me", ghNode("github.com", "acme/widgets", 13, true, "Fresh PR"))
	refresh()
	a, _ = pollPanel("the error gone again", func(a, r panelJSON) bool { return a.Error == nil && len(a.Orders) == 1 })
	if a.Orders[0].ID != "acme/widgets#13" || a.Orders[0].Text != "https://github.com/acme/widgets/pull/13" {
		t.Errorf("Assigned to me after the refresh = %s, want acme/widgets#13", dump(a.Orders))
	}

	// only reads, and no token anywhere but in the environment of gh
	for _, c := range g.calls() {
		if !(c.has("auth", "token") || c.has("api", "graphql")) || c.mentions("mutation") || c.has("--method") || c.has("-X") {
			t.Errorf("gh was run as %q, want only `auth token` and read-only `api graphql`", c.Argv)
		}
	}
	noLeak := func(stage string) {
		t.Helper()
		out := w.in.stderrBuf.String()
		if strings.Contains(out, defaultToken) || strings.Contains(out, workToken) {
			t.Errorf("%s: Admiral's log holds a token: %s", stage, out)
		}
		if f := holdsAny(t, w.home, defaultToken, workToken); f != "" {
			t.Errorf("%s: %s holds a token", stage, f)
		}
	}
	noLeak("after the run")

	// gh not installed: both sources ask for it
	w.in.Stop()
	w.extra = append(w.extra, "PATH="+w.pythonOnlyPath())
	w.restart()
	for _, key := range []string{"gh", "work"} {
		s = w.waitSource(key, 15*time.Second, "auth_required without gh", func(s sourceJSON) bool { return s.State == "auth_required" })
		if want := "Install the GitHub CLI (brew install gh), then run gh auth login"; s.Auth == nil || s.Auth.Message != want {
			t.Errorf("%s with no gh: auth = %+v, want the message %q", key, s.Auth, want)
		}
	}
	noLeak("with no gh")
}
