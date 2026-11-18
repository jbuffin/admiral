package sources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const alphaPage = `[{"id":"a","title":"Alpha","rows":[{"id":"r1","title":"One"}]}]`

func liveOf(t *testing.T, r *runner) map[string]any {
	t.Helper()
	l, ok := r.PageLive()
	if !ok {
		t.Fatal("PageLive() reports no page block for a source that declares a page")
	}
	return jsonOf(t, l)
}

func versionOf(t *testing.T, r *runner) int {
	t.Helper()
	return int(liveOf(t, r)["version"].(float64))
}

func rowsOf(t *testing.T, r *runner) []string {
	t.Helper()
	l := liveOf(t, r)
	raw, has := l["rows"]
	if !has {
		return nil
	}
	var out []string
	for _, id := range raw.([]any) {
		out = append(out, id.(string))
	}
	slices.Sort(out)
	return out
}

// A runner of a plugin that declares a page, up and holding a good page.
func livePageRunner(t *testing.T, c *pageConn) *runner {
	t.Helper()
	r := pageRunner()
	r.caps = []string{"page"}
	r.p, r.ready, r.state = c, true, StateOK
	r.pullPage(t.Context(), c)
	return r
}

func pageGets(c *pageConn) int {
	n := 0
	for _, call := range c.calls {
		if call.method == "page.get" {
			n++
		}
	}
	return n
}

func TestPageChangedMakesAdmiralAskPageGetAgainAndServeTheNewPage(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	if pageGets(c) != 1 {
		t.Fatalf("page.get calls after the first pull = %d, want 1", pageGets(c))
	}
	c.page = json.RawMessage(pageOf(`[{"id":"b","title":"Beta","rows":[]}]`, 5))
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	if pageGets(c) != 2 {
		t.Errorf("page.get calls after page.changed = %d, want 2", pageGets(c))
	}
	if _, titles, waiting := pageTitles(t, r); len(titles) != 1 || titles[0] != "Beta" || waiting != float64(5) {
		t.Errorf("groups %v waiting %v, want Beta and 5 from the new page.get", titles, waiting)
	}
}

func TestPageChangedFromAnOlderProcessIsIgnored(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	before := versionOf(t, r)
	r.note(0, "page.changed", json.RawMessage(`{"rows":["r1"]}`))
	r.processPulls(t.Context(), c)
	if pageGets(c) != 1 || versionOf(t, r) != before {
		t.Errorf("page.get calls %d, version %d -> %d, want no refetch and no version change for an older process", pageGets(c), before, versionOf(t, r))
	}
}

func TestAnOverLimitAnswerToThePageGetThatPageChangedCausedKeepsTheLastGoodPage(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	c.page = json.RawMessage(pageOf(groupsJSON(maxPageGroups+1), 9))
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	if state, titles, waiting := pageTitles(t, r); state != "ready" || len(titles) != 1 || titles[0] != "Alpha" || waiting != float64(1) {
		t.Errorf("after an over-limit refetch: state %q groups %v waiting %v, want the good page, ready", state, titles, waiting)
	}
}

func TestTwoNamedPageChangesBeforeOnePullAreOneRefetchNamingBothRows(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	r.note(1, "page.changed", json.RawMessage(`{"rows":["r1"]}`))
	r.note(1, "page.changed", json.RawMessage(`{"rows":["r2","r1"]}`))
	r.processPulls(t.Context(), c)
	if pageGets(c) != 2 {
		t.Errorf("page.get calls = %d, want the first pull and one for both notifications", pageGets(c))
	}
	if got := rowsOf(t, r); !slices.Equal(got, []string{"r1", "r2"}) {
		t.Errorf("live rows = %v, want the union [r1 r2]", got)
	}
}

func TestAnUnnamedPageChangeAmongNamedOnesMakesItAllRows(t *testing.T) {
	for name, order := range map[string][]string{
		"named then unnamed": {`{"rows":["r1"]}`, ``},
		"unnamed then named": {``, `{"rows":["r1"]}`},
		"an empty object":    {`{"rows":["r1"]}`, `{}`},
	} {
		c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
		r := livePageRunner(t, c)
		for _, p := range order {
			var params json.RawMessage
			if p != "" {
				params = json.RawMessage(p)
			}
			r.note(1, "page.changed", params)
		}
		r.processPulls(t.Context(), c)
		if got := rowsOf(t, r); got != nil {
			t.Errorf("%s: live rows = %v, want none (absent means all rows)", name, got)
		}
	}
}

func TestRowIDsInPageChangedAreUntrusted(t *testing.T) {
	long := strings.Repeat("i", maxIDRunes+1)
	for name, params := range map[string]string{
		"a string, not a list":   `{"rows":"r1"}`,
		"an object, not a list":  `{"rows":{"r1":true}}`,
		"numbers, not ids":       `{"rows":[1,2]}`,
		"params that are a list": `["r1"]`,
	} {
		c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
		r := livePageRunner(t, c)
		r.note(1, "page.changed", json.RawMessage(params))
		r.processPulls(t.Context(), c)
		if pageGets(c) != 2 {
			t.Errorf("%s: page.get calls = %d, want the page still refetched", name, pageGets(c))
		}
		if got := rowsOf(t, r); got != nil {
			t.Errorf("%s: live rows = %v, want the malformed list dropped, so none named", name, got)
		}
	}

	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	r.note(1, "page.changed", json.RawMessage(`{"rows":["r1","bad\nid","","`+long+`","r2"]}`))
	r.processPulls(t.Context(), c)
	if got := rowsOf(t, r); !slices.Equal(got, []string{"r1", "r2"}) {
		t.Errorf("live rows = %v, want the ids that pass the id rules [r1 r2]", got)
	}
}

func TestNamedRowsAreCappedAtThePageRowLimit(t *testing.T) {
	ids := make([]string, maxPageRows+500)
	for i := range ids {
		ids[i] = "r" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
	}
	raw, _ := json.Marshal(map[string]any{"rows": ids})
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	r.note(1, "page.changed", raw)
	r.processPulls(t.Context(), c)
	if got := rowsOf(t, r); len(got) > maxPageRows {
		t.Errorf("live rows = %d ids, want at most the page's row limit %d", len(got), maxPageRows)
	}
	if pageGets(c) != 2 {
		t.Errorf("page.get calls = %d, want the page refetched", pageGets(c))
	}
}

func TestARowsListPastThePageRowLimitIsAllRowsEvenWhenItsItemsAreDuplicates(t *testing.T) {
	ids := make([]string, maxPageRows+1)
	for i := range ids {
		ids[i] = "r1"
	}
	raw, _ := json.Marshal(map[string]any{"rows": ids})
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	r.note(1, "page.changed", raw)
	r.processPulls(t.Context(), c)
	if got := rowsOf(t, r); got != nil {
		t.Errorf("live rows = %v, want none: a list past the row limit means all rows", got)
	}
	if pageGets(c) != 2 {
		t.Errorf("page.get calls = %d, want the page refetched", pageGets(c))
	}
}

func TestNamedRowsThatCoalescePastThePageRowLimitMakeItAllRows(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	for _, from := range []int{0, maxPageRows} {
		ids := make([]string, maxPageRows)
		for i := range ids {
			ids[i] = "r" + strconv.Itoa(from+i)
		}
		raw, _ := json.Marshal(map[string]any{"rows": ids})
		r.note(1, "page.changed", raw)
	}
	r.processPulls(t.Context(), c)
	if got := rowsOf(t, r); got != nil {
		t.Errorf("live rows = %d ids, want none: the union passed the row limit, so all rows", len(got))
	}
}

func TestAPageChangedBeforeInitializeFromAPluginWithoutAPageNeverReachesPageGet(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := pageRunner()
	r.caps = []string{"refresh"}
	r.p, r.ready, r.state = c, true, StateOK
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	if pageGets(c) != 0 {
		t.Errorf("page.get calls = %d, want none for a plugin that declares no page", pageGets(c))
	}
}

func TestVersionGoesUpByOneOnEachKindOfPageChange(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	v := versionOf(t, r)
	step := func(what string, act func()) {
		t.Helper()
		act()
		if got := versionOf(t, r); got != v+1 {
			t.Errorf("%s: version %d -> %d, want it up by exactly 1", what, v, got)
		}
		v = versionOf(t, r)
	}
	step("a new good page", func() {
		c.page = json.RawMessage(pageOf(`[{"id":"b","title":"Beta","rows":[]}]`, 2))
		r.pullPage(t.Context(), c)
	})
	step("page.changed with a good answer", func() {
		c.page = json.RawMessage(pageOf(alphaPage, 3))
		r.note(1, "page.changed", nil)
		r.processPulls(t.Context(), c)
	})
	step("page.changed whose page.get is refused", func() {
		c.page = json.RawMessage(`{"groups":"nope"}`)
		r.note(1, "page.changed", nil)
		r.processPulls(t.Context(), c)
	})
	step("page.changed whose page.get fails with an rpc error", func() {
		c.page, c.pageErr = nil, &rpcError{Code: -32000, Message: "upstream down"}
		r.note(1, "page.changed", nil)
		r.processPulls(t.Context(), c)
	})
	c.pageErr = nil
	step("a state change to auth_required", func() {
		r.note(1, "auth.required", json.RawMessage(`{"message":"Sign in"}`))
	})
	step("auth.ok", func() { r.note(1, "auth.ok", nil) })
}

func TestRowsAreNamedOnlyWhenTheLastBumpCameFromNamedPageChanges(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}
	r := livePageRunner(t, c)
	named := func() {
		r.note(1, "page.changed", json.RawMessage(`{"rows":["r1"]}`))
		r.processPulls(t.Context(), c)
		if got := rowsOf(t, r); !slices.Equal(got, []string{"r1"}) {
			t.Fatalf("live rows after a named page.changed = %v, want [r1]", got)
		}
	}
	named()
	c.page = json.RawMessage(pageOf(`[{"id":"b","title":"Beta","rows":[]}]`, 2))
	r.pullPage(t.Context(), c) // what a refresh or a restart does
	if got := rowsOf(t, r); got != nil {
		t.Errorf("live rows after a new page that no page.changed named = %v, want none", got)
	}
	named()
	r.note(1, "auth.required", json.RawMessage(`{"message":"Sign in"}`))
	if got := rowsOf(t, r); got != nil {
		t.Errorf("live rows after a state change = %v, want none", got)
	}
}

func TestTheLiveBlockIsOnlyForASourceThatDeclaresAPage(t *testing.T) {
	r := pageRunner()
	if _, ok := r.PageLive(); ok {
		t.Error("PageLive() before initialize = a block, want none: the source has not said it has a page")
	}
	r.caps = []string{"refresh"}
	r.noPage = true
	if _, ok := r.PageLive(); ok {
		t.Error("PageLive() for a source without page in its capabilities = a block, want none")
	}
	r.caps, r.noPage = []string{"refresh", "page"}, false
	l := liveOf(t, r)
	if l["state"] != "starting" || l["waiting"] != float64(0) {
		t.Errorf("live block before the first page.get = %s, want state starting, waiting 0", dump(l))
	}
	if _, has := l["rows"]; has {
		t.Errorf("live block = %s, want no rows key when none were named", dump(l))
	}
}

func TestTheLiveBlockShowsTheStateAndTheWaitingCountOfThePage(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 4))}
	r := livePageRunner(t, c)
	if l := liveOf(t, r); l["state"] != "ready" || l["waiting"] != float64(4) {
		t.Errorf("live block = %s, want ready and waiting 4", dump(l))
	}
	r.note(1, "auth.required", json.RawMessage(`{"message":"Sign in"}`))
	if l := liveOf(t, r); l["state"] != "auth_required" || l["waiting"] != float64(4) {
		t.Errorf("live block under auth.required = %s, want auth_required and the last waiting count 4", dump(l))
	}
}

func pageAnswer(t *testing.T, r *runner) map[string]any {
	t.Helper()
	return jsonOf(t, mustPage(t, r))
}

func TestAuthRequiredShowsOnThePageWithItsFieldsAndKeepsTheLastGoodGroups(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 2))}
	r := livePageRunner(t, c)
	r.note(1, "auth.required", json.RawMessage(`{"message":"Sign in to Acme","url":"https://acme.example/login","code":"AB-12"}`))
	got := pageAnswer(t, r)
	if got["state"] != "auth_required" {
		t.Fatalf("page = %s, want state auth_required", dump(got))
	}
	if len(list(t, got, "groups")) != 1 || got["waiting"] != float64(2) {
		t.Errorf("page = %s, want the last good groups and waiting kept", dump(got))
	}
	auth, _ := got["auth"].(map[string]any)
	if auth["message"] != "Sign in to Acme" || auth["url"] != "https://acme.example/login" || auth["code"] != "AB-12" {
		t.Errorf("auth = %s, want message, url and code from auth.required", dump(got["auth"]))
	}
	r.note(1, "auth.ok", nil)
	got = pageAnswer(t, r)
	if got["state"] != "ready" || got["auth"] != nil {
		t.Errorf("page after auth.ok = %s, want ready and no auth", dump(got))
	}
}

func TestAuthRequiredBeforeAnyGoodPageHasNoGroups(t *testing.T) {
	r := pageRunner()
	r.caps = []string{"page"}
	r.ready, r.state = true, StateOK
	r.note(1, "auth.required", json.RawMessage(`{"message":"Sign in"}`))
	got := pageAnswer(t, r)
	if groups, ok := got["groups"].([]any); got["state"] != "auth_required" || !ok || len(groups) != 0 {
		t.Errorf("page = %s, want state auth_required and groups []", dump(got))
	}
}

func TestACrashShowsAsAnErrorStateKeepingTheLastGoodGroupsAndCarryingTheMessage(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 2))}
	r := livePageRunner(t, c)
	r.fail(errors.New("the plugin exited with status 1"))
	got := pageAnswer(t, r)
	if got["state"] != "error" || got["error"] != "the plugin exited with status 1" {
		t.Errorf("page = %s, want state error with the message", dump(got))
	}
	if len(list(t, got, "groups")) != 1 || got["waiting"] != float64(2) {
		t.Errorf("page = %s, want the last good groups kept", dump(got))
	}
	if l := liveOf(t, r); l["state"] != "error" {
		t.Errorf("live block = %s, want state error", dump(l))
	}

	fresh := pageRunner()
	fresh.caps = []string{"page"}
	fresh.fail(errors.New("boom"))
	got = pageAnswer(t, fresh)
	if groups, ok := got["groups"].([]any); got["state"] != "error" || !ok || len(groups) != 0 {
		t.Errorf("page of a source that never had a good page = %s, want state error and groups []", dump(got))
	}
}

func TestARestartKeepsThePageReadyWithItsLastGoodGroupsUntilTheSourceSaysOtherwise(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 2))}
	r := livePageRunner(t, c)
	r.restart()
	got := pageAnswer(t, r)
	if got["state"] != "ready" || len(list(t, got, "groups")) != 1 {
		t.Errorf("page while the source restarts = %s, want ready with the last good groups", dump(got))
	}
}

func TestAnRPCErrorFromPageGetShowsAnErrorOnThePageUntilTheNextGoodPage(t *testing.T) {
	c := &pageConn{page: json.RawMessage(pageOf(alphaPage, 2))}
	r := livePageRunner(t, c)
	c.page, c.pageErr = nil, &rpcError{Code: -32000, Message: "upstream down"}
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	got := pageAnswer(t, r)
	if got["state"] != "error" || got["error"] != "upstream down" {
		t.Fatalf("page = %s, want state error with the plugin's message", dump(got))
	}
	if len(list(t, got, "groups")) != 1 || got["waiting"] != float64(2) {
		t.Errorf("page = %s, want the last good groups kept", dump(got))
	}
	if r.snapshot().state != StateOK {
		t.Errorf("source state = %q, want it still ok: only the page has the error", r.snapshot().state)
	}

	c.page, c.pageErr = json.RawMessage(pageOf(`[{"id":"b","title":"Beta","rows":[]}]`, 3)), nil
	r.note(1, "page.changed", nil)
	r.processPulls(t.Context(), c)
	got = pageAnswer(t, r)
	if got["state"] != "ready" || got["error"] != nil && got["error"] != "" {
		t.Errorf("page after a good answer = %s, want ready with the error cleared", dump(got))
	}
}

func TestAProtocolOrLimitRefusalOrATimeoutOnlyLogsAndTheStateStaysReady(t *testing.T) {
	for name, bad := range map[string]*pageConn{
		"over a limit": {page: json.RawMessage(pageOf(groupsJSON(maxPageGroups+1), 9))},
		"malformed":    {page: json.RawMessage(`{"groups":"nope"}`)},
		"a timeout":    {pageErr: errTimeout},
	} {
		r := livePageRunner(t, &pageConn{page: json.RawMessage(pageOf(alphaPage, 2))})
		r.pullPage(t.Context(), bad)
		got := pageAnswer(t, r)
		if got["state"] != "ready" || got["error"] != nil && got["error"] != "" {
			t.Errorf("%s: page = %s, want ready with no error", name, dump(got))
		}
	}
}

// pageRefreshConn is a pageConn that also answers page.refresh, as the test says.
type pageRefreshConn struct {
	*pageConn
	mu         sync.Mutex
	refreshErr error
	refreshed  []pageCall
}

func (c *pageRefreshConn) call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if method == "page.refresh" {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.refreshed = append(c.refreshed, pageCall{method, dump(params), timeout})
		if c.refreshErr != nil {
			return nil, c.refreshErr
		}
		return json.RawMessage(`{}`), nil
	}
	return c.pageConn.call(ctx, method, params, timeout)
}

// A fixture whose source "m" is a runner nobody runs, holding conn.
func pageFixture(t *testing.T, caps []string, noPage, ready bool, c conn) (*serviceFixture, *runner) {
	t.Helper()
	f := newServiceFixture(t, `{"version":1}`)
	rn := &runner{key: "m", sid: 1, notify: func() {}, wake: make(chan struct{}, 1), restartCh: make(chan struct{}, 1), caps: caps, noPage: noPage}
	rn.mu.Lock()
	rn.p, rn.ready, rn.state = c, ready, StateOK
	if !ready {
		rn.state = StateStarting
	}
	rn.mu.Unlock()
	f.Svc.Sup.mu.Lock()
	f.Svc.Sup.runners = map[string]*runner{"m": rn}
	f.Svc.Sup.order = append(f.Svc.Sup.order, "m")
	f.Svc.Sup.mu.Unlock()
	return f, rn
}

func TestPageRefreshCallsThePluginWithinTheCallLimitAndQueuesAPageGet(t *testing.T) {
	c := &pageRefreshConn{pageConn: &pageConn{page: json.RawMessage(pageOf(alphaPage, 1))}}
	f, rn := pageFixture(t, []string{"page"}, false, true, c)
	code, body := f.do(http.MethodPost, "/m/page/refresh", nil)
	if code != http.StatusOK {
		t.Fatalf("POST page/refresh = %d %s, want 200", code, body)
	}
	if len(c.refreshed) != 1 || c.refreshed[0].params != "{}" || c.refreshed[0].timeout != callTimeout {
		t.Errorf("page.refresh calls = %+v, want one with {} and the call limit %s", c.refreshed, callTimeout)
	}
	rn.processPulls(t.Context(), c)
	if pageGets(c.pageConn) != 1 {
		t.Errorf("page.get calls after the route and one pull = %d, want 1: the route queues a pull", pageGets(c.pageConn))
	}
}

func TestPageRefreshOfASourceWithoutAPageIs404AndNeverReachesThePlugin(t *testing.T) {
	c := &pageRefreshConn{pageConn: &pageConn{}}
	f, _ := pageFixture(t, []string{"refresh"}, true, true, c)
	code, body := f.do(http.MethodPost, "/m/page/refresh", nil)
	if code != http.StatusNotFound || !strings.Contains(body, "The source has no page") {
		t.Errorf("POST page/refresh = %d %s, want 404 \"The source has no page\"", code, body)
	}
	if code, _ := f.do(http.MethodPost, "/nope/page/refresh", nil); code != http.StatusNotFound {
		t.Errorf("POST page/refresh of a source that does not exist = %d, want 404", code)
	}
	if len(c.refreshed) != 0 {
		t.Errorf("page.refresh calls = %d, want none", len(c.refreshed))
	}
}

func TestPageRefreshOfASourceThatIsNotRunningIs409(t *testing.T) {
	c := &pageRefreshConn{pageConn: &pageConn{}}
	f, _ := pageFixture(t, []string{"page"}, false, false, c)
	if code, body := f.do(http.MethodPost, "/m/page/refresh", nil); code != http.StatusConflict {
		t.Errorf("POST page/refresh of a source that is starting = %d %s, want 409", code, body)
	}
	if len(c.refreshed) != 0 {
		t.Errorf("page.refresh calls = %d, want none while the source is not running", len(c.refreshed))
	}
}

func TestPageRefreshMapsAFailedCallTheWayOrdersRefreshDoes(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"the plugin refuses": {&rpcError{Code: -32000, Message: "not today"}, http.StatusUnprocessableEntity},
		"a timeout":          {errTimeout, http.StatusGatewayTimeout},
		"any other failure":  {errors.New("pipe closed"), http.StatusBadGateway},
	} {
		c := &pageRefreshConn{pageConn: &pageConn{}, refreshErr: tc.err}
		f, _ := pageFixture(t, []string{"page"}, false, true, c)
		if code, body := f.do(http.MethodPost, "/m/page/refresh", nil); code != tc.want {
			t.Errorf("%s: POST page/refresh = %d %s, want %d", name, code, body, tc.want)
		}
	}
}

func TestTheLiveBlockRidesTheSourcesViewOnlyForASourceWithAPage(t *testing.T) {
	c := &pageRefreshConn{pageConn: &pageConn{page: json.RawMessage(pageOf(alphaPage, 3))}}
	f, rn := pageFixture(t, []string{"page"}, false, true, c)
	rn.pullPage(t.Context(), c)
	views := f.Svc.View()
	if len(views) != 1 {
		t.Fatalf("View() = %s, want the one source", dump(views))
	}
	got := jsonOf(t, views[0])
	live, _ := got["page"].(map[string]any)
	if live["state"] != "ready" || live["waiting"] != float64(3) || live["version"] == nil {
		t.Errorf("the source's page block = %s, want state ready, waiting 3 and a version", dump(got["page"]))
	}

	rn.mu.Lock()
	rn.caps, rn.noPage = []string{"refresh"}, true
	rn.mu.Unlock()
	if _, has := jsonOf(t, f.Svc.View()[0])["page"]; has {
		t.Error("a source without a page has a page key in its view, want it unchanged")
	}
}
