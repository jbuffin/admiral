package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func (w *world) configSources() map[string]map[string]any {
	w.t.Helper()
	var cfg struct {
		Sources map[string]map[string]any `json:"sources"`
	}
	if _, err := os.Stat(configPath(w.home)); os.IsNotExist(err) {
		return nil // a fresh install writes no config.json until something is saved
	}
	if err := json.Unmarshal([]byte(w.readFile(configPath(w.home))), &cfg); err != nil {
		w.t.Fatalf("config.json is not JSON: %v\n%s", err, w.readFile(configPath(w.home)))
	}
	return cfg.Sources
}

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
	if !ok || len(s.Views) == 0 {
		w.t.Fatalf("source %q has no panel: %s", key, dump(w.sources()))
	}
	return s.Views[0].ID
}

// waitUp waits until the server answers its plugins list: it has started and
// written the plugin folders.
func (w *world) waitUp() {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/plugins", nil)
		if code == http.StatusOK && strings.Contains(body, `"github"`) {
			return
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 10s GET /api/plugins never listed the plugins: %d %s", code, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestFreshInstall(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.startFresh()
	w.waitUp()

	for _, name := range embeddedPlugins {
		var man struct {
			Name    string `json:"name"`
			Command string `json:"command"`
		}
		raw := w.readFile(filepath.Join(w.home, "plugins", name, "plugin.json"))
		if err := json.Unmarshal([]byte(raw), &man); err != nil || man.Name != name || man.Command == "" {
			t.Fatalf("plugins/%s/plugin.json must be a manifest named %s with a command, got %q (%v)", name, name, raw, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(w.home, "plugins", "freeform")); err == nil {
		t.Errorf("a fresh install wrote plugins/freeform: freeform is no longer embedded")
	}
	if got := w.configSources(); len(got) != 0 {
		t.Errorf("config.json sources = %v, want none on a fresh install", got)
	}
	if list := w.sources(); len(list) != 0 {
		t.Errorf("GET /api/sources lists %s, want none", keysOf(list))
	}
}

func TestFreshInstallLeavesWhatItShould(t *testing.T) {
	t.Parallel()
	t.Run("an empty sources object gets no source added", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		writeConfig(t, w.home, `{"version":1,"fleets":{},"sources":{}}`)
		w.start()
		w.waitSources(5*time.Second, "an empty list", func(l []sourceJSON) bool { return len(l) == 0 })
		if got := w.configSources(); len(got) != 0 {
			t.Errorf("config sources = %v, want {} left alone", got)
		}
	})
	t.Run("an invalid config gets nothing added", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		bad := `{"version":1,"fleets":[]}`
		writeConfig(t, w.home, bad)
		w.start()
		if got := w.readFile(configPath(w.home)); got != bad {
			t.Errorf("an invalid config at startup was rewritten: %q", got)
		}
	})
	t.Run("a sources key deleted by hand is not undone until the next start", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.startFresh()
		w.waitUp()
		replaceConfig(t, w.home, `{"version":1,"fleets":{}}`)
		w.waitSources(10*time.Second, "no sources after the hand edit", func(l []sourceJSON) bool { return len(l) == 0 })
		holdThat(t, "a reload re-added sources", func() bool { return !strings.Contains(w.readFile(configPath(w.home)), "sources") })
		w.in.Stop()
		w.startFresh()
		w.waitUp()
		w.waitSources(5*time.Second, "no source after a start", func(l []sourceJSON) bool { return len(l) == 0 })
		if got := w.readFile(configPath(w.home)); strings.Contains(got, `"orders"`) {
			t.Errorf("a start added a source to a file without sources: %s", got)
		}
	})
	for _, name := range embeddedPlugins {
		t.Run("an edited "+name+" folder is left alone and a missing one is written again", func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.startFresh()
			w.waitUp()
			manifest := filepath.Join(w.home, "plugins", name, "plugin.json")
			var m map[string]any
			if err := json.Unmarshal([]byte(w.readFile(manifest)), &m); err != nil {
				t.Fatal(err)
			}
			m["description"] = "edited by the engineer"
			writeJSON(t, manifest, m)
			w.in.Stop()
			w.startFresh()
			w.waitUp()
			if got := w.readFile(manifest); !strings.Contains(got, "edited by the engineer") {
				t.Errorf("an edited %s folder was overwritten: %s", name, got)
			}
			w.in.Stop()
			if err := os.RemoveAll(filepath.Join(w.home, "plugins", name)); err != nil {
				t.Fatal(err)
			}
			w.startFresh()
			w.waitUp()
			if got := w.readFile(manifest); got == "" || strings.Contains(got, "edited by the engineer") {
				t.Errorf("a missing %s folder was not written again: %q", name, got)
			}
		})
	}
}

var embeddedPlugins = []string{"github"}

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

func TestPluginUpgrade(t *testing.T) {
	t.Parallel()
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
		t.Parallel()
		w := newWorld(t)
		// Finder leaves a .DS_Store; go:embed never carried one, so it is not an edit.
		withFinder := map[string]string{".DS_Store": "finder"}
		for k, v := range old {
			withFinder[k] = v
		}
		writeOld(t, w, old, withFinder)
		w.startFresh()
		w.waitUp()
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
		t.Parallel()
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

// Admiral does not ship freeform. A start removes its folder only while it still
// matches its stamp; an edited copy and a symlink are the engineer's and stay.
func TestLeftoverFreeformFolder(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"plugin.json": `{"name":"freeform","description":"an earlier copy","command":"python3","args":["plugin.py"]}`,
		"plugin.py":   "# an earlier copy\n",
	}
	write := func(t *testing.T, dir string, files map[string]string, stamp string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for n, c := range files {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, ".admiral-stamp"), []byte(stamp), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("an untouched copy is removed", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		dir := filepath.Join(w.home, "plugins", "freeform")
		write(t, dir, files, stampOf(files))
		w.startFresh()
		w.waitUp()
		if _, err := os.Lstat(dir); err == nil {
			t.Errorf("plugins/freeform is still there after a start: an untouched copy of a plugin that is gone must be removed")
		}
	})
	t.Run("an edited copy is left alone", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		dir := filepath.Join(w.home, "plugins", "freeform")
		write(t, dir, files, stampOf(files))
		if err := os.WriteFile(filepath.Join(dir, "plugin.py"), []byte("# edited by the engineer\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		w.startFresh()
		w.waitUp()
		if got := w.readFile(filepath.Join(dir, "plugin.py")); got != "# edited by the engineer\n" {
			t.Errorf("an edited plugins/freeform was changed: plugin.py = %q", got)
		}
	})
	t.Run("a symlink is left alone and its target untouched", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		target := filepath.Join(w.root, "my-freeform")
		write(t, target, files, stampOf(files)) // would pass the stamp check if followed
		if err := os.MkdirAll(filepath.Join(w.home, "plugins"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(w.home, "plugins", "freeform")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		w.startFresh()
		w.waitUp()
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the plugins/freeform symlink was removed or replaced: %v", err)
		}
		if got := w.readFile(filepath.Join(target, "plugin.py")); got != files["plugin.py"] {
			t.Errorf("the symlink's target was changed: plugin.py = %q", got)
		}
		if _, err := os.Stat(filepath.Join(target, ".admiral-stamp")); err != nil {
			t.Errorf("the symlink's target lost its stamp: %v", err)
		}
	})
}

// The sources name a fleet: the startup migration would otherwise rewrite the file.
func TestSourcesConfigValidation(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, config, want string }{
		{"sources null", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":null}`, "sources"},
		{"sources an array", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":[]}`, "sources"},
		{"bad source key", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":{"my source":{"plugin":"jira","fleet":"f"}}}`, "my source"},
		{"unknown key in a source", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":{"s":{"plugin":"jira","fleet":"f","colour":"red"}}}`, "sources.s.colour"},
		{"fleet empty", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":{"s":{"plugin":"jira","fleet":""}}}`, "sources.s.fleet"},
		{"unknown fleet", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":{"s":{"plugin":"jira","fleet":"nope"}}}`, "sources.s.fleet"},
		{"plugin missing", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":{"s":{"name":"S","fleet":"f"}}}`, "sources.s.plugin"},
		{"settings not an object", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":{"s":{"plugin":"jira","fleet":"f","settings":[]}}}`, "sources.s.settings"},
		{"duplicate source key", `{"version":1,"fleets":{"f":{"name":"F"}},"sources":{"dupkey":{"plugin":"jira","fleet":"f"},"dupkey":{"plugin":"jira","fleet":"f"}}}`, "dupkey"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
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

func TestOrderTextValidationAndEditFlow(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.startSources(w.orderSrcDef("orders", "Orders"))
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	panel := w.panelID("orders")
	ws := w.ws()
	waitSourcesMsg(t, ws, 5*time.Second, "the orders source on connect", func(l []sourceJSON) bool {
		_, ok := find(l, "orders")
		return ok
	})
	store := filepath.Join(w.dataDir("orders"), "stored.json")

	text := "\n\n  Fix the flaky test  \nsecond line\n\tindented"
	code, body := w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": text})
	w.mustOK(code, body, "POST /api/sources/orders/orders")
	var first orderJSON
	waitSourcesMsg(t, ws, 5*time.Second, "the new order pushed to the client", func(l []sourceJSON) bool {
		s, _ := find(l, "orders")
		for _, p := range s.Views {
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
		t.Errorf("data/orders/stored.json = %q, want it to hold the order", got)
	}

	code, body = w.srcReq(http.MethodPut, "/orders/orders/"+first.ID, map[string]any{"text": "Rewrite the parser\nwith care"})
	w.mustOK(code, body, "PUT /api/sources/orders/orders/<id>")
	w.waitSource("orders", 5*time.Second, "the edited order", func(s sourceJSON) bool {
		o, ok := s.order(first.ID)
		return ok && o.Title == "Rewrite the parser" && o.Text == "Rewrite the parser\nwith care"
	})
	if got := w.readFile(store); strings.Contains(got, "flaky") || !strings.Contains(got, "Rewrite the parser") {
		t.Errorf("data/orders/stored.json = %q, want the edit and not the old text", got)
	}

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

	s = w.waitSource("orders", 5*time.Second, "two orders", func(s sourceJSON) bool { return len(s.orderIDs()) == 2 })

	code, body = w.srcReq(http.MethodDelete, "/orders/orders/"+first.ID, nil)
	w.mustOK(code, body, "DELETE /api/sources/orders/orders/<id>")
	w.waitSource("orders", 5*time.Second, "the order removed", func(s sourceJSON) bool { return !s.hasOrder(first.ID) && len(s.orderIDs()) == 1 })
	if got := w.readFile(store); strings.Contains(got, "Rewrite the parser") {
		t.Errorf("data/orders/stored.json still holds the removed order: %q", got)
	}
}

func TestLaunchTakesTheOrdersTextNotTheClients(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.startSources(w.orderSrcDef("orders", "Orders"))
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	panel := w.panelID("orders")
	text := "Fix the flaky test\n\n\tsee the CI log\nthen rerun it"
	code, body := w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": text})
	w.mustOK(code, body, "add order")
	s := w.waitSource("orders", 5*time.Second, "the order", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 })
	id := s.orderIDs()[0]

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
	w.in.waitShipsList(t, 10*time.Second, "the ship gone", func(l []shipJSON) bool { return len(l) == 0 })
	w.waitSource("orders", 5*time.Second, "no orders left", func(s sourceJSON) bool { return len(s.orderIDs()) == 0 })
	if got := w.readFile(filepath.Join(w.dataDir("orders"), "stored.json")); strings.Contains(got, "Fix the flaky test") {
		t.Errorf("stored.json still holds the launched order: %q", got)
	}
}

func TestPluginsAPI(t *testing.T) {
	t.Parallel()
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
