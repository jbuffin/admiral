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

// A module's page and row detail over the API, from the test plugin's page.json and rows/<id>.json.

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
	Waiting int `json:"waiting"`
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
	w.waitSource("m", 10*time.Second, "state ok", isOK)
	w.waitPageReady("m")

	groups := make([]string, 101)
	for i := range groups {
		groups[i] = `{"id":"g` + strconv.Itoa(i) + `","title":"G","rows":[]}`
	}
	p.put("page.json", `{"groups":[`+strings.Join(groups, ",")+`],"waiting":9}`)
	w.mustOK(w.srcReq(http.MethodPost, "/m/restart", nil))
	p.waitStarts(10*time.Second, 2)
	w.waitSource("m", 10*time.Second, "state ok again", isOK)
	waitUntil(t, "the restarted plugin's page.get", func() bool { return len(p.callsOf("page.get")) >= 2 })

	got := w.getPage("m")
	if got.State != "ready" || !reflect.DeepEqual(got.titles(), []string{"Needs review"}) || got.Waiting != 2 {
		t.Errorf("page after the restart = %s, want the last good page (Needs review, waiting 2)", dump(got))
	}
}
