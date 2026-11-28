package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
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
	var got pageJSON
	eventually(w.t, fmt.Sprintf("the page of %q becoming ready", key), func() (bool, string) {
		got = w.getPage(key)
		return got.State == "ready", "last: " + dump(got)
	})
	return got
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

	s := w.waitSource("m", "state ok", isOK)
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
	call := p.waitCall("row.get", func(c rpcCall) bool { return c.Params["id"] == "t1" })
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
	s := w.waitSource("o", "state ok", isOK)
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
	p.waitStarts(2)
	// State ok is set only after the restarted plugin's page.get was read, so the bad page has been refused by now.
	w.waitSource("m", "state ok again", isOK)

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
	return w.waitSource(key, what, func(s sourceJSON) bool { return s.Page != nil && pred(s.Page) }).Page
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
	if plain := w.waitSource("o", "state ok", isOK); plain.Page != nil {
		t.Errorf("the source without a page has a page block: %s", dump(plain))
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

	w.mustOK(w.srcReq(http.MethodPost, "/m/page/refresh", nil))
	p.waitCall("page.refresh", func(rpcCall) bool { return true })

	if code, body := w.srcReq(http.MethodPost, "/o/page/refresh", nil); code != http.StatusNotFound || !strings.Contains(body, "The source has no page") {
		t.Errorf("POST page/refresh of a source without a page = %d %s, want 404", code, body)
	}
}

func (p *tplug) putAnswer(dir, id, content string) {
	p.t.Helper()
	if err := os.MkdirAll(filepath.Join(p.dir, dir), 0o755); err != nil {
		p.t.Fatal(err)
	}
	tmp := filepath.Join(p.dir, dir, ".tmp-"+id)
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		p.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(p.dir, dir, id+".json")); err != nil {
		p.t.Fatal(err)
	}
}

func TestAModuleActionFormIsFetchedAndRunThroughThePluginProcess(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := w.newTP("m")
	p.put("page.json", `{"groups":[{"id":"review","title":"Needs review","rows":[{"id":"t1","title":"Fix login","action":{"id":"pass","label":"Pass review","confirm":"form"}}]}],"waiting":1}`)
	p.putAnswer("forms", "pass", `{"title":"Pass review","fields":[{"id":"time","label":"Time","type":"duration","default":"01:30","min":"00:15"}]}`)
	p.putAnswer("runs", "pass", `{"message":"Review passed","url":"https://acme.test/T-1"}`)
	w.startSources(tpSource("m", p, map[string]any{"capabilities": []string{"page"}}))
	w.waitPageReady("m")

	code, body := w.srcReq(http.MethodPost, "/m/rows/t1/actions/pass/form", nil)
	w.mustOK(code, body, "POST form")
	var form struct {
		Title  string `json:"title"`
		Fields []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Default string `json:"default"`
			Min     string `json:"min"`
		} `json:"fields"`
	}
	if err := json.Unmarshal([]byte(body), &form); err != nil || form.Title != "Pass review" || len(form.Fields) != 1 ||
		form.Fields[0].ID != "time" || form.Fields[0].Type != "duration" || form.Fields[0].Default != "01:30" || form.Fields[0].Min != "00:15" {
		t.Errorf("form = %s (%v), want the plugin's forms/pass.json", body, err)
	}
	call := p.waitCall("action.form", func(rpcCall) bool { return true })
	if call.Params["row"] != "t1" || call.Params["action"] != "pass" {
		t.Errorf("action.form params = %v, want row t1 and action pass", call.Params)
	}

	code, body = w.srcReq(http.MethodPost, "/m/rows/t1/actions/pass/run", map[string]any{"values": map[string]any{"time": "01:45"}})
	w.mustOK(code, body, "POST run")
	if !strings.Contains(body, `"message":"Review passed"`) || !strings.Contains(body, `"url":"https://acme.test/T-1"`) {
		t.Errorf("run answer = %s, want the plugin's runs/pass.json", body)
	}
	run := p.waitCall("action.run", func(rpcCall) bool { return true })
	values, _ := run.Params["values"].(map[string]any)
	if run.Params["row"] != "t1" || run.Params["action"] != "pass" || values["time"] != "01:45" {
		t.Errorf("action.run params = %v, want row t1, action pass and values {time: 01:45}", run.Params)
	}
	if n := len(p.callsOf("action.form")) + len(p.callsOf("action.run")); n != 2 {
		t.Errorf("module action calls = %d, want one form fetch and one run", n)
	}
}
