package follow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/github"
)

const (
	parentEndpoint = "repos/acme/widgets/issues"
	subEndpoint    = "repos/acme/widgets/issues/99/sub_issues"
	newParentURL   = "https://github.com/acme/widgets/issues/99"
)

// restID is a child's REST id: past int32 and unlike its number, so a link that sends the number or Item.ID fails.
func restID(n int) int64 { return 4000000000 + int64(n) }

func issueEndpoint(repo string, n int) string { return fmt.Sprintf("repos/%s/issues/%d", repo, n) }

// child answers the read of an issue acme/widgets#n; parent is its parent_issue_url.
func (g *fakeGH) child(repo string, n int, parent string) {
	p := "null"
	if parent != "" {
		p = `"` + parent + `"`
	}
	g.respondREST("GET", issueEndpoint(repo, n), "", fmt.Sprintf(`{"id":%d,"number":%d,"parent_issue_url":%s}`, restID(n), n, p))
}

// parentReady answers the create, every link and the follow's read of the new parent over the given children.
func (g *fakeGH) parentReady(kids ...int) {
	g.respondREST("POST", parentEndpoint, "", `{"number":99,"html_url":"`+newParentURL+`"}`)
	g.respondREST("POST", subEndpoint, "", `{"id":1}`)
	var under []iss
	for _, k := range kids {
		under = append(under, iss{num: k})
	}
	g.set(iss{num: 99, labels: []string{"wayfinder:map"}, kids: under})
}

func (r *rig) create(body any) (int, string) {
	r.t.Helper()
	code, b := r.do("POST", "/api/campaigns/create", body)
	return code, string(b)
}

func createBody(issues ...string) map[string]any {
	return map[string]any{"repo": "acme/widgets", "title": "Orders: parent groups", "body": "Groups the issues as one campaign.", "issues": issues}
}

// posts are the REST calls that wrote.
func posts(cs []restCall) []restCall {
	var out []restCall
	for _, c := range cs {
		if c.Method != "GET" {
			out = append(out, c)
		}
	}
	return out
}

func TestCreateMakesTheParentOnceLinksEachChildInTheRequestsOrderAndFollowsIt(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.gh.child("acme/widgets", 33, "")
	r.gh.child("acme/widgets", 31, "")
	r.gh.child("acme/other", 7, "")
	r.gh.parentReady(33, 31, 7)

	code, body := r.create(createBody("https://github.com/acme/widgets/issues/33", "acme/widgets#31", "acme/other#7"))
	if code != http.StatusCreated {
		t.Fatalf("POST /api/campaigns/create = %d %s, want 201", code, body)
	}
	var c campaignJSON
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("campaign JSON: %v\n%s", err, body)
	}
	if c.URL != newParentURL || c.Fleet != "work" || c.ID == "" || c.Root == nil || c.Root.Number != 99 {
		t.Errorf("campaign = url %q fleet %q root %+v; want the new parent %s followed on work", c.URL, c.Fleet, c.Root, newParentURL)
	}
	if got := r.get(c.ID); got.URL != newParentURL {
		t.Errorf("the campaign is not stored: GET = %+v", got)
	}

	calls := r.gh.restCalls()
	if len(calls) != 7 {
		t.Fatalf("REST calls = %+v; want 3 reads, 1 create and 3 links", calls)
	}
	reads := map[string]bool{}
	for _, c := range calls[:3] {
		if c.Method != "GET" {
			t.Errorf("call %+v came before the writes but is not a read: every child is read before anything is written", c)
		}
		reads[c.Endpoint] = true
	}
	for _, want := range []string{issueEndpoint("acme/widgets", 33), issueEndpoint("acme/widgets", 31), issueEndpoint("acme/other", 7)} {
		if !reads[want] {
			t.Errorf("no read of %s; reads = %v", want, reads)
		}
	}
	var create struct{ Title, Body string }
	if calls[3].Method != "POST" || calls[3].Endpoint != parentEndpoint || json.Unmarshal([]byte(calls[3].Stdin), &create) != nil ||
		create.Title != "Orders: parent groups" || create.Body != "Groups the issues as one campaign." {
		t.Errorf("fourth call = %+v; want the parent created in acme/widgets with the title and body on stdin", calls[3])
	}
	for i, n := range []int{33, 31, 7} {
		got := calls[4+i]
		want := fmt.Sprintf(`{"sub_issue_id":%d}`, restID(n))
		if got.Method != "POST" || got.Endpoint != subEndpoint || got.Stdin != want {
			t.Errorf("link %d = %+v; want POST %s with %s (the child's REST id, in the request's order)", i+1, got, subEndpoint, want)
		}
	}
	for _, c := range calls {
		if c.Host != "github.com" {
			t.Errorf("call %+v lacks --hostname github.com", c)
		}
		if strings.Contains(c.Args, "Orders: parent groups") || strings.Contains(c.Args, "Groups the issues") {
			t.Errorf("call %+v carries the title or body in args; they belong on stdin", c)
		}
		if strings.Contains(c.Stdin, "replace_parent") || strings.HasSuffix(c.Endpoint, "/sub_issue") || strings.HasSuffix(c.Endpoint, "/priority") || c.Method == "DELETE" || c.Method == "PATCH" || c.Method == "PUT" {
			t.Errorf("call %+v edits an existing tree; Admiral only creates and links", c)
		}
	}
	if got := r.launches(); len(got) != 0 {
		t.Errorf("launches = %+v; creating a campaign launches nothing", got)
	}
}

func TestCreateRefusesAChildThatAlreadyHasAParentAndWritesNothing(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.gh.child("acme/widgets", 31, "")
	r.gh.child("acme/widgets", 32, "https://api.github.com/repos/acme/widgets/issues/5")
	r.gh.child("acme/widgets", 33, "")
	r.gh.parentReady(31, 32, 33)

	code, body := r.create(createBody("acme/widgets#31", "acme/widgets#32", "acme/widgets#33"))
	if code != http.StatusConflict || !strings.Contains(body, "https://github.com/acme/widgets/issues/32") {
		t.Errorf("create = %d %s; want 409 naming https://github.com/acme/widgets/issues/32", code, body)
	}
	if w := posts(r.gh.restCalls()); len(w) != 0 {
		t.Errorf("writes = %+v; a parented child must stop the run before any write", w)
	}
}

func TestCreateAnswers502AndWritesNothingWhenAChildCannotBeRead(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.gh.child("acme/widgets", 31, "")
	r.gh.failREST("GET", issueEndpoint("acme/widgets", 32), "", "HTTP 500: server error")
	r.gh.parentReady(31, 32)

	code, body := r.create(createBody("acme/widgets#31", "acme/widgets#32"))
	if code != http.StatusBadGateway {
		t.Errorf("create = %d %s; want 502", code, body)
	}
	if w := posts(r.gh.restCalls()); len(w) != 0 {
		t.Errorf("writes = %+v; a failed read must stop the run before any write", w)
	}
}

func TestCreateRefusesABadRequestBeforeAnyGhCall(t *testing.T) {
	fifty1 := make([]string, 51)
	for i := range fifty1 {
		fifty1[i] = fmt.Sprintf("acme/widgets#%d", 100+i)
	}
	fifty := fifty1[:50]
	cases := []struct {
		name string
		raw  string
		body map[string]any
		want int
	}{
		{"bad JSON", `{"repo":`, nil, 400},
		{"no title", "", map[string]any{"repo": "acme/widgets", "title": "", "issues": []string{"acme/widgets#31"}}, 400},
		{"a blank title", "", map[string]any{"repo": "acme/widgets", "title": "  \t ", "issues": []string{"acme/widgets#31"}}, 400},
		{"no issues", "", createBody(), 400},
		{"51 issues", "", createBody(fifty1...), 400},
		{"a repeated ref", "", createBody("acme/widgets#31", "acme/widgets#32", "acme/widgets#31"), 400},
		{"an unparseable ref", "", createBody("acme/widgets#31", "not an issue"), 400},
		{"a pull request ref", "", createBody("https://github.com/acme/widgets/pull/31"), 400},
		{"hosts that differ", "", createBody("acme/widgets#31", "https://ghe.example.com/acme/widgets/issues/32"), 422},
		{"a repo no fleet routes", "", map[string]any{"repo": "nobody/nothing", "title": "T", "issues": []string{"nobody/nothing#1"}}, 422},
		{"a child owned by another owner", "", createBody("acme/widgets#31", "rival/widgets#32"), 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, fixtureTree())
			r.svc.Route = func(host, nwo string) string {
				if strings.HasPrefix(nwo, "nobody/") {
					return ""
				}
				return "work"
			}
			var code int
			if c.raw != "" {
				req := httptest.NewRequest("POST", "/api/campaigns/create", bytes.NewBufferString(c.raw))
				w := httptest.NewRecorder()
				r.mux.ServeHTTP(w, req)
				code = w.Code
			} else {
				code, _ = r.create(c.body)
			}
			if code != c.want {
				t.Errorf("create = %d, want %d", code, c.want)
			}
			if n := r.gh.ghCalls(); n != 0 {
				t.Errorf("gh ran %d times; a bad request is refused before any gh call", n)
			}
		})
	}
	t.Run("50 issues is the most", func(t *testing.T) {
		r := newRig(t, fixtureTree())
		for i := 100; i < 150; i++ {
			r.gh.child("acme/widgets", i, "")
		}
		nums := make([]int, 50)
		for i := range nums {
			nums[i] = 100 + i
		}
		r.gh.parentReady(nums...)
		if code, body := r.create(createBody(fifty...)); code != http.StatusCreated {
			t.Errorf("create of 50 issues = %d %s, want 201", code, body)
		}
	})
}

func TestCreateRefusesAChildThatRoutesToAnotherFleetBeforeAnyGhCall(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.svc.Route = func(host, nwo string) string {
		if nwo == "acme/other" {
			return "personal"
		}
		return "work"
	}
	code, body := r.create(createBody("acme/widgets#31", "acme/other#7"))
	if code != http.StatusUnprocessableEntity {
		t.Errorf("create = %d %s; want 422: one fleet's account must do every read and write", code, body)
	}
	if n := r.gh.ghCalls(); n != 0 {
		t.Errorf("gh ran %d times; want none", n)
	}
}

func TestCreateRefusesAClientThatReadsAnotherHostBeforeAnyGhCall(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.svc.Clients = func(fleet, host string) (*github.Client, bool) { return r.gh.client("ghe.example.com"), true }
	code, body := r.create(createBody("acme/widgets#31"))
	if code < 400 {
		t.Errorf("create = %d %s; want an error: the fleet's client reads ghe.example.com, not github.com", code, body)
	}
	if n := r.gh.ghCalls(); n != 0 {
		t.Errorf("gh ran %d times; want none", n)
	}
}

type createFailure struct {
	Error  string   `json:"error"`
	Parent string   `json:"parent"`
	Linked []string `json:"linked"`
}

func TestCreateStopsAtAFailedLinkAndAnswersWhatWasMade(t *testing.T) {
	r := newRig(t, fixtureTree())
	for _, n := range []int{31, 32, 33} {
		r.gh.child("acme/widgets", n, "")
	}
	r.gh.parentReady(31, 32, 33)
	r.gh.failREST("POST", subEndpoint, fmt.Sprint(restID(32)), "HTTP 422: Validation Failed")

	code, body := r.create(createBody("acme/widgets#31", "acme/widgets#32", "acme/widgets#33"))
	var got createFailure
	if code != http.StatusBadGateway || json.Unmarshal([]byte(body), &got) != nil || got.Error == "" ||
		got.Parent != newParentURL || !reflect.DeepEqual(got.Linked, []string{"https://github.com/acme/widgets/issues/31"}) {
		t.Errorf("create = %d %s; want 502 {error, parent %s, linked [issue 31]}", code, body, newParentURL)
	}
	w := posts(r.gh.restCalls())
	if len(w) != 3 || w[0].Endpoint != parentEndpoint {
		t.Errorf("writes = %+v; want the create, link 31 and the failed link 32, and no retry or link 33", w)
	}
	for _, c := range w {
		if c.Method != "POST" {
			t.Errorf("write %+v: nothing is deleted or edited after a failure", c)
		}
	}
	if _, ok := r.svc.Store.Lookup(newParentURL); ok {
		t.Error("the parent is followed although a link failed")
	}
}

func TestCreateAnswersAnEmptyLinkedListWhenTheFirstLinkFails(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.gh.child("acme/widgets", 31, "")
	r.gh.parentReady(31)
	r.gh.failREST("POST", subEndpoint, "", "HTTP 403: forbidden")

	code, body := r.create(createBody("acme/widgets#31"))
	var raw map[string]json.RawMessage
	if code != http.StatusBadGateway || json.Unmarshal([]byte(body), &raw) != nil || string(raw["linked"]) != "[]" || string(raw["parent"]) != `"`+newParentURL+`"` {
		t.Errorf("create = %d %s; want 502 with parent %s and \"linked\":[]", code, body, newParentURL)
	}
}

func TestCreateAnswers502WithTheParentWhenTheFollowFailsAfterEveryLink(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.gh.child("acme/widgets", 31, "")
	r.gh.child("acme/widgets", 32, "")
	r.gh.respondREST("POST", parentEndpoint, "", `{"number":99,"html_url":"`+newParentURL+`"}`)
	r.gh.respondREST("POST", subEndpoint, "", `{"id":1}`) // the new parent's tree has no answer, so the follow's read fails

	code, body := r.create(createBody("acme/widgets#31", "acme/widgets#32"))
	var got createFailure
	want := []string{"https://github.com/acme/widgets/issues/31", "https://github.com/acme/widgets/issues/32"}
	if code != http.StatusBadGateway || json.Unmarshal([]byte(body), &got) != nil || got.Error == "" || got.Parent != newParentURL || !reflect.DeepEqual(got.Linked, want) {
		t.Errorf("create = %d %s; want 502 {error, parent %s, linked %v}", code, body, newParentURL, want)
	}
	if w := posts(r.gh.restCalls()); len(w) != 3 {
		t.Errorf("writes = %+v; want the create and both links, none repeated", w)
	}
}

func TestCreateAnswers502AndLinksNothingWhenTheParentCannotBeCreated(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.gh.child("acme/widgets", 31, "")
	r.gh.failREST("POST", parentEndpoint, "", "HTTP 403: Resource not accessible")

	code, body := r.create(createBody("acme/widgets#31"))
	if code != http.StatusBadGateway {
		t.Errorf("create = %d %s; want 502", code, body)
	}
	for _, c := range posts(r.gh.restCalls()) {
		if strings.HasSuffix(c.Endpoint, "/sub_issues") {
			t.Errorf("link %+v ran although no parent exists", c)
		}
	}
}
