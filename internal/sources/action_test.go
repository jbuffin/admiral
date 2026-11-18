package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// actionConn answers action.form and action.run as the test says, and everything else through its pageConn.
type actionConn struct {
	*pageConn
	mu       sync.Mutex
	acalls   []pageCall
	form     json.RawMessage
	formErr  error
	run      json.RawMessage
	runErr   error
	hold     bool          // action.run blocks until release, fire or its context ends
	entered  chan struct{} // receives once for each held run that has started
	release  chan struct{}
	fire     chan struct{} // stands for the run timeout passing
	canceled bool
}

func newActionConn(page string) *actionConn {
	return &actionConn{
		pageConn: &pageConn{page: json.RawMessage(page), rows: map[string]json.RawMessage{}},
		entered:  make(chan struct{}, 10), release: make(chan struct{}), fire: make(chan struct{}),
		run: json.RawMessage(`{"message":"Done"}`),
	}
}

func (c *actionConn) call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	switch method {
	case "action.form":
		c.record(method, params, timeout)
		return c.form, c.formErr
	case "action.run":
		c.record(method, params, timeout)
		c.mu.Lock()
		hold := c.hold
		c.mu.Unlock()
		if hold {
			c.entered <- struct{}{}
			select {
			case <-c.release:
			case <-c.fire:
				return nil, errTimeout
			case <-ctx.Done():
				c.mu.Lock()
				c.canceled = true
				c.mu.Unlock()
				return nil, ctx.Err()
			}
		}
		return c.run, c.runErr
	}
	return c.pageConn.call(ctx, method, params, timeout)
}

func (c *actionConn) record(method string, params any, timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acalls = append(c.acalls, pageCall{method, dump(params), timeout})
}

func (c *actionConn) actionCalls() []pageCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pageCall(nil), c.acalls...)
}

func (c *actionConn) wasCanceled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canceled
}

const actionsPage = `[{"id":"g","title":"G","rows":[
	{"id":"t1","title":"Fix login",
	 "action":{"id":"pass","label":"Pass review","confirm":"form"},
	 "actions":[
	  {"id":"close","label":"Close","confirm":"click"},
	  {"id":"ask","label":"Ask","confirm":"yesno","summary":"Sure?"},
	  {"id":"open","label":"Open","confirm":"click","url":"https://acme.test/T-1"}]},
	{"id":"t2","title":"Add search","action":{"id":"pass","label":"Pass review","confirm":"form"}}]}]`

const t1Detail = `{"sections":[{"kind":"steps","title":"Steps","steps":[
	{"id":"s1","title":"Review","actions":[
	  {"id":"retry","label":"Retry","confirm":"form"},
	  {"id":"close","label":"Close too","confirm":"form"}]}]}]}`

func actionFixture(t *testing.T, c *actionConn) (*serviceFixture, *runner) {
	t.Helper()
	f, rn := pageFixture(t, []string{"page"}, false, true, c)
	rn.pullPage(t.Context(), c)
	return f, rn
}

func actionPath(row, action, verb string) string {
	return "/m/rows/" + row + "/actions/" + action + "/" + verb
}

func (f *serviceFixture) runAction(row, action string, values any) (int, string) {
	f.t.Helper()
	return f.do(http.MethodPost, actionPath(row, action, "run"), map[string]any{"values": values})
}

func (f *serviceFixture) formOf(row, action string) (int, string) {
	f.t.Helper()
	return f.do(http.MethodPost, actionPath(row, action, "form"), nil)
}

func actionAnswerOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%q is not a JSON object: %v", body, err)
	}
	return m
}

func actionErrorLine(t *testing.T, body string) string {
	t.Helper()
	msg, _ := actionAnswerOf(t, body)["error"].(string)
	if msg == "" || strings.ContainsAny(msg, "\r\n") {
		t.Errorf("error = %q, want one non-empty line", msg)
	}
	return msg
}

func sameActionJSON(t *testing.T, what, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Errorf("%s = %q, not JSON: %v", what, got, err)
		return
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if dump(g) != dump(w) {
		t.Errorf("%s = %s, want %s", what, dump(g), dump(w))
	}
}

func TestAFormFetchReachesThePluginWithinTheCallLimitAndServesTheValidatedForm(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.form = json.RawMessage(`{"title":"Pass review\nsecond line","fields":[
		{"id":"whose","label":"Whose work","type":"choice","options":[{"id":"a","label":"Amy\nAmy"},{"id":"b","label":"Bo"}],"default":"b"},
		{"id":"files","label":"Files","type":"checklist","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}],"default":["y"]},
		{"id":"time","label":"Time","type":"duration","default":"01:30","min":"00:15"}]}`)
	f, _ := actionFixture(t, c)
	code, body := f.formOf("t1", "pass")
	if code != http.StatusOK {
		t.Fatalf("POST form = %d %s, want 200", code, body)
	}
	sameActionJSON(t, "form", body, `{"title":"Pass review","fields":[
		{"id":"whose","label":"Whose work","type":"choice","options":[{"id":"a","label":"Amy"},{"id":"b","label":"Bo"}],"default":"b"},
		{"id":"files","label":"Files","type":"checklist","options":[{"id":"x","label":"X"},{"id":"y","label":"Y"}],"default":["y"]},
		{"id":"time","label":"Time","type":"duration","default":"01:30","min":"00:15"}]}`)
	calls := c.actionCalls()
	if len(calls) != 1 || calls[0].method != "action.form" || calls[0].timeout != callTimeout {
		t.Fatalf("plugin calls = %+v, want one action.form within the call limit %s", calls, callTimeout)
	}
	sameActionJSON(t, "action.form params", calls[0].params, `{"row":"t1","action":"pass"}`)
}

func TestAFormDropsADefaultThatDoesNotFitItsFieldAndKeepsTheRest(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.form = json.RawMessage(`{"fields":[
		{"id":"whose","label":"Whose","type":"choice","options":[{"id":"a","label":"A"}],"default":"zzz"},
		{"id":"files","label":"Files","type":"checklist","options":[{"id":"x","label":"X"}],"default":["x","zzz"]},
		{"id":"all","label":"All","type":"checklist","options":[{"id":"x","label":"X"}]}]}`)
	f, _ := actionFixture(t, c)
	code, body := f.formOf("t1", "pass")
	if code != http.StatusOK {
		t.Fatalf("POST form = %d %s, want 200", code, body)
	}
	sameActionJSON(t, "form", body, `{"fields":[
		{"id":"whose","label":"Whose","type":"choice","options":[{"id":"a","label":"A"}]},
		{"id":"files","label":"Files","type":"checklist","options":[{"id":"x","label":"X"}]},
		{"id":"all","label":"All","type":"checklist","options":[{"id":"x","label":"X"}]}]}`)
}

func repeatFormFields(n int) string {
	return `{"fields":` + repeat(`{"id":"f","label":"F","type":"choice","options":[{"id":"a","label":"A"}]}`, n) + `}`
}

func optionsFormOf(n int) string {
	opts := make([]string, n)
	for i := range opts {
		opts[i] = `{"id":"o` + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676)) + `","label":"O"}`
	}
	return `{"fields":[{"id":"f","label":"F","type":"checklist","options":[` + strings.Join(opts, ",") + `]}]}`
}

func TestAFormAtEveryLimitIsKept(t *testing.T) {
	for name, form := range map[string]string{
		"the most fields":  repeatFormFields(maxFormFields),
		"the most options": optionsFormOf(maxFormOptions),
	} {
		c := newActionConn(pageOf(actionsPage, 0))
		c.form = json.RawMessage(form)
		f, _ := actionFixture(t, c)
		if code, body := f.formOf("t1", "pass"); code != http.StatusOK {
			t.Errorf("%s: POST form = %d %.200s, want 200", name, code, body)
		}
	}
}

func TestAnInvalidFormAnswerIs502WithOneLineAndNothingElseChanges(t *testing.T) {
	bigTitle := `{"title":"` + strings.Repeat("x", maxFormBytes) + `","fields":[]}`
	for name, form := range map[string]string{
		"not an object":                 `"form"`,
		"fields not a list":             `{"fields":"x"}`,
		"an unknown field type":         `{"fields":[{"id":"f","label":"F","type":"text"}]}`,
		"a min that is not hh:mm":       `{"fields":[{"id":"f","label":"F","type":"duration","min":"90"}]}`,
		"a duration default not hh:mm":  `{"fields":[{"id":"f","label":"F","type":"duration","default":"1h30"}]}`,
		"minutes past 59":               `{"fields":[{"id":"f","label":"F","type":"duration","default":"00:75"}]}`,
		"one field too many":            repeatFormFields(maxFormFields + 1),
		"one option too many":           optionsFormOf(maxFormOptions + 1),
		"a field id over the id cap":    `{"fields":[{"id":"` + strings.Repeat("i", maxIDRunes+1) + `","label":"F","type":"choice","options":[]}]}`,
		"an answer over the byte limit": bigTitle,
	} {
		c := newActionConn(pageOf(actionsPage, 4))
		c.form = json.RawMessage(form)
		f, rn := actionFixture(t, c)
		before, _ := rn.Page()
		code, body := f.formOf("t1", "pass")
		if code != http.StatusBadGateway {
			t.Errorf("%s: POST form = %d %.200s, want 502", name, code, body)
			continue
		}
		actionErrorLine(t, body)
		if after, _ := rn.Page(); dump(after) != dump(before) {
			t.Errorf("%s: the page changed after a refused form: %s", name, dump(after))
		}
	}
}

func TestAFormFailureOfThePluginMapsLikeAnyOtherCall(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"the plugin refuses": {&rpcError{Code: -32000, Message: "closed upstream"}, http.StatusUnprocessableEntity},
		"a timeout":          {errTimeout, http.StatusGatewayTimeout},
		"a call never sent":  {&notSentError{err: errors.New("pipe closed")}, http.StatusBadGateway},
	} {
		c := newActionConn(pageOf(actionsPage, 0))
		c.formErr = tc.err
		f, _ := actionFixture(t, c)
		if code, body := f.formOf("t1", "pass"); code != tc.want {
			t.Errorf("%s: POST form = %d %s, want %d", name, code, body, tc.want)
		}
	}
}

func TestARunPassesItsValuesOnAndServesTheMessageAndSafeLinkWithinTheRunLimit(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.run = json.RawMessage(`{"message":"Review passed","url":"https://acme.test/T-1"}`)
	f, _ := actionFixture(t, c)
	code, body := f.runAction("t1", "pass", map[string]any{"whose": "b", "files": []string{"x", "y"}, "time": "01:30"})
	if code != http.StatusOK {
		t.Fatalf("POST run = %d %s, want 200", code, body)
	}
	sameActionJSON(t, "run answer", body, `{"message":"Review passed","url":"https://acme.test/T-1"}`)
	calls := c.actionCalls()
	if len(calls) != 1 || calls[0].method != "action.run" || calls[0].timeout != runTimeout {
		t.Fatalf("plugin calls = %+v, want one action.run with the run limit %s", calls, runTimeout)
	}
	sameActionJSON(t, "action.run params", calls[0].params, `{"row":"t1","action":"pass","values":{"whose":"b","files":["x","y"],"time":"01:30"}}`)
	if runTimeout <= callTimeout {
		t.Errorf("run limit %s is not longer than the call limit %s", runTimeout, callTimeout)
	}
}

func TestARunAnswerIsCutToOneLineAndAnUnsafeLinkIsLeftOut(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.run = json.RawMessage(`{"message":"` + strings.Repeat("m", 400) + `\nsecond line","url":"javascript:alert(1)"}`)
	f, _ := actionFixture(t, c)
	code, body := f.runAction("t1", "close", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("POST run = %d %s, want 200", code, body)
	}
	got := actionAnswerOf(t, body)
	msg, _ := got["message"].(string)
	if n := len([]rune(msg)); n == 0 || n > 300 || strings.Contains(msg, "second") || strings.ContainsAny(msg, "\r\n") || !strings.HasPrefix(msg, "mmm") {
		t.Errorf("message = %q (%d runes), want the first line cut to at most 300", msg, n)
	}
	if _, has := got["url"]; has {
		t.Errorf("answer = %s, want the unsafe url left out", body)
	}
}

func TestARunTheModuleRefusesIs422WithItsCutMessage(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.runErr = &rpcError{Code: -32000, Message: strings.Repeat("e", 400) + "\nsecond line"}
	f, _ := actionFixture(t, c)
	code, body := f.runAction("t1", "close", map[string]any{})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("POST run = %d %s, want 422", code, body)
	}
	msg := actionErrorLine(t, body)
	if n := len([]rune(msg)); n > 300 || strings.Contains(msg, "second") || !strings.HasPrefix(msg, "eee") {
		t.Errorf("error = %q (%d runes), want the first line cut to at most 300", msg, n)
	}
}

func TestARunFailureOtherThanARefusalMapsLikeAnyOtherCall(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"a timeout":         {errTimeout, http.StatusGatewayTimeout},
		"a call never sent": {&notSentError{err: errors.New("pipe closed")}, http.StatusBadGateway},
	} {
		c := newActionConn(pageOf(actionsPage, 0))
		c.runErr = tc.err
		f, _ := actionFixture(t, c)
		if code, body := f.runAction("t1", "close", map[string]any{}); code != tc.want {
			t.Errorf("%s: POST run = %d %s, want %d", name, code, body, tc.want)
		}
	}
}

func bigRunValues(n int) map[string]any {
	v := map[string]any{}
	for i := 0; i < n; i++ {
		v["k"+strings.Repeat("a", i%7)+string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}
	return v
}

func TestRunValuesAtEveryLimitAreKeptAndAnythingOverOrOfTheWrongShapeIs400(t *testing.T) {
	ok := map[string]map[string]any{
		"the most fields":       bigRunValues(maxRunValues),
		"the longest string":    {"a": strings.Repeat("v", maxRunValueRunes)},
		"the longest checklist": {"a": make([]string, maxRunItems)},
		"no values":             {},
	}
	bad := map[string]any{
		"one field too many":      bigRunValues(maxRunValues + 1),
		"a string too long":       map[string]any{"a": strings.Repeat("v", maxRunValueRunes+1)},
		"a checklist too long":    map[string]any{"a": make([]string, maxRunItems+1)},
		"a number":                map[string]any{"a": 1},
		"a nested object":         map[string]any{"a": map[string]any{"b": "c"}},
		"a list holding a number": map[string]any{"a": []any{"x", 2}},
		"values as a list":        []string{"x"},
		"values as a string":      "x",
	}
	for name, v := range ok {
		c := newActionConn(pageOf(actionsPage, 0))
		f, _ := actionFixture(t, c)
		if code, body := f.runAction("t1", "close", v); code != http.StatusOK {
			t.Errorf("%s: POST run = %d %.200s, want 200", name, code, body)
		}
	}
	for name, v := range bad {
		c := newActionConn(pageOf(actionsPage, 0))
		f, _ := actionFixture(t, c)
		if code, body := f.runAction("t1", "close", v); code != http.StatusBadRequest {
			t.Errorf("%s: POST run = %d %.200s, want 400", name, code, body)
		}
		if n := len(c.actionCalls()); n != 0 {
			t.Errorf("%s: the plugin got %d calls, want none for a refused body", name, n)
		}
	}
	c := newActionConn(pageOf(actionsPage, 0))
	f, _ := actionFixture(t, c)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/sources"+actionPath("t1", "close", "run"), bytes.NewReader([]byte(`{"values":`)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST run with a body that is not JSON = %d, want 400", resp.StatusCode)
	}
}

func TestModuleActionRoutesCheckInTheDocumentedOrderAndAskThePluginOnlyAfterEveryCheckPasses(t *testing.T) {
	type setup func(rn *runner)
	none := func(*runner) {}
	inError := func(rn *runner) { rn.mu.Lock(); rn.pageErr = "boom"; rn.mu.Unlock() }
	for _, tc := range []struct {
		name      string
		caps      []string
		noPage    bool
		ready     bool
		setup     setup
		path      string // under /api/sources
		verb      string
		wantForm  int
		wantRun   int
		pageState string
	}{
		{"an unknown source", []string{"page"}, false, true, none, "/nope/rows/t1/actions/close/%s", "", 404, 404, ""},
		{"a source with no page", []string{"refresh"}, true, true, none, "/m/rows/t1/actions/close/%s", "", 404, 404, ""},
		{"a row not on the page", []string{"page"}, false, true, none, "/m/rows/nope/actions/close/%s", "", 404, 404, ""},
		{"a module action nobody declared", []string{"page"}, false, true, none, "/m/rows/t1/actions/nope/%s", "", 404, 404, ""},
		{"a module action of another row", []string{"page"}, false, true, none, "/m/rows/t2/actions/close/%s", "", 404, 404, ""},
		{"a step's action before the row was opened", []string{"page"}, false, true, none, "/m/rows/t1/actions/retry/%s", "", 404, 404, ""},
		{"a click action on /form", []string{"page"}, false, true, none, "/m/rows/t1/actions/close/%s", "", 404, 200, ""},
		{"a yesno action on /form", []string{"page"}, false, true, none, "/m/rows/t1/actions/ask/%s", "", 404, 200, ""},
		{"a module action with a url", []string{"page"}, false, true, none, "/m/rows/t1/actions/open/%s", "", 404, 404, ""},
		{"an unknown module action while the page is in error", []string{"page"}, false, true, inError, "/m/rows/t1/actions/nope/%s", "", 404, 404, ""},
		{"a source that is not running", []string{"page"}, false, false, none, "/m/rows/t1/actions/pass/%s", "", 409, 409, ""},
		{"a page in the error state", []string{"page"}, false, true, inError, "/m/rows/t1/actions/pass/%s", "", 409, 409, ""},
	} {
		for _, verb := range []string{"form", "run"} {
			want := tc.wantForm
			if verb == "run" {
				want = tc.wantRun
			}
			c := newActionConn(pageOf(actionsPage, 0))
			c.form = json.RawMessage(`{"fields":[]}`)
			f, rn := pageFixture(t, tc.caps, tc.noPage, tc.ready, c)
			rn.pullPage(t.Context(), c)
			tc.setup(rn)
			path := strings.Replace(tc.path, "%s", verb, 1)
			var body any
			if verb == "run" {
				body = map[string]any{"values": map[string]any{}}
			}
			code, got := f.do(http.MethodPost, path, body)
			if code != want {
				t.Errorf("%s, %s: %d %.120s, want %d", tc.name, verb, code, got, want)
			}
			if want != 200 {
				if n := len(c.actionCalls()); n != 0 {
					t.Errorf("%s, %s: the plugin got %d module action calls, want none", tc.name, verb, n)
				}
			}
		}
	}
}

func TestAnAuthRequiredSourceStillRunsModuleActions(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.form = json.RawMessage(`{"fields":[]}`)
	f, rn := actionFixture(t, c)
	rn.mu.Lock()
	rn.state = StateAuthRequired
	rn.mu.Unlock()
	if v, _ := rn.Page(); v.State != PageAuthRequired {
		t.Fatalf("page state = %q, want auth_required for this test", v.State)
	}
	if code, body := f.formOf("t1", "pass"); code != http.StatusOK {
		t.Errorf("POST form while auth is required = %d %s, want 200", code, body)
	}
	if code, body := f.runAction("t1", "pass", map[string]any{}); code != http.StatusOK {
		t.Errorf("POST run while auth is required = %d %s, want 200", code, body)
	}
}

func TestARunBodyIsChecked409BeforeIts400AndASlotIsNotLeakedByARefusedBody(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	f, rn := actionFixture(t, c)
	rn.mu.Lock()
	rn.pageErr = "boom"
	rn.mu.Unlock()
	if code, body := f.runAction("t1", "close", "not an object"); code != http.StatusConflict {
		t.Errorf("POST run in error with a bad body = %d %s, want 409 before 400", code, body)
	}
	rn.mu.Lock()
	rn.pageErr = ""
	rn.mu.Unlock()
	if code, body := f.runAction("t1", "close", "not an object"); code != http.StatusBadRequest {
		t.Fatalf("POST run with a bad body = %d %s, want 400", code, body)
	}
	if code, body := f.runAction("t1", "close", map[string]any{}); code != http.StatusOK {
		t.Errorf("POST run after a refused body = %d %s, want 200: a 400 holds no slot", code, body)
	}
}

func TestTheModuleActionsOfARowsStepsAreKnownOnlyAfterItsRowGet(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.rows["t1"] = json.RawMessage(t1Detail)
	f, _ := actionFixture(t, c)
	if code, _ := f.runAction("t1", "retry", map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("POST run of a step's action before row.get = %d, want 404", code)
	}
	if code, body := f.do(http.MethodGet, "/m/rows/t1", nil); code != http.StatusOK {
		t.Fatalf("GET row = %d %s, want 200", code, body)
	}
	if code, body := f.runAction("t1", "retry", map[string]any{}); code != http.StatusOK {
		t.Errorf("POST run of a step's action after row.get = %d %s, want 200", code, body)
	}
	if code, _ := f.runAction("t2", "retry", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("POST run of row t1's step action on row t2 = %d, want 404", code)
	}
}

func TestTheFirstModuleActionWithAnIdWinsInRowActionListThenStepOrder(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.rows["t1"] = json.RawMessage(t1Detail) // a step repeats "close" as a form; the row's click came first
	c.form = json.RawMessage(`{"fields":[]}`)
	f, _ := actionFixture(t, c)
	f.do(http.MethodGet, "/m/rows/t1", nil)
	if code, body := f.formOf("t1", "close"); code != http.StatusNotFound {
		t.Errorf("POST form of %q = %d %s, want 404: the row's click action won, the step's form copy is ignored", "close", code, body)
	}
	if code, body := f.runAction("t1", "close", map[string]any{}); code != http.StatusOK {
		t.Errorf("POST run of %q = %d %s, want 200", "close", code, body)
	}
	// "pass" is the row's main action and a later step repeats it as a click with a url.
	c.rows["t1"] = json.RawMessage(`{"sections":[{"kind":"steps","title":"S","steps":[{"id":"s1","title":"S","actions":[{"id":"pass","label":"P","confirm":"click","url":"https://acme.test/"}]}]}]}`)
	f.do(http.MethodGet, "/m/rows/t1", nil)
	if code, body := f.formOf("t1", "pass"); code != http.StatusOK {
		t.Errorf("POST form of the row's own %q = %d %s, want 200: it is the first with that id", "pass", code, body)
	}
}

func TestARowGetThatIsRefusedLeavesTheRememberedModuleActionsAsTheyWere(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.rows["t1"] = json.RawMessage(t1Detail)
	f, _ := actionFixture(t, c)
	f.do(http.MethodGet, "/m/rows/t1", nil)
	c.rows["t1"] = json.RawMessage(`{"sections":[{"kind":"steps","title":"S","steps":[{"id":"d","title":"A"},{"id":"d","title":"B"}]}]}`)
	if code, _ := f.do(http.MethodGet, "/m/rows/t1", nil); code != http.StatusBadGateway {
		t.Fatalf("GET row with a duplicate step id = %d, want 502 for this test", code)
	}
	if code, body := f.runAction("t1", "retry", map[string]any{}); code != http.StatusOK {
		t.Errorf("POST run of a step's action after a refused row.get = %d %s, want 200: the last good answer stands", code, body)
	}
	c.rows["t1"] = json.RawMessage(`{"sections":[]}`)
	f.do(http.MethodGet, "/m/rows/t1", nil)
	if code, _ := f.runAction("t1", "retry", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("POST run of a step's action a newer row.get no longer lists = %d, want 404", code)
	}
}

func TestARowIsForgottenWhenItLeavesThePageAndItsStepsNeedANewRowGet(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.rows["t1"] = json.RawMessage(t1Detail)
	f, rn := actionFixture(t, c)
	f.do(http.MethodGet, "/m/rows/t1", nil)

	c.pageConn.page = json.RawMessage(pageOf(actionsPage, 1)) // the same rows again
	rn.pullPage(t.Context(), c)
	if code, body := f.runAction("t1", "retry", map[string]any{}); code != http.StatusOK {
		t.Errorf("POST run after a page.get that kept the row = %d %s, want 200", code, body)
	}

	c.pageConn.page = json.RawMessage(pageOf(`[{"id":"g","title":"G","rows":[{"id":"t2","title":"Add search"}]}]`, 0))
	rn.pullPage(t.Context(), c)
	if code, _ := f.runAction("t1", "close", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("POST run on a row that left the page = %d, want 404", code)
	}

	c.pageConn.page = json.RawMessage(pageOf(actionsPage, 0)) // it comes back
	rn.pullPage(t.Context(), c)
	if code, _ := f.runAction("t1", "retry", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("POST run of a step's action after its row left and came back = %d, want 404 until row.get is asked again", code)
	}
	f.do(http.MethodGet, "/m/rows/t1", nil)
	if code, body := f.runAction("t1", "retry", map[string]any{}); code != http.StatusOK {
		t.Errorf("POST run after row.get again = %d %s, want 200", code, body)
	}
}

type actionRunResult struct {
	code int
	body string
}

// startRun posts a run that the held conn blocks, and returns once the plugin has it.
func startRun(t *testing.T, f *serviceFixture, c *actionConn, ctx context.Context, row string) chan actionRunResult {
	t.Helper()
	out := make(chan actionRunResult, 1)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.srv.URL+"/api/sources"+actionPath(row, "pass", "run"), strings.NewReader(`{"values":{}}`))
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			out <- actionRunResult{0, err.Error()}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		out <- actionRunResult{resp.StatusCode, string(b)}
	}()
	select {
	case <-c.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("within 5s the plugin never got the run")
	}
	return out
}

func holdActionWindow(d time.Duration, bad func() string) string {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if s := bad(); s != "" {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}

func eventuallyAction(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("within 5s: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestASecondRunOnARowIs409WhileTheFirstIsOpenAndOtherRowsAreFree(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.hold = true
	f, _ := actionFixture(t, c)
	first := startRun(t, f, c, t.Context(), "t1")
	if code, body := f.runAction("t1", "close", map[string]any{}); code != http.StatusConflict {
		t.Errorf("a second run on the row while the first is open = %d %s, want 409", code, body)
	}
	if n := len(c.actionCalls()); n != 1 {
		t.Errorf("the plugin got %d module action calls, want only the first run", n)
	}
	second := startRun(t, f, c, t.Context(), "t2")
	close(c.release)
	for name, ch := range map[string]chan actionRunResult{"the first": first, "the other row's": second} {
		if r := <-ch; r.code != http.StatusOK {
			t.Errorf("%s run = %d %s, want 200", name, r.code, r.body)
		}
	}
	c.mu.Lock()
	c.hold = false
	c.mu.Unlock()
	eventuallyAction(t, "the row's slot to be free after the plugin answered", func() bool {
		code, _ := f.runAction("t1", "close", map[string]any{})
		return code == http.StatusOK
	})
}

func TestARunStaysOpenAfterItsClientGoesAwayAndTheSlotFreesWhenTheRunTimeoutPasses(t *testing.T) {
	c := newActionConn(pageOf(actionsPage, 0))
	c.hold = true
	f, _ := actionFixture(t, c)
	ctx, cancel := context.WithCancel(t.Context())
	first := startRun(t, f, c, ctx, "t1")
	cancel()
	<-first

	if bad := holdActionWindow(300*time.Millisecond, func() string {
		if c.wasCanceled() {
			return "the plugin call's context ended with the client's request"
		}
		if code, _ := f.runAction("t1", "close", map[string]any{}); code != http.StatusConflict {
			return "a second run after the client went away was not refused with 409"
		}
		return ""
	}); bad != "" {
		t.Fatal(bad)
	}
	if n := len(c.actionCalls()); n != 1 {
		t.Errorf("the plugin got %d module action calls, want only the first run", n)
	}
	if calls := c.actionCalls(); calls[0].timeout != runTimeout {
		t.Errorf("run timeout passed to the plugin call = %s, want %s", calls[0].timeout, runTimeout)
	}

	c.mu.Lock()
	c.hold = false
	c.mu.Unlock()
	close(c.fire)
	eventuallyAction(t, "the row's slot to be free once the run timeout passed", func() bool {
		code, _ := f.runAction("t1", "close", map[string]any{})
		return code == http.StatusOK
	})
}
