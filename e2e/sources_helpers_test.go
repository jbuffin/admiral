package e2e

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The test plugin (e2e/testplugin/plugin.py) is steered through the flag
// directory its source's settings name; see the plugin's docstring.

type orderJSON struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Text   string   `json:"text"`
	URL    string   `json:"url"`
	Hot    bool     `json:"hot"`
	Repo   string   `json:"repo"`
	Labels []string `json:"labels,omitempty"`
}

type viewJSON struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Orders []orderJSON `json:"orders"`
	Error  *string     `json:"error"`
	// Autopilot is null for a panel with no autopilot entry in config.
	Autopilot *struct {
		Enabled    bool     `json:"enabled"`
		Paused     bool     `json:"paused"`
		MaxShips   int      `json:"max_ships"`
		Resources  []string `json:"resources"`
		WaitingFor []string `json:"waiting_for"`
	} `json:"autopilot"`
}

type authJSON struct {
	Message string `json:"message"`
	URL     string `json:"url"`
	Code    string `json:"code"`
}

type sourceJSON struct {
	Key          string     `json:"key"`
	Name         string     `json:"name"`
	Plugin       string     `json:"plugin"`
	IconURL      string     `json:"icon_url"`
	Capabilities []string   `json:"capabilities"`
	State        string     `json:"state"`
	ReadOnly     bool       `json:"read_only"`
	Error        *string    `json:"error"`
	Auth         *authJSON  `json:"auth"`
	Views        []viewJSON `json:"views"`
}

func (p viewJSON) orderIDs() []string { return sourceJSON{Views: []viewJSON{p}}.orderIDs() }

func (p viewJSON) hasOrder(id string) bool { return sourceJSON{Views: []viewJSON{p}}.hasOrder(id) }

func (s sourceJSON) orderIDs() []string {
	ids := []string{}
	for _, p := range s.Views {
		for _, o := range p.Orders {
			ids = append(ids, o.ID)
		}
	}
	return ids
}

func (s sourceJSON) order(id string) (orderJSON, bool) {
	for _, p := range s.Views {
		for _, o := range p.Orders {
			if o.ID == id {
				return o, true
			}
		}
	}
	return orderJSON{}, false
}

func (s sourceJSON) hasOrder(id string) bool { _, ok := s.order(id); return ok }

func (s sourceJSON) errText() string {
	if s.Error == nil {
		return ""
	}
	return *s.Error
}

func keysOf(list []sourceJSON) []string {
	k := []string{}
	for _, s := range list {
		k = append(k, s.Key)
	}
	return k
}

func find(list []sourceJSON, key string) (sourceJSON, bool) {
	for _, s := range list {
		if s.Key == key {
			return s, true
		}
	}
	return sourceJSON{}, false
}

func decodeSources(t *testing.T, raw string) []sourceJSON {
	t.Helper()
	var list []sourceJSON
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return list
	}
	var wrap struct {
		Sources []sourceJSON `json:"sources"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
		t.Fatalf("source list is not JSON: %v\n%s", err, raw)
	}
	return wrap.Sources
}

// sources is GET /api/sources. It fails at once on a non-200, so a missing
// route does not spend a polling deadline.
func (w *world) sources() []sourceJSON {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/sources", nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET /api/sources = %d, want 200: %s", code, body)
	}
	return decodeSources(w.t, body)
}

func (w *world) waitSource(key string, d time.Duration, what string, pred func(sourceJSON) bool) sourceJSON {
	w.t.Helper()
	deadline := time.Now().Add(d)
	for {
		list := w.sources()
		s, ok := find(list, key)
		if ok && pred(s) {
			return s
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within %s source %q never showed %s; last list: %s", d, key, what, dump(list))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) waitSources(d time.Duration, what string, pred func([]sourceJSON) bool) []sourceJSON {
	w.t.Helper()
	deadline := time.Now().Add(d)
	for {
		list := w.sources()
		if pred(list) {
			return list
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within %s GET /api/sources never showed %s; last list: %s", d, what, dump(list))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dump(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func isOK(s sourceJSON) bool { return s.State == "ok" }

func waitSourcesMsg(t *testing.T, c *websocket.Conn, d time.Duration, what string, pred func([]sourceJSON) bool) []sourceJSON {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var last string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within %s the websocket never delivered a `sources` message with %s (last sources message: %s): %v", d, what, last, err)
		}
		var m struct {
			Type    string       `json:"type"`
			Sources []sourceJSON `json:"sources"`
		}
		if json.Unmarshal(data, &m) != nil || m.Type != "sources" {
			continue
		}
		last = string(data)
		if pred(m.Sources) {
			return m.Sources
		}
	}
}

func (w *world) installPlugin(folder, manifestName string) string {
	w.t.Helper()
	src, err := filepath.Abs(filepath.Join("testplugin", "plugin.py"))
	if err != nil {
		w.t.Fatal(err)
	}
	code, err := os.ReadFile(src)
	if err != nil {
		w.t.Fatal(err)
	}
	dir := filepath.Join(w.home, "plugins", folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.py"), code, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "icon.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"></svg>`), 0o644); err != nil {
		w.t.Fatal(err)
	}
	writeJSON(w.t, filepath.Join(dir, "plugin.json"), map[string]any{
		"name": manifestName, "description": "Test plugin " + manifestName,
		"command": "./plugin.py", "args": []string{}, "icon": "icon.svg"})
	return dir
}

type tplug struct {
	t   *testing.T
	dir string
}

func (w *world) newTP(label string) *tplug {
	w.t.Helper()
	d := filepath.Join(w.root, "fd-"+label)
	if err := os.MkdirAll(d, 0o755); err != nil {
		w.t.Fatal(err)
	}
	return &tplug{t: w.t, dir: d}
}

func (p *tplug) settings(extra map[string]any) map[string]any {
	m := map[string]any{"flagdir": p.dir}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// put writes a flag or command file atomically.
func (p *tplug) put(name, content string) {
	p.t.Helper()
	tmp := filepath.Join(p.dir, ".tmp-"+name)
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		p.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(p.dir, name)); err != nil {
		p.t.Fatal(err)
	}
}

func (p *tplug) clear(name string) {
	p.t.Helper()
	if err := os.Remove(filepath.Join(p.dir, name)); err != nil && !os.IsNotExist(err) {
		p.t.Fatal(err)
	}
}

func (p *tplug) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(p.dir, name))
	return strings.TrimSpace(string(b))
}

func (p *tplug) setOrders(byPanel map[string][]map[string]any) {
	p.t.Helper()
	b, err := json.Marshal(byPanel)
	if err != nil {
		p.t.Fatal(err)
	}
	p.put("orders.json", string(b))
}

func (p *tplug) mainOrders(orders ...map[string]any) {
	p.setOrders(map[string][]map[string]any{"main": orders})
}

func ord(id, title string) map[string]any { return map[string]any{"id": id, "title": title} }

func (p *tplug) do(cmd, content string) { p.t.Helper(); p.put("do-"+cmd, content) }

func (p *tplug) starts() []float64 {
	var out []float64
	for _, l := range strings.Split(p.read("starts"), "\n") {
		if f, err := strconv.ParseFloat(strings.TrimSpace(l), 64); err == nil {
			out = append(out, f)
		}
	}
	return out
}

func (p *tplug) waitStarts(d time.Duration, n int) []float64 {
	p.t.Helper()
	deadline := time.Now().Add(d)
	for {
		if s := p.starts(); len(s) >= n {
			return s
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("within %s the plugin received %d initialize calls, want %d", d, len(p.starts()), n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type rpcCall struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func (p *tplug) calls() []rpcCall {
	var out []rpcCall
	for _, l := range strings.Split(p.read("calls"), "\n") {
		var c rpcCall
		if json.Unmarshal([]byte(l), &c) == nil && c.Method != "" {
			out = append(out, c)
		}
	}
	return out
}

func (p *tplug) callsOf(method string) []rpcCall {
	var out []rpcCall
	for _, c := range p.calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (p *tplug) waitCall(d time.Duration, method string, pred func(rpcCall) bool) rpcCall {
	p.t.Helper()
	deadline := time.Now().Add(d)
	for {
		for _, c := range p.callsOf(method) {
			if pred(c) {
				return c
			}
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("within %s the plugin never received a matching %s; calls: %+v", d, method, p.calls())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// pid waits for the plugin to write its pid, which it does just after it
// records its start.
func (p *tplug) pid() int {
	p.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if n, _ := strconv.Atoi(p.read("pid")); n > 0 {
			return n
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("within 5s the plugin wrote no pid")
		}
	}
}

func (p *tplug) waitDead(d time.Duration, pid int, what string) {
	p.t.Helper()
	deadline := time.Now().Add(d)
	for {
		if pid > 0 && syscall.Kill(pid, 0) != nil {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("within %s the plugin process %d (%s) was still alive", d, pid, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (p *tplug) alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

type srcDef struct {
	Key, Plugin, Name string
	Fleet             string // defaults to testFleet
	Settings          map[string]any
}

// testFleet is the fleet of srcConfig's fleets that a source belongs to unless
// its definition names another. testSourceFleet is the first of the default
// fleets (config) that is meant for it.
const (
	testFleet       = "stay"
	testSourceFleet = "work"
)

// rawSourcesIn is rawSources for definitions that belong to the given fleet
// unless they name another.
func rawSourcesIn(fleet string, defs ...srcDef) string {
	in := make([]srcDef, len(defs))
	for i, d := range defs {
		in[i] = d
		if d.Fleet == "" {
			in[i].Fleet = fleet
		}
	}
	return rawSources(in...)
}

// inFleet gives every source in the map (key → source object) that names no
// fleet this one, and returns the map. It is where a test source gets its fleet.
func inFleet(fleet string, sources map[string]any) map[string]any {
	for _, s := range sources {
		if m, ok := s.(map[string]any); ok && m["fleet"] == nil {
			m["fleet"] = fleet
		}
	}
	return sources
}

func rawSources(defs ...srcDef) string {
	parts := []string{}
	for _, d := range defs {
		m := map[string]any{"plugin": d.Plugin, "fleet": cmp.Or(d.Fleet, testFleet)}
		if d.Name != "" {
			m["name"] = d.Name
		}
		if d.Settings != nil {
			m["settings"] = d.Settings
		}
		b, _ := json.Marshal(m)
		k, _ := json.Marshal(d.Key)
		parts = append(parts, string(k)+":"+string(b))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// srcConfig is a config with three fleets whose harnesses behave differently
// (stay: runs until killed; sail: exits 0 and sails off; anch: exits 1 and
// stays anchored) and the given sources.
func (w *world) srcConfig(defs ...srcDef) string {
	w.t.Helper()
	return strings.TrimSuffix(w.fleetsConfig(), "}") + `,"sources":` + rawSources(defs...) + `}`
}

func (w *world) fleetsConfig() string {
	w.t.Helper()
	fleets := map[string]any{}
	for key, cue := range map[string][]string{"stay": nil, "sail": {"exit 0"}, "anch": {"exit 1"}} {
		env := w.harnessEnv()
		if cue != nil {
			path := filepath.Join(w.root, "cue-"+key)
			if err := os.WriteFile(path, []byte(strings.Join(cue, "\n")+"\n"), 0o600); err != nil {
				w.t.Fatal(err)
			}
			env["FAKE_CUE"] = path
		}
		fleets[key] = map[string]any{"name": key, "color": "#336699", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": w.fakeExe("claude"), "args": []string{"--fa-claude"}, "env": env}}
	}
	fb, err := json.Marshal(fleets)
	if err != nil {
		w.t.Fatal(err)
	}
	return `{"version":1,"fleets":` + string(fb) + `}`
}

func (w *world) startSources(defs ...srcDef) *fleetInst {
	w.t.Helper()
	w.installPlugin("testplug", "testplug")
	writeConfig(w.t, w.home, w.srcConfig(defs...))
	return w.start()
}

func tpSource(key string, p *tplug, extra map[string]any) srcDef {
	return srcDef{Key: key, Plugin: "testplug", Name: "Test " + key, Settings: p.settings(extra)}
}

// orderSrcDef is the test-only source that stores orders, for the tests whose
// subject is not an order source but that need orders to launch from.
func (w *world) orderSrcDef(key, name string) srcDef {
	w.t.Helper()
	if _, err := os.Stat(filepath.Join(w.home, "plugins", "testplug", "plugin.json")); err != nil {
		w.installPlugin("testplug", "testplug")
	}
	p := w.newTP(key)
	p.put("panels.json", `[{"id":"orders","title":"Orders"}]`)
	d := tpSource(key, p, map[string]any{"storeOrders": true, "capabilities": []string{"add", "edit", "remove"}})
	d.Name = name
	return d
}

// orderSource names no fleet: the config that holds it does (inFleet).
func (w *world) orderSource(key, name string, extra map[string]any) map[string]any {
	w.t.Helper()
	d := w.orderSrcDef(key, name)
	src := map[string]any{"plugin": d.Plugin, "name": d.Name, "settings": d.Settings}
	for k, v := range extra {
		src[k] = v
	}
	return src
}

func (w *world) ordersSource() map[string]any {
	return map[string]any{"orders": w.orderSource("orders", "Orders", nil)}
}

// Call addOrdersSource before start: a config with a `sources` key at all is never seeded.
func (w *world) addOrdersSource() {
	w.t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.readFile(configPath(w.home))), &cfg); err != nil {
		w.t.Fatal(err)
	}
	cfg["sources"] = inFleet(firstFleet(w.t, cfg), w.ordersSource())
	b, err := json.Marshal(cfg)
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
}

// firstFleet is the first fleet key, in sorted order, of a config map.
func firstFleet(t *testing.T, cfg map[string]any) string {
	t.Helper()
	fleets, _ := cfg["fleets"].(map[string]any)
	keys := make([]string, 0, len(fleets))
	for k := range fleets {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		t.Fatal("the config has no fleets for a source to belong to")
	}
	sort.Strings(keys)
	return keys[0]
}

func (w *world) startFresh() *fleetInst {
	w.t.Helper()
	w.in = launchAdmiral(w.t, w.home, "127.0.0.1:"+freePort(w.t), w.baseEnv())
	return w.in
}

// launchFrom posts a launch that names a source and an order and carries no
// text: the server must look the text up.
func (w *world) launchFrom(fleet, source, orderID string) (int, string) {
	w.t.Helper()
	return send(w.t, http.MethodPost, w.in.LocalURL+"/api/ships", map[string]any{
		"fleet": fleet, "harness": "claude", "dir": w.checkout("proj"), "skill": noSkill,
		"source": source, "order_id": orderID})
}

func (w *world) mustLaunchFrom(fleet, source, orderID string) shipJSON {
	w.t.Helper()
	code, body := w.launchFrom(fleet, source, orderID)
	if code != http.StatusCreated {
		w.t.Fatalf("launch of %s/%s from fleet %s = %d, want 201: %s", source, orderID, fleet, code, body)
	}
	return decodeShip(w.t, body)
}

func (w *world) srcReq(method, path string, body any) (int, string) {
	w.t.Helper()
	return send(w.t, method, w.in.LocalURL+"/api/sources"+path, body)
}

func (w *world) mustOK(code int, body string, what ...string) {
	w.t.Helper()
	if code/100 != 2 {
		w.t.Fatalf("%s = %d, want 2xx: %s", strings.Join(what, " "), code, body)
	}
}

func (w *world) wantConflict(code int, body string, what ...string) {
	w.t.Helper()
	if code != http.StatusConflict {
		w.t.Errorf("%s = %d, want 409: %s", strings.Join(what, " "), code, body)
	}
}

func (w *world) ws() *websocket.Conn {
	w.t.Helper()
	c, _, err := wsDial(w.t, w.in.LocalURL, "", nil, "")
	if err != nil {
		w.t.Fatalf("websocket dial: %v", err)
	}
	return c
}

var sourceKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (w *world) dataDir(key string) string { return filepath.Join(w.home, "data", key) }

func (w *world) readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// fakeGH steers testdata/fakegh/gh, which the GitHub plugin finds first on
// PATH. dir is the folder the fake reads its answers from and records its
// calls in (see the script's header).
type fakeGH struct {
	t   *testing.T
	dir string
}

func (w *world) newFakeGH() *fakeGH {
	w.t.Helper()
	bin, err := filepath.Abs(filepath.Join("testdata", "fakegh"))
	if err != nil {
		w.t.Fatal(err)
	}
	g := &fakeGH{t: w.t, dir: filepath.Join(w.root, "fakegh")}
	if err := os.MkdirAll(filepath.Join(g.dir, "search"), 0o755); err != nil {
		w.t.Fatal(err)
	}
	w.extra = append(w.extra, "FAKEGH_DIR="+g.dir, "PATH="+bin+":"+os.Getenv("PATH"))
	return g
}

func (g *fakeGH) write(rel, content string) {
	g.t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, rel), []byte(content), 0o600); err != nil {
		g.t.Fatal(err)
	}
}

// token signs the account in (user "" is the host's active account).
func (g *fakeGH) token(user, token string) {
	g.t.Helper()
	if user == "" {
		g.write("token", token+"\n")
		return
	}
	g.write("token-"+user, token+"\n")
}

// search canns a search: a call with an argument holding match is answered with
// the GraphQL response for these nodes.
func (g *fakeGH) search(key, match string, nodes ...map[string]any) {
	g.t.Helper()
	b, err := json.Marshal(map[string]any{"data": map[string]any{"search": map[string]any{"issueCount": len(nodes), "nodes": nodes}}})
	if err != nil {
		g.t.Fatal(err)
	}
	g.write("search/"+key+".match", match)
	g.write("search/"+key+".json", string(b))
	g.unfail(key)
}

func (g *fakeGH) fail(key, stderr string) { g.t.Helper(); g.write("search/"+key+".fail", stderr+"\n") }

func (g *fakeGH) unfail(key string) {
	g.t.Helper()
	if err := os.Remove(filepath.Join(g.dir, "search", key+".fail")); err != nil && !os.IsNotExist(err) {
		g.t.Fatal(err)
	}
}

func ghNode(host, repo string, n int, pull bool, title string, labels ...string) map[string]any {
	kind, segment := "Issue", "issues"
	if pull {
		kind, segment = "PullRequest", "pull"
	}
	owner, name, _ := strings.Cut(repo, "/")
	names := []map[string]any{}
	for _, l := range labels {
		names = append(names, map[string]any{"name": l})
	}
	return map[string]any{
		"__typename": kind, "number": n, "title": title,
		"url":        "https://" + host + "/" + repo + "/" + segment + "/" + strconv.Itoa(n),
		"updatedAt":  "2026-10-18T09:00:00Z",
		"repository": map[string]any{"nameWithOwner": repo, "name": name, "owner": map[string]any{"login": owner}, "url": "https://" + host + "/" + repo},
		"labels":     map[string]any{"nodes": names},
	}
}

type ghCall struct {
	Argv []string
	Env  map[string]string
	at   time.Time
}

func (c ghCall) has(args ...string) bool {
	for i := 0; i+len(args) <= len(c.Argv); i++ {
		if slices.Equal(c.Argv[i:i+len(args)], args) {
			return true
		}
	}
	return false
}

func (c ghCall) mentions(s string) bool {
	for _, a := range c.Argv {
		if strings.Contains(a, s) {
			return true
		}
	}
	return false
}

// calls are the runs recorded so far, oldest first.
func (g *fakeGH) calls() []ghCall {
	g.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*"))
	var out []ghCall
	for _, d := range dirs {
		argv, err := os.ReadFile(filepath.Join(d, "argv"))
		if err != nil {
			continue // still being written
		}
		c := ghCall{Env: map[string]string{}}
		if len(argv) > 0 {
			c.Argv = strings.Split(strings.TrimSuffix(string(argv), "\x00"), "\x00")
		}
		env, _ := os.ReadFile(filepath.Join(d, "env"))
		for _, l := range strings.Split(string(env), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				c.Env[k] = v
			}
		}
		if fi, err := os.Stat(d); err == nil {
			c.at = fi.ModTime()
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}

func (g *fakeGH) searchesWith(token string) []ghCall {
	var out []ghCall
	for _, c := range g.calls() {
		if c.has("api", "graphql") && c.Env["GH_TOKEN"] == token {
			out = append(out, c)
		}
	}
	return out
}

func (w *world) pythonOnlyPath() string {
	w.t.Helper()
	dir := filepath.Join(w.root, "nogh-bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	for _, name := range []string{"python3", "tmux"} {
		exe, err := exec.LookPath(name)
		if err != nil {
			w.t.Fatalf("%s is required: %v", name, err)
		}
		if err := os.Symlink(exe, filepath.Join(dir, name)); err != nil {
			w.t.Fatal(err)
		}
	}
	return dir
}

func (s sourceJSON) view(title string) (viewJSON, bool) {
	for _, p := range s.Views {
		if p.Title == title {
			return p, true
		}
	}
	return viewJSON{}, false
}

func (p viewJSON) errText() string {
	if p.Error == nil {
		return ""
	}
	return *p.Error
}
