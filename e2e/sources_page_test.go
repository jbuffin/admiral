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

type pageJSON struct {
	State  string `json:"state"`
	Groups []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Rows  []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"rows"`
	} `json:"groups"`
	Waiting int       `json:"waiting"`
	Error   string    `json:"error"`
	Auth    *authJSON `json:"auth"`
}

func (p *tplug) putRow(id string, detail string) {
	p.t.Helper()
	if err := os.MkdirAll(filepath.Join(p.dir, "rows"), 0o755); err != nil {
		p.t.Fatal(err)
	}
	tmp := filepath.Join(p.dir, "rows", ".tmp-"+id)
	if err := os.WriteFile(tmp, []byte(detail), 0o600); err != nil {
		p.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(p.dir, "rows", id+".json")); err != nil {
		p.t.Fatal(err)
	}
}

func (w *world) getPage(key string) pageJSON {
	w.t.Helper()
	code, body := w.srcReq(http.MethodGet, "/"+key+"/page", nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET page = %d, want 200: %s", code, body)
	}
	var p pageJSON
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		w.t.Fatalf("page is not JSON: %v\n%s", err, body)
	}
	return p
}

func (w *world) waitPageReady(key string) pageJSON {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if p := w.getPage(key); p.State == "ready" {
			return p
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 10s the page of %q never became ready; last: %s", key, dump(w.getPage(key)))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (p pageJSON) titles() []string {
	out := []string{}
	for _, g := range p.Groups {
		out = append(out, g.Title)
	}
	return out
}

const modulePage = `{"groups":[{"id":"review","title":"Needs review","rows":[{"id":"t1","title":"Fix login","chips":[{"label":"P1","tone":"warn"}]}]}],"waiting":2}`

func TestAPluginThatDeclaresAPageHasItsPageAndRowDetailServed(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := w.newTP("m")
	p.put("page.json", modulePage)
	p.putRow("t1", `{"sections":[{"kind":"fields","title":"Details","fields":[{"label":"Owner","value":"amy"}]}]}`)
	w.startSources(tpSource("m", p, map[string]any{"capabilities": []string{"page"}}))

	s := w.waitSource("m", 10*time.Second, "state ok", isOK)
	if !reflect.DeepEqual(s.Capabilities, []string{"page"}) {
		t.Errorf("capabilities = %v, want [page]", s.Capabilities)
	}
	got := w.waitPageReady("m")
	if !reflect.DeepEqual(got.titles(), []string{"Needs review"}) || got.Waiting != 2 || len(got.Groups[0].Rows) != 1 || got.Groups[0].Rows[0].ID != "t1" {
		t.Errorf("page = %s, want the plugin's page.json", dump(got))
	}
	if n := len(p.callsOf("page.get")); n != 1 {
		t.Errorf("page.get calls = %d, want one at start", n)
	}

	code, body := w.srcReq(http.MethodGet, "/m/rows/t1?include=closed&include=queued", nil)
	w.mustOK(code, body, "GET row")
	if !strings.Contains(body, `"Owner"`) || !strings.Contains(body, `"amy"`) {
		t.Errorf("GET row = %s, want the plugin's detail view", body)
	}
	call := p.waitCall(2*time.Second, "row.get", func(c rpcCall) bool { return c.Params["id"] == "t1" })
	if inc, _ := call.Params["include"].([]any); !reflect.DeepEqual(inc, []any{"closed", "queued"}) {
		t.Errorf("row.get params = %v, want include [closed queued]", call.Params)
	}

	if code, _ := w.srcReq(http.MethodGet, "/m/rows/not-on-the-page", nil); code != http.StatusNotFound {
		t.Errorf("GET row that is not on the page = %d, want 404", code)
	}
	tooMany := "/m/rows/t1?" + strings.Repeat("include=x&", 11)
	if code, _ := w.srcReq(http.MethodGet, tooMany, nil); code != http.StatusBadRequest {
		t.Errorf("GET row with 11 include values = %d, want 400", code)
	}
	if n := len(p.callsOf("row.get")); n != 1 {
		t.Errorf("row.get calls = %d, want only the one for t1", n)
	}
}

func TestASourceWithoutAPageIsUnchangedAndAnswersItsPageRoutesWith404(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := w.newTP("o")
	p.mainOrders(ord("o1", "First"))
	w.startSources(tpSource("o", p, nil))
	s := w.waitSource("o", 10*time.Second, "state ok", isOK)
	if !reflect.DeepEqual(s.Capabilities, []string{"refresh"}) {
		t.Errorf("capabilities = %v, want [refresh] as before", s.Capabilities)
	}
	for _, path := range []string{"/o/page", "/o/rows/t1"} {
		code, body := w.srcReq(http.MethodGet, path, nil)
		if code != http.StatusNotFound || !strings.Contains(body, "The source has no page") {
			t.Errorf("GET %s = %d %s, want 404 \"The source has no page\"", path, code, body)
		}
	}
	if code, _ := w.srcReq(http.MethodGet, "/nope/page", nil); code != http.StatusNotFound {
		t.Errorf("GET page of a source that does not exist = %d, want 404", code)
	}
	if n := len(p.callsOf("page.get")) + len(p.callsOf("row.get")); n != 0 {
		t.Errorf("the plugin got %d page calls, want none: it declared no page", n)
	}
}

func TestARestartThatBringsAnOverLimitPageKeepsServingTheLastGoodOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := w.newTP("m")
	p.put("page.json", modulePage)
	w.startSources(tpSource("m", p, map[string]any{"capabilities": []string{"page"}}))
	w.waitPageReady("m")

	groups := make([]string, 101)
	for i := range groups {
		groups[i] = `{"id":"g` + strconv.Itoa(i) + `","title":"G","rows":[]}`
	}
	p.put("page.json", `{"groups":[`+strings.Join(groups, ",")+`],"waiting":9}`)
	w.mustOK(w.srcReq(http.MethodPost, "/m/restart", nil))
	p.waitStarts(10*time.Second, 2)
	// State ok is set only after the restarted plugin's page.get was read, so the bad page has been refused by now.
	w.waitSource("m", 10*time.Second, "state ok again", isOK)

	got := w.getPage("m")
	if got.State != "ready" || !reflect.DeepEqual(got.titles(), []string{"Needs review"}) || got.Waiting != 2 {
		t.Errorf("page after the restart = %s, want the last good page (Needs review, waiting 2)", dump(got))
	}
}

func pageWith(title string, waiting int) string {
	return `{"groups":[{"id":"g","title":"` + title + `","rows":[{"id":"t1","title":"Fix login"},{"id":"t2","title":"Add search"}]}],"waiting":` + strconv.Itoa(waiting) + `}`
}

func (w *world) waitLive(key, what string, pred func(*livePage) bool) *livePage {
	w.t.Helper()
	return w.waitSource(key, 10*time.Second, what, func(s sourceJSON) bool { return s.Page != nil && pred(s.Page) }).Page
}

func TestAModulesPageStaysLiveWhenItsPluginSendsPageChanged(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := w.newTP("m")
	p.put("page.json", pageWith("Before", 1))
	o := w.newTP("o")
	o.mainOrders(ord("o1", "First"))
	w.startSources(tpSource("m", p, map[string]any{"capabilities": []string{"page"}}), tpSource("o", o, nil))

	first := w.waitLive("m", "a ready page block", func(l *livePage) bool { return l.State == "ready" })
	if first.Waiting != 1 {
		t.Errorf("page block = %s, want waiting 1 from the page", dump(first))
	}
	code, body := w.srcReq(http.MethodGet, "", nil)
	w.mustOK(code, body, "GET sources")
	var raw []map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		var wrap struct {
			Sources []map[string]any `json:"sources"`
		}
		if err := json.Unmarshal([]byte(body), &wrap); err != nil {
			t.Fatalf("GET sources is not JSON: %v", err)
		}
		raw = wrap.Sources
	}
	for _, s := range raw {
		if _, has := s["page"]; has && s["key"] == "o" {
			t.Errorf("the source without a page has a page key: %s", dump(s))
		}
	}

	p.put("page.json", pageWith("After", 4))
	p.do("page-changed", `{"rows":["t1"]}`)
	named := w.waitLive("m", "a higher version after page.changed", func(l *livePage) bool { return l.Version > first.Version })
	if !reflect.DeepEqual(named.Rows, []string{"t1"}) || named.Waiting != 4 {
		t.Errorf("page block = %s, want rows [t1] and waiting 4", dump(named))
	}
	if got := w.getPage("m"); !reflect.DeepEqual(got.titles(), []string{"After"}) || got.Waiting != 4 {
		t.Errorf("GET page right after the version moved = %s, want the new data", dump(got))
	}
	if n := len(p.callsOf("page.get")); n != 2 {
		t.Errorf("page.get calls = %d, want the start and one for page.changed", n)
	}

	p.do("page-changed", "")
	all := w.waitLive("m", "a higher version after an unnamed page.changed", func(l *livePage) bool { return l.Version > named.Version })
	if len(all.Rows) != 0 {
		t.Errorf("page block = %s, want no rows named: all rows changed", dump(all))
	}

	groups := make([]string, 101)
	for i := range groups {
		groups[i] = `{"id":"g` + strconv.Itoa(i) + `","title":"Over","rows":[]}`
	}
	p.put("page.json", `{"groups":[`+strings.Join(groups, ",")+`],"waiting":9}`)
	p.do("page-changed", "")
	refused := w.waitLive("m", "a higher version after page.changed whose page.get is over a limit", func(l *livePage) bool { return l.Version > all.Version })
	if refused.State != "ready" {
		t.Errorf("page block = %s, want it still ready on the last good page", dump(refused))
	}
	if got := w.getPage("m"); got.State != "ready" || !reflect.DeepEqual(got.titles(), []string{"After"}) || got.Waiting != 4 {
		t.Errorf("GET page after an over-limit answer = %s, want the last good page (After, waiting 4)", dump(got))
	}

	before := len(p.callsOf("page.get"))
	w.mustOK(w.srcReq(http.MethodPost, "/m/page/refresh", nil))
	p.waitCall(5*time.Second, "page.refresh", func(rpcCall) bool { return true })
	deadline := time.Now().Add(5 * time.Second)
	for len(p.callsOf("page.get")) <= before {
		if time.Now().After(deadline) {
			t.Fatalf("within 5s the page was not asked again after page/refresh; calls: %+v", p.calls())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if code, body := w.srcReq(http.MethodPost, "/o/page/refresh", nil); code != http.StatusNotFound || !strings.Contains(body, "The source has no page") {
		t.Errorf("POST page/refresh of a source without a page = %d %s, want 404", code, body)
	}
	if n := len(o.callsOf("page.refresh")); n != 0 {
		t.Errorf("the source without a page got %d page.refresh calls, want none", n)
	}
}

func TestAModulesPageShowsAuthRequiredAndKeepsItsGroupsUntilAuthOK(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := w.newTP("m")
	p.put("page.json", pageWith("Kept", 2))
	w.startSources(tpSource("m", p, map[string]any{"capabilities": []string{"page"}}))
	ready := w.waitLive("m", "a ready page block", func(l *livePage) bool { return l.State == "ready" })

	p.do("auth-required", `{"message":"Sign in to Acme","url":"https://acme.example/login","code":"AB-12"}`)
	auth := w.waitLive("m", "auth_required", func(l *livePage) bool { return l.State == "auth_required" })
	if auth.Version <= ready.Version {
		t.Errorf("version %d -> %d, want it up on a state change", ready.Version, auth.Version)
	}
	got := w.getPage("m")
	if got.State != "auth_required" || got.Auth == nil || got.Auth.Message != "Sign in to Acme" || got.Auth.URL != "https://acme.example/login" || got.Auth.Code != "AB-12" {
		t.Errorf("GET page = %s, want auth_required with the sign-in fields", dump(got))
	}
	if !reflect.DeepEqual(got.titles(), []string{"Kept"}) {
		t.Errorf("GET page groups = %v, want the last good groups kept", got.titles())
	}

	p.do("auth-ok", "")
	w.waitLive("m", "ready again", func(l *livePage) bool { return l.State == "ready" })
	if got := w.getPage("m"); got.State != "ready" || got.Auth != nil {
		t.Errorf("GET page after auth.ok = %s, want ready with no auth", dump(got))
	}
}
