package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Order sources (u7): the supervision and protocol scenarios, driven with the
// test plugin in e2e/testplugin (a source of the plugin "testplug").

// The handshake Admiral sends, what the plugin may do, and the rules that a
// GET never touches a plugin and Admiral pulls on refresh.
func TestSourceHandshakeAndRefresh(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(ord("o1", "First"), ord("o2", "Second"))
	w.startSources(tpSource("a", p, map[string]any{"title": "Things"}))

	s := w.waitSource("a", 10*time.Second, "state ok", isOK)
	if s.Name != "Test a" || s.Plugin != "testplug" || s.IconURL == "" || s.ReadOnly || s.Error != nil || s.Auth != nil {
		t.Errorf("source = %s, want a healthy source named %q of plugin testplug with an icon", dump(s), "Test a")
	}
	if !reflect.DeepEqual(s.Capabilities, []string{"refresh"}) {
		t.Errorf("capabilities = %v, want [refresh]", s.Capabilities)
	}
	if len(s.Panels) != 1 || s.Panels[0].ID != "main" || s.Panels[0].Title != "Things" || !reflect.DeepEqual(s.orderIDs(), []string{"o1", "o2"}) {
		t.Errorf("panels = %s, want panel main %q holding o1, o2", dump(s.Panels), "Things")
	}

	inits := p.callsOf("initialize")
	if len(inits) != 1 {
		t.Fatalf("initialize calls = %+v, want exactly one", inits)
	}
	if v, _ := inits[0].Params["protocolVersion"].(float64); v != 1 {
		t.Errorf("initialize protocolVersion = %v, want 1", inits[0].Params["protocolVersion"])
	}
	if got, _ := inits[0].Params["settings"].(map[string]any); got["title"] != "Things" || got["flagdir"] != p.dir {
		t.Errorf("initialize settings = %v, want the source's settings", inits[0].Params["settings"])
	}
	if got, want := inits[0].Params["dataDir"], w.dataDir("a"); got != want {
		t.Errorf("initialize dataDir = %v, want %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(w.dataDir("a"), "marker")); err != nil {
		t.Errorf("the plugin could not write into its dataDir: %v", err)
	}
	wantCwd, _ := filepath.EvalSymlinks(filepath.Join(w.home, "plugins", "testplug"))
	if gotCwd, _ := filepath.EvalSymlinks(p.read("cwd")); gotCwd != wantCwd {
		t.Errorf("plugin working directory = %q, want its folder %q", gotCwd, wantCwd)
	}
	if !strings.Contains(w.in.stderrBuf.String(), "initialize received") {
		t.Errorf("the plugin's stderr never reached Admiral's log: %q", w.in.stderrBuf.String())
	}
	p.waitCall(2*time.Second, "panels.list", func(rpcCall) bool { return true })
	p.waitCall(2*time.Second, "orders.list", func(c rpcCall) bool { return c.Params["panel"] == "main" })

	// no GET touches the plugin or changes state
	seen := len(p.calls())
	w.sources()
	w.mustOK(send(t, http.MethodGet, w.in.LocalURL+"/api/sources/a/schema", nil))
	time.Sleep(500 * time.Millisecond)
	if n := len(p.calls()); n != seen {
		t.Errorf("GETs made the plugin receive %d more calls: %+v", n-seen, p.calls()[seen:])
	}
	if code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/sources/a/schema", nil); !strings.Contains(body, "flagdir") {
		t.Errorf("GET schema = %d %s, want the plugin's settingsSchema", code, body)
	}

	// refresh: the plugin is told, and Admiral pulls the lists again
	p.mainOrders(ord("o1", "First"), ord("o3", "Third"))
	code, body := w.srcReq(http.MethodPost, "/a/refresh", map[string]any{"panel": "main"})
	w.mustOK(code, body, "POST refresh")
	p.waitCall(2*time.Second, "orders.refresh", func(c rpcCall) bool { return c.Params["panel"] == "main" })
	w.waitSource("a", 5*time.Second, "the refreshed orders", func(s sourceJSON) bool { return reflect.DeepEqual(s.orderIDs(), []string{"o1", "o3"}) })

	// the source declares only refresh: add, edit and remove are refused
	w.wantConflict(w.srcReq(http.MethodPost, "/a/orders", map[string]any{"panel": "main", "text": "x"}))
	w.wantConflict(w.srcReq(http.MethodPut, "/a/orders/o1", map[string]any{"text": "x"}))
	w.wantConflict(w.srcReq(http.MethodDelete, "/a/orders/o1", nil))
	if n := len(p.callsOf("orders.add")) + len(p.callsOf("orders.edit")) + len(p.callsOf("orders.remove")); n != 0 {
		t.Errorf("a refused mutation still reached the plugin (%d calls)", n)
	}

	// plugins stop when Admiral shuts down
	pid := p.pid()
	w.in.Stop()
	p.waitDead(5*time.Second, pid, "after Admiral shut down")
}

// A plugin answering another protocolVersion puts its source in error.
func TestSourceProtocolVersionMismatch(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	w.startSources(tpSource("a", p, map[string]any{"protocolVersion": 2}))
	s := w.waitSource("a", 10*time.Second, "state error", func(s sourceJSON) bool { return s.State == "error" })
	if s.errText() == "" {
		t.Errorf("source = %s, want an error message", dump(s))
	}
}

// What a plugin sends is untrusted: urls, repos, ids, titles and sizes.
func TestSourceUntrustedOrders(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "state ok", isOK)

	// push sets the plugin's orders, notifies, and waits until Admiral has
	// pulled the list, so an absence check below proves a rejection and not a delay.
	push := func(orders ...map[string]any) {
		before := len(p.callsOf("orders.list"))
		p.mainOrders(orders...)
		p.do("orders-changed", "")
		p.waitCall(5*time.Second, "orders.list", func(rpcCall) bool { return len(p.callsOf("orders.list")) > before })
	}
	push(
		map[string]any{"id": "ok", "title": "Plain", "text": "line1\nline2\tx", "url": "https://example.com/i/1", "hot": true,
			"repo": "https://github.com/acme/widgets"},
		map[string]any{"id": "js", "title": "Evil link", "url": "javascript:alert(1)"},
		map[string]any{"id": "multi", "title": "  two\nlines  ", "text": "t"},
		map[string]any{"id": "badrepo", "title": "Bad repo", "repo": "banana"},
	)
	s := w.waitSource("a", 5*time.Second, "the pushed orders", func(s sourceJSON) bool { return s.hasOrder("js") })
	ok, _ := s.order("ok")
	if ok.URL != "https://example.com/i/1" || !ok.Hot || ok.Text != "line1\nline2\tx" || ok.Repo == "" || ok.Title != "Plain" {
		t.Errorf("a well-formed order = %+v, want url, hot, whole text and repo kept", ok)
	}
	if js, _ := s.order("js"); js.URL != "" {
		t.Errorf("javascript: url kept: %+v", js)
	}
	if m, _ := s.order("multi"); strings.ContainsAny(m.Title, "\r\n") || m.Title == "" {
		t.Errorf("multi-line title = %q, want one trimmed line", m.Title)
	}
	if br, _ := s.order("badrepo"); br.Repo != "" {
		t.Errorf("a repo that is not a remote kept: %+v", br)
	}

	// each of these is a protocol error: the order (or list) must not be shown,
	// and Admiral keeps serving.
	push(ord("ok", "Plain"), ord("ctl\x01id", "Bad id"))
	time.Sleep(200 * time.Millisecond)
	if s, _ := find(w.sources(), "a"); s.hasOrder("ctl\x01id") {
		t.Errorf("an order id with a control character was accepted: %s", dump(s.Panels))
	}
	push(map[string]any{"id": "big", "title": "Big", "text": strings.Repeat("a", 12001)})
	time.Sleep(200 * time.Millisecond)
	if s, _ := find(w.sources(), "a"); s.hasOrder("big") {
		t.Errorf("an order text over 12,000 bytes was accepted")
	}
	many := make([]map[string]any, 5001)
	for i := range many {
		many[i] = ord("n"+strconv.Itoa(i), "n")
	}
	push(many...)
	time.Sleep(500 * time.Millisecond)
	if s, _ := find(w.sources(), "a"); len(s.orderIDs()) > 5000 {
		t.Errorf("an order list over 5,000 orders was accepted (%d)", len(s.orderIDs()))
	}
}

// auth.required shows on the source with its message, a safe url and the code;
// auth.ok clears it.
func TestSourceAuth(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "state ok", isOK)
	ws := w.ws()

	p.do("auth-required", `{"message":"Sign in to Acme","url":"https://acme.example/device","code":"ABCD-1234"}`)
	l := waitSourcesMsg(t, ws, 5*time.Second, "auth_required", func(l []sourceJSON) bool {
		s, _ := find(l, "a")
		return s.State == "auth_required"
	})
	s, _ := find(l, "a")
	want := authJSON{Message: "Sign in to Acme", URL: "https://acme.example/device", Code: "ABCD-1234"}
	if s.Auth == nil || *s.Auth != want {
		t.Errorf("auth = %+v, want %+v", s.Auth, want)
	}

	p.do("auth-required", `{"message":"Try again","url":"javascript:alert(1)"}`)
	s = w.waitSource("a", 5*time.Second, "the new message", func(s sourceJSON) bool { return s.Auth != nil && s.Auth.Message == "Try again" })
	if s.Auth.URL != "" || s.State != "auth_required" {
		t.Errorf("auth = %+v state %s, want the javascript: url dropped and the state kept", s.Auth, s.State)
	}

	p.do("auth-ok", "")
	s = w.waitSource("a", 5*time.Second, "auth cleared", func(s sourceJSON) bool { return s.State == "ok" })
	if s.Auth != nil {
		t.Errorf("auth = %+v after auth.ok, want null", s.Auth)
	}
}

// S4: a crash keeps the last orders, goes read-only, refuses mutations, backs
// off with growing gaps and restarts at once on request.
func TestSourceCrashRecovery(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(ord("o1", "First"), ord("o2", "Second"))
	caps := map[string]any{"capabilities": []string{"add", "edit", "remove", "refresh"}}
	w.startSources(tpSource("a", p, caps))
	w.waitSource("a", 10*time.Second, "both orders", func(s sourceJSON) bool { return isOK(s) && len(s.orderIDs()) == 2 })

	// with every capability a mutation is allowed (and reaches the plugin)
	code, body := w.srcReq(http.MethodPost, "/a/orders", map[string]any{"panel": "main", "text": "new order"})
	w.mustOK(code, body, "add on a healthy source")
	p.waitCall(2*time.Second, "orders.add", func(c rpcCall) bool { return c.Params["panel"] == "main" && c.Params["text"] == "new order" })
	code, body = w.srcReq(http.MethodPut, "/a/orders/o1", map[string]any{"text": "edited"})
	w.mustOK(code, body, "edit on a healthy source")
	p.waitCall(2*time.Second, "orders.edit", func(c rpcCall) bool { return c.Params["id"] == "o1" && c.Params["text"] == "edited" })
	code, body = w.srcReq(http.MethodDelete, "/a/orders/o2", nil)
	w.mustOK(code, body, "remove on a healthy source")
	p.waitCall(2*time.Second, "orders.remove", func(c rpcCall) bool { return c.Params["id"] == "o2" })

	p.put("crash-on-initialize", "")
	p.do("crash", "")
	s := w.waitSource("a", 10*time.Second, "state error", func(s sourceJSON) bool { return s.State == "error" })
	if !s.ReadOnly || s.errText() == "" {
		t.Errorf("crashed source = %s, want read_only true and an error message", dump(s))
	}
	if !reflect.DeepEqual(s.orderIDs(), []string{"o1", "o2"}) {
		t.Errorf("crashed source orders = %v, want the last orders [o1 o2] kept", s.orderIDs())
	}
	w.wantConflict(w.srcReq(http.MethodPost, "/a/orders", map[string]any{"panel": "main", "text": "x"}))
	w.wantConflict(w.srcReq(http.MethodPut, "/a/orders/o1", map[string]any{"text": "x"}))
	w.wantConflict(w.srcReq(http.MethodDelete, "/a/orders/o1", nil))
	w.wantConflict(w.srcReq(http.MethodPost, "/a/refresh", map[string]any{}))

	// restarts with backoff: initialize arrives at least three more times,
	// each gap longer than the one before (1 s, 2 s, 4 s…)
	starts := p.waitStarts(15*time.Second, 4)
	n := len(starts)
	g1, g2 := starts[n-2]-starts[n-3], starts[n-1]-starts[n-2]
	if g1 < 0.8 || g2 < g1+0.5 {
		t.Errorf("gaps between restarts = %.2fs then %.2fs, want growing gaps of about 1 s doubling", g1, g2)
	}

	// settings save works while crashed, and restarts the source
	before := len(p.starts())
	code, body = w.srcReq(http.MethodPut, "/a", map[string]any{"name": "Renamed", "settings": p.settings(caps)})
	w.mustOK(code, body, "settings save on a crashed source")
	p.waitStarts(3*time.Second, before+1)

	// Restart now: the cause is gone and the source is back without waiting out the backoff
	p.clear("crash-on-initialize")
	code, body = w.srcReq(http.MethodPost, "/a/restart", nil)
	w.mustOK(code, body, "POST restart")
	s = w.waitSource("a", 3*time.Second, "state ok", isOK)
	if s.ReadOnly || s.Error != nil || s.Name != "Renamed" {
		t.Errorf("restarted source = %s, want read_only false, no error, the saved name", dump(s))
	}
	code, body = w.srcReq(http.MethodPost, "/a/refresh", map[string]any{})
	w.mustOK(code, body, "refresh after the restart")

	// delete works on a crashed source and its process is gone
	p.put("crash-on-initialize", "")
	p.do("crash", "")
	w.waitSource("a", 10*time.Second, "state error", func(s sourceJSON) bool { return s.State == "error" })
	code, body = w.srcReq(http.MethodDelete, "/a", nil)
	w.mustOK(code, body, "DELETE on a crashed source")
	w.waitSources(5*time.Second, "no sources", func(l []sourceJSON) bool { return len(l) == 0 })
	if got := w.configSources(); len(got) != 0 {
		t.Errorf("config sources after the delete = %v, want none", got)
	}
	time.Sleep(1500 * time.Millisecond)
	stopped := len(p.starts())
	time.Sleep(2500 * time.Millisecond)
	if n := len(p.starts()); n != stopped {
		t.Errorf("a deleted source was started again (%d starts, then %d)", stopped, n)
	}
}

// A crash before panels.list still shows the source's error and Restart.
func TestSourceCrashBeforePanels(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(ord("o1", "First"))
	p.put("crash-on-panels", "")
	w.startSources(tpSource("a", p, nil))
	s := w.waitSource("a", 10*time.Second, "state error", func(s sourceJSON) bool { return s.State == "error" })
	if s.Name != "Test a" || s.Plugin != "testplug" || s.errText() == "" || !s.ReadOnly || len(s.Panels) != 0 {
		t.Errorf("source = %s, want its name, plugin and an error with no panels yet", dump(s))
	}
	p.clear("crash-on-panels")
	code, body := w.srcReq(http.MethodPost, "/a/restart", nil)
	w.mustOK(code, body, "POST restart")
	s = w.waitSource("a", 4*time.Second, "state ok", isOK)
	if len(s.Panels) != 1 || !s.hasOrder("o1") {
		t.Errorf("restarted source = %s, want its panel and order", dump(s))
	}
}

// S4: a launch from a crashed (read-only) source works; its orders.launched is
// delivered after the source next initializes.
func TestLaunchFromCrashedSource(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(map[string]any{"id": "o1", "title": "Title one", "text": "The whole\ntext of one"})
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "order o1", func(s sourceJSON) bool { return isOK(s) && s.hasOrder("o1") })
	p.put("crash-on-initialize", "")
	p.do("crash", "")
	w.waitSource("a", 10*time.Second, "state error", func(s sourceJSON) bool { return s.State == "error" })

	ship := w.mustLaunchFrom("stay", "a", "o1")
	if got := promptOf(t, w, w.record(ship.Session)); got != "The whole\ntext of one" {
		t.Errorf("harness prompt = %q, want the order's text", got)
	}
	if n := len(p.callsOf("orders.launched")); n != 0 {
		t.Errorf("orders.launched reached a crashed plugin (%d)", n)
	}
	p.clear("crash-on-initialize")
	w.mustOK(w.srcReq(http.MethodPost, "/a/restart", nil))
	w.waitSource("a", 4*time.Second, "state ok", isOK)
	p.waitCall(5*time.Second, "orders.launched", func(c rpcCall) bool { return c.Params["id"] == "o1" })
	w.mustTmux("kill-session", "-t", ship.Session)
}

// S4: a ship hides its order while it exists, anchored or underway, and the
// order shows again once the ship is gone; orders.launched tells the plugin.
func TestHiddenOrdersByShips(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(ord("o1", "First"), ord("o2", "Second"), ord("o3", "Third"))
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "three orders", func(s sourceJSON) bool { return len(s.orderIDs()) == 3 })

	anchored := w.mustLaunchFrom("anch", "a", "o1")
	w.in.waitShipsList(t, 10*time.Second, "an anchored ship", func(l []shipJSON) bool { return statusOf(l, anchored.Session) == "anchored" })
	underway := w.mustLaunchFrom("stay", "a", "o2")
	w.waitSource("a", 5*time.Second, "o1 and o2 hidden", func(s sourceJSON) bool { return reflect.DeepEqual(s.orderIDs(), []string{"o3"}) })
	p.waitCall(3*time.Second, "orders.launched", func(c rpcCall) bool { return c.Params["id"] == "o1" })
	p.waitCall(3*time.Second, "orders.launched", func(c rpcCall) bool { return c.Params["id"] == "o2" })

	w.mustOK(send(t, http.MethodPost, w.in.LocalURL+"/api/ships/"+anchored.Session+"/dismiss", nil))
	w.waitSource("a", 5*time.Second, "o1 shown again after Dismiss", func(s sourceJSON) bool { return s.hasOrder("o1") && !s.hasOrder("o2") })
	w.mustTmux("kill-session", "-t", underway.Session)
	w.waitSource("a", 10*time.Second, "o2 shown again once its ship is gone", func(s sourceJSON) bool { return s.hasOrder("o2") })
}

// S4: a request that gets no answer fails after about 10 s; a hung initialize
// counts as a crash.
func TestSourceHungRequests(t *testing.T) {
	t.Run("a hung request only fails that call", func(t *testing.T) {
		w := newWorld(t)
		p := w.newTP("a")
		p.mainOrders(ord("o1", "First"))
		w.startSources(tpSource("a", p, nil))
		w.waitSource("a", 10*time.Second, "state ok", isOK)
		pid := p.pid()
		p.put("hang-orders.refresh", "")
		began := time.Now()
		code, body := w.srcReq(http.MethodPost, "/a/refresh", map[string]any{})
		took := time.Since(began)
		if code/100 == 2 || code == http.StatusConflict {
			t.Errorf("refresh of a hung plugin = %d %s, want a failure that is not 2xx or 409", code, body)
		}
		if took < 9*time.Second || took > 16*time.Second {
			t.Errorf("the hung refresh failed after %s, want about 10 s", took)
		}
		s, _ := find(w.sources(), "a")
		if s.State != "ok" || s.ReadOnly || !p.alive(pid) {
			t.Errorf("after a hung refresh: source %s, plugin alive %v, want the source still ok and running", dump(s), p.alive(pid))
		}
		p.clear("hang-orders.refresh")
		code, body = w.srcReq(http.MethodPost, "/a/refresh", map[string]any{})
		w.mustOK(code, body, "refresh once the plugin answers")
	})
	t.Run("a hung initialize is a crash", func(t *testing.T) {
		w := newWorld(t)
		p := w.newTP("a")
		p.put("hang-initialize", "")
		w.startSources(tpSource("a", p, nil))
		first := w.waitSource("a", 3*time.Second, "state starting", func(s sourceJSON) bool { return s.State == "starting" })
		if first.ReadOnly {
			t.Errorf("a starting source is read_only: %s", dump(first))
		}
		p.waitStarts(3*time.Second, 1)
		pid := p.pid()
		p.waitDead(14*time.Second, pid, "killed after a hung initialize")
		w.waitSource("a", 5*time.Second, "state error", func(s sourceJSON) bool { return s.State == "error" })
		p.waitStarts(8*time.Second, 2)
	})
}

// A slow initialize shows the source as starting until it answers.
func TestSourceSlowInitialize(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.put("slow-initialize", "2")
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 2*time.Second, "state starting", func(s sourceJSON) bool { return s.State == "starting" })
	w.waitSource("a", 8*time.Second, "state ok", isOK)
}

// A garbage line or an oversized line is a protocol error, and Admiral keeps
// running.
func TestSourceProtocolErrors(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(ord("o1", "First"))
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "state ok", isOK)

	for _, cmd := range []string{"garbage", "oversize"} {
		p.do(cmd, "")
		time.Sleep(500 * time.Millisecond)
		select {
		case <-w.in.done:
			t.Fatalf("Admiral exited after a %s line from a plugin; stderr: %s", cmd, w.in.stderrBuf.String())
		default:
		}
		w.waitSource("a", 12*time.Second, "the source usable again after "+cmd, func(s sourceJSON) bool { return isOK(s) && !s.ReadOnly && s.hasOrder("o1") })
		code, body := w.srcReq(http.MethodPost, "/a/refresh", map[string]any{})
		w.mustOK(code, body, "refresh after a "+cmd+" line")
	}
}

// S5: two sources of one plugin run side by side with their own data dirs;
// saving one's settings restarts only it; hand edits and the API start and
// stop sources; a removed source keeps its data dir and its key is not reused.
func TestTwoSourcesOfOnePlugin(t *testing.T) {
	w := newWorld(t)
	a, b, c, d := w.newTP("a"), w.newTP("b"), w.newTP("c"), w.newTP("d")
	a.mainOrders(ord("a1", "From A"))
	b.mainOrders(ord("b1", "From B"))
	defA := tpSource("a", a, map[string]any{"title": "Alpha"})
	defB := tpSource("b", b, map[string]any{"title": "Beta"})
	w.startSources(defA, defB)
	w.waitSources(10*time.Second, "both sources ok", func(l []sourceJSON) bool {
		sa, _ := find(l, "a")
		sb, _ := find(l, "b")
		return isOK(sa) && isOK(sb)
	})
	list := w.sources()
	if !reflect.DeepEqual(keysOf(list), []string{"a", "b"}) {
		t.Errorf("source order = %v, want config order [a b]", keysOf(list))
	}
	sa, sb := list[0], list[1]
	if sa.Panels[0].Title != "Alpha" || sb.Panels[0].Title != "Beta" || !sa.hasOrder("a1") || sa.hasOrder("b1") || !sb.hasOrder("b1") {
		t.Errorf("panels are not separate: %s", dump(list))
	}
	if a.read("datadir") != w.dataDir("a") || b.read("datadir") != w.dataDir("b") {
		t.Errorf("dataDirs = %q, %q, want data/a and data/b", a.read("datadir"), b.read("datadir"))
	}

	// saving A's settings restarts only A
	code, body := w.srcReq(http.MethodPut, "/a", map[string]any{"settings": a.settings(map[string]any{"title": "Alpha 2"})})
	w.mustOK(code, body, "PUT settings of a")
	a.waitStarts(5*time.Second, 2)
	w.waitSource("a", 5*time.Second, "the new title", func(s sourceJSON) bool { return isOK(s) && len(s.Panels) == 1 && s.Panels[0].Title == "Alpha 2" })
	time.Sleep(500 * time.Millisecond)
	if len(b.starts()) != 1 {
		t.Errorf("saving a's settings restarted b (%d starts)", len(b.starts()))
	}
	if got := w.configSources()["a"]["settings"].(map[string]any)["title"]; got != "Alpha 2" {
		t.Errorf("config.json source a title = %v, want Alpha 2 (saved through the config writer)", got)
	}

	// a hand edit adds c, then removes b
	defA.Settings = a.settings(map[string]any{"title": "Alpha 2"})
	defC := tpSource("c", c, nil)
	replaceConfig(t, w.home, w.srcConfig(defA, defB, defC))
	w.waitSource("c", 5*time.Second, "c started", isOK)
	if keys := keysOf(w.sources()); !reflect.DeepEqual(keys, []string{"a", "b", "c"}) {
		t.Errorf("source order after the hand edit = %v, want [a b c]", keys)
	}
	pidB := b.pid()
	replaceConfig(t, w.home, w.srcConfig(defA, defC))
	w.waitSources(5*time.Second, "b dropped", func(l []sourceJSON) bool { _, ok := find(l, "b"); return !ok })
	b.waitDead(5*time.Second, pidB, "b removed by a hand edit")
	if _, err := os.Stat(w.dataDir("b")); err != nil {
		t.Errorf("data/b was removed with its source: %v", err)
	}

	// the API: add generates a unique key and never reuses a data dir
	body1 := map[string]any{"plugin": "testplug", "name": "New", "settings": d.settings(nil)}
	code, raw := w.srcReq(http.MethodPost, "", body1)
	w.mustOK(code, raw, "POST /api/sources")
	var added []string
	for _, k := range keysOf(w.sources()) {
		if k != "a" && k != "c" {
			added = append(added, k)
		}
	}
	if len(added) != 1 || !sourceKeyRe.MatchString(added[0]) || !strings.Contains(added[0], "testplug") {
		t.Fatalf("added source keys = %v, want one key of letters, digits, - and _ made from the plugin name", added)
	}
	code, raw = w.srcReq(http.MethodDelete, "/"+added[0], nil)
	w.mustOK(code, raw, "DELETE the added source")
	code, raw = w.srcReq(http.MethodPost, "", body1)
	w.mustOK(code, raw, "POST /api/sources again")
	var again []string
	for _, k := range keysOf(w.sources()) {
		if k != "a" && k != "c" {
			again = append(again, k)
		}
	}
	if len(again) != 1 || again[0] == added[0] || again[0] == "b" {
		t.Errorf("second generated key = %v, want a key that is neither %q nor b (their data dirs exist)", again, added[0])
	}

	// deleting a drops its panels from the clients
	ws := w.ws()
	code, raw = w.srcReq(http.MethodDelete, "/a", nil)
	w.mustOK(code, raw, "DELETE a")
	waitSourcesMsg(t, ws, 5*time.Second, "a gone", func(l []sourceJSON) bool { _, ok := find(l, "a"); return !ok })
}

// S6: orders.changed and panels.changed reach a connected client with no
// refresh call.
func TestSourcePushNotifications(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "state ok", isOK)
	ws := w.ws()
	waitSourcesMsg(t, ws, 5*time.Second, "the source on connect", func(l []sourceJSON) bool {
		s, ok := find(l, "a")
		return ok && len(s.orderIDs()) == 0
	})

	p.mainOrders(ord("n1", "New one"), ord("n2", "New two"))
	p.do("orders-changed", `{"panel":"main"}`)
	waitSourcesMsg(t, ws, 5*time.Second, "the pushed orders", func(l []sourceJSON) bool {
		s, _ := find(l, "a")
		return reflect.DeepEqual(s.orderIDs(), []string{"n1", "n2"})
	})

	p.put("panels.json", `[{"id":"main","title":"Main"},{"id":"extra","title":"Extra"}]`)
	p.do("panels-changed", "")
	waitSourcesMsg(t, ws, 5*time.Second, "two panels", func(l []sourceJSON) bool {
		s, _ := find(l, "a")
		return len(s.Panels) == 2 && s.Panels[1].Title == "Extra"
	})
	if n := len(p.callsOf("orders.refresh")); n != 0 {
		t.Errorf("the plugin received %d orders.refresh calls, want none: Admiral pulls on a notification, it does not ask for a refresh", n)
	}
}

// Launch orders asks the server for directories with a source and an order:
// the server looks the order's repo up itself (story 60).
func TestLaunchOrdersDirsFromSource(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(map[string]any{"id": "o1", "title": "Widgets", "repo": "https://github.com/acme/widgets"})
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "order o1", func(s sourceJSON) bool { return isOK(s) && s.hasOrder("o1") })
	widgets := w.repo("widgets", "https://github.com/acme/widgets")
	w.repo("other", "https://github.com/acme/other")

	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/launch-orders/dirs",
		map[string]any{"fleet": "stay", "harness": "claude", "source": "a", "order_id": "o1"})
	if code != http.StatusOK {
		t.Fatalf("POST /api/launch-orders/dirs = %d, want 200: %s", code, body)
	}
	var r dirsResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if r.selected() != widgets {
		t.Errorf("selected = %q, want %q (the checkout of the order's repo, looked up by the server)", r.selected(), widgets)
	}
}
