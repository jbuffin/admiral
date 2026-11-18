package sources

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const noticeActionsPage = `{"groups":[{"id":"g","title":"G","rows":[
	{"id":"pick","title":"A row sharing the notice id","action":{"id":"project","label":"Row pick","confirm":"form"}},
	{"id":"notice:x","title":"A row whose id looks like a prefix","action":{"id":"project","label":"Row x","confirm":"form"}}]}],"waiting":0,
	"notices":[
	 {"id":"pick","text":"Pick your project","action":{"id":"project","label":"Pick your project","confirm":"form"}},
	 {"id":"x","text":"X","action":{"id":"project","label":"Pick","confirm":"form"}},
	 {"id":"sync","text":"Sync","action":{"id":"sync","label":"Sync now","confirm":"click"}},
	 {"id":"reset","text":"Reset","action":{"id":"reset","label":"Reset","confirm":"yesno","summary":"Sure?"}},
	 {"id":"docs","text":"Docs","action":{"id":"open","label":"Open","confirm":"click","url":"https://acme.test/docs"}},
	 {"id":"plain","text":"No action here"}]}`

func noticePath(notice, action, verb string) string {
	return "/m/notices/" + notice + "/actions/" + action + "/" + verb
}

func (f *serviceFixture) runNoticeAction(notice, action string, values any) (int, string) {
	f.t.Helper()
	return f.do(http.MethodPost, noticePath(notice, action, "run"), map[string]any{"values": values})
}

func (f *serviceFixture) noticeFormOf(notice, action string) (int, string) {
	f.t.Helper()
	return f.do(http.MethodPost, noticePath(notice, action, "form"), nil)
}

func TestANoticeFormFetchAddressesTheNoticeAndServesTheValidatedForm(t *testing.T) {
	c := newActionConn(noticeActionsPage)
	c.form = json.RawMessage(`{"title":"Pick your project","fields":[
		{"id":"project","label":"Project","type":"choice","options":[{"id":"a","label":"Apollo"},{"id":"b","label":"Borealis"}]}]}`)
	f, _ := actionFixture(t, c)
	code, body := f.noticeFormOf("pick", "project")
	if code != http.StatusOK {
		t.Fatalf("POST notice form = %d %s, want 200", code, body)
	}
	sameActionJSON(t, "form", body, `{"title":"Pick your project","fields":[
		{"id":"project","label":"Project","type":"choice","options":[{"id":"a","label":"Apollo"},{"id":"b","label":"Borealis"}]}]}`)
	calls := c.actionCalls()
	if len(calls) != 1 || calls[0].method != "action.form" || calls[0].timeout != callTimeout {
		t.Fatalf("plugin calls = %+v, want one action.form within the call limit %s", calls, callTimeout)
	}
	sameActionJSON(t, "action.form params", calls[0].params, `{"notice":"pick","action":"project"}`)
}

func TestANoticeRunAddressesTheNoticeAndPassesItsValuesOn(t *testing.T) {
	c := newActionConn(noticeActionsPage)
	c.run = json.RawMessage(`{"message":"Project set","url":"https://acme.test/p"}`)
	f, _ := actionFixture(t, c)
	code, body := f.runNoticeAction("pick", "project", map[string]any{"project": "b"})
	if code != http.StatusOK {
		t.Fatalf("POST notice run = %d %s, want 200", code, body)
	}
	sameActionJSON(t, "run answer", body, `{"message":"Project set","url":"https://acme.test/p"}`)
	calls := c.actionCalls()
	if len(calls) != 1 || calls[0].method != "action.run" || calls[0].timeout != runTimeout {
		t.Fatalf("plugin calls = %+v, want one action.run with the run limit %s", calls, runTimeout)
	}
	sameActionJSON(t, "action.run params", calls[0].params, `{"notice":"pick","action":"project","values":{"project":"b"}}`)
}

func TestAClickAndAYesnoActionOnANoticeRunWithNoValuesAndNeverNeedAForm(t *testing.T) {
	for _, id := range []string{"sync", "reset"} {
		c := newActionConn(noticeActionsPage)
		f, _ := actionFixture(t, c)
		if code, body := f.runNoticeAction(id, id, map[string]any{}); code != http.StatusOK {
			t.Errorf("%s: POST run = %d %s, want 200", id, code, body)
		}
		if code, body := f.noticeFormOf(id, id); code != http.StatusNotFound {
			t.Errorf("%s: POST form = %d %s, want 404: the action asks for no form", id, code, body)
		}
		calls := c.actionCalls()
		if len(calls) != 1 || calls[0].method != "action.run" {
			t.Fatalf("%s: plugin calls = %+v, want the one run", id, calls)
		}
		sameActionJSON(t, id+" params", calls[0].params, `{"notice":"`+id+`","action":"`+id+`","values":{}}`)
	}
}

func TestANoticeCallThePageDoesNotBackIsRefusedWithoutAskingThePlugin(t *testing.T) {
	inError := func(rn *runner) { rn.mu.Lock(); rn.pageErr = "boom"; rn.mu.Unlock() }
	none := func(*runner) {}
	for _, tc := range []struct {
		name   string
		ready  bool
		setup  func(*runner)
		path   string // under /api/sources, %s is the verb
		status int
		want   string // a word the one-line error carries, "" for none
	}{
		{"an unknown notice", true, none, "/m/notices/nope/actions/project/%s", 404, "notice"},
		{"a row id used as a notice id", true, none, "/m/notices/t1/actions/project/%s", 404, "notice"},
		{"a notice without an action", true, none, "/m/notices/plain/actions/project/%s", 404, "notice"},
		{"an action the notice does not have", true, none, "/m/notices/pick/actions/nope/%s", 404, "notice"},
		{"another notice's action", true, none, "/m/notices/sync/actions/project/%s", 404, "notice"},
		{"a url action", true, none, "/m/notices/docs/actions/open/%s", 404, ""},
		{"a source that is not running", false, none, "/m/notices/pick/actions/project/%s", 409, ""},
		{"a page in the error state", true, inError, "/m/notices/pick/actions/project/%s", 409, ""},
	} {
		for _, verb := range []string{"form", "run"} {
			c := newActionConn(noticeActionsPage)
			c.form = json.RawMessage(`{"fields":[]}`)
			f, rn := pageFixture(t, []string{"page"}, false, tc.ready, c)
			rn.pullPage(t.Context(), c)
			tc.setup(rn)
			var body any
			if verb == "run" {
				body = map[string]any{"values": map[string]any{}}
			}
			code, got := f.do(http.MethodPost, strings.Replace(tc.path, "%s", verb, 1), body)
			if code != tc.status {
				t.Errorf("%s, %s: %d %.120s, want %d", tc.name, verb, code, got, tc.status)
				continue
			}
			// A bare mux 404 is not an answer from the route, so every refusal must carry its one-line error.
			if msg := actionErrorLine(t, got); tc.want != "" && !strings.Contains(strings.ToLower(msg), tc.want) {
				t.Errorf("%s, %s: error %q, want it to name the notice", tc.name, verb, msg)
			}
			if n := len(c.actionCalls()); n != 0 {
				t.Errorf("%s, %s: the plugin got %d calls, want none", tc.name, verb, n)
			}
		}
	}
}

func TestANoticeIdIsNotARowIdOnTheRowRoutes(t *testing.T) {
	c := newActionConn(noticeActionsPage)
	f, _ := actionFixture(t, c)
	if code, body := f.runAction("sync", "sync", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("row route with a notice id = %d %s, want 404", code, body)
	}
	if n := len(c.actionCalls()); n != 0 {
		t.Errorf("the plugin got %d calls, want none", n)
	}
}

func TestANoticeServedWhileTheModuleIsStillLoadingCanRunItsAction(t *testing.T) {
	c := newActionConn(`{"loading":true,"notices":[{"id":"pick","text":"Pick your project","action":{"id":"project","label":"Pick","confirm":"form"}}]}`)
	c.form = json.RawMessage(`{"fields":[{"id":"project","label":"Project","type":"choice","options":[{"id":"a","label":"Apollo"}]}]}`)
	f, _ := actionFixture(t, c)
	if code, body := f.noticeFormOf("pick", "project"); code != http.StatusOK {
		t.Fatalf("POST notice form while loading = %d %s, want 200", code, body)
	}
	if code, body := f.runNoticeAction("pick", "project", map[string]any{"project": "a"}); code != http.StatusOK {
		t.Errorf("POST notice run while loading = %d %s, want 200", code, body)
	}
}

// startRunAt posts a run at path that the held conn blocks, and returns once the plugin has it.
func startRunAt(t *testing.T, f *serviceFixture, c *actionConn, ctx context.Context, path string) chan actionRunResult {
	t.Helper()
	out := make(chan actionRunResult, 1)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.srv.URL+"/api/sources"+path, strings.NewReader(`{"values":{}}`))
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

func TestASecondRunOnANoticeIs409WhileTheFirstIsOpenAndANoticeAndARowWithOneIdDoNotBlockEachOther(t *testing.T) {
	c := newActionConn(noticeActionsPage)
	c.hold = true
	f, _ := actionFixture(t, c)
	first := startRunAt(t, f, c, t.Context(), noticePath("pick", "project", "run"))
	code, body := f.runNoticeAction("pick", "project", map[string]any{})
	if code != http.StatusConflict {
		t.Errorf("a second run on the notice while the first is open = %d %s, want 409", code, body)
	} else if msg := strings.ToLower(actionErrorLine(t, body)); !strings.Contains(msg, "notice") || strings.Contains(msg, "row") {
		t.Errorf("409 error = %q, want it worded for the notice", msg)
	}
	if n := len(c.actionCalls()); n != 1 {
		t.Errorf("the plugin got %d calls, want only the first run", n)
	}
	// Same id, other kind: the row "pick" runs beside the notice "pick".
	rowRun := startRunAt(t, f, c, t.Context(), actionPath("pick", "project", "run"))
	// A row whose id is literally "notice:x" is not notice "x".
	noticeX := startRunAt(t, f, c, t.Context(), noticePath("x", "project", "run"))
	prefixRow := startRunAt(t, f, c, t.Context(), actionPath("notice:x", "project", "run"))
	close(c.release)
	for name, ch := range map[string]chan actionRunResult{"the notice's": first, "the same-id row's": rowRun, "notice x's": noticeX, "the prefix-id row's": prefixRow} {
		if r := <-ch; r.code != http.StatusOK {
			t.Errorf("%s run = %d %s, want 200", name, r.code, r.body)
		}
	}
	c.mu.Lock()
	c.hold = false
	c.mu.Unlock()
	eventuallyAction(t, "the notice's slot to be free after the plugin answered", func() bool {
		code, _ := f.runNoticeAction("pick", "project", map[string]any{})
		return code == http.StatusOK
	})
}

func TestAFormCanBeFetchedWhileARunIsOpenOnTheNotice(t *testing.T) {
	c := newActionConn(noticeActionsPage)
	c.hold = true
	c.form = json.RawMessage(`{"fields":[]}`)
	f, _ := actionFixture(t, c)
	first := startRunAt(t, f, c, t.Context(), noticePath("pick", "project", "run"))
	if code, body := f.noticeFormOf("pick", "project"); code != http.StatusOK {
		t.Errorf("form fetch while a run is open = %d %s, want 200", code, body)
	}
	close(c.release)
	<-first
}
