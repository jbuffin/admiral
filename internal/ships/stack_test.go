package ships

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

var errNoStack = errors.New("pull request 9 cannot join a stack")

// chainedSevenNine makes prSeven the bottom of a chain and prNine its top, in no GitHub stack.
func chainedSevenNine(g *pollGitHub) {
	g.detail = map[string]moor.Detail{
		prSeven: {Number: 7, State: "OPEN", Head: "b7", Base: "main", DefaultBranch: "main"},
		prNine:  {Number: 9, State: "OPEN", Head: "b9", Base: "b7", DefaultBranch: "main"},
	}
}

func (f *mooredFixture) getStack(session string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ships/"+session+"/stack", nil))
	return rec
}

func TestStackLinkPassesGitHubsNumbersBottomToTopWhateverOrderTheShipRecordedThem(t *testing.T) {
	g := &pollGitHub{}
	chainedSevenNine(g)
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prNine, prSeven)

	rec := f.post("/api/ships/" + session + "/stack")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST stack = %d %s, want 200", rec.Code, rec.Body)
	}
	if want := [][]int{{7, 9}}; !slices.EqualFunc(g.linked, want, slices.Equal[[]int]) {
		t.Errorf("LinkStack calls = %v, want one call with 7 then 9", g.linked)
	}
	if want := []string{"acme/widgets"}; !slices.Equal(g.linkedRepo, want) {
		t.Errorf("LinkStack repos = %v, want %v", g.linkedRepo, want)
	}
	var got struct {
		Shape string   `json:"shape"`
		PRs   []string `json:"prs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Shape != "chained" || !slices.Equal(got.PRs, []string{prSeven, prNine}) {
		t.Errorf("answer = %s, want the shape read after the link: still chained, 7 then 9 (this fake's GitHub holds no stack)", rec.Body)
	}
}

func TestStackLinkThatRunsPastItsLimitAnswers504AndAsksForAPoll(t *testing.T) {
	g := &pollGitHub{linkErr: moor.ErrMergeTimeout}
	chainedSevenNine(g)
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven, prNine)

	rec := f.post("/api/ships/" + session + "/stack")
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("POST stack = %d %s, want 504", rec.Code, rec.Body)
	}
	select {
	case <-f.s.kickChan():
	default:
		t.Errorf("no poll was asked for after a link whose result is unknown")
	}
}

func TestStackLinkThatGhRefusesAnswers502WithGhsMessage(t *testing.T) {
	g := &pollGitHub{linkErr: errNoStack}
	chainedSevenNine(g)
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven, prNine)

	rec := f.post("/api/ships/" + session + "/stack")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), errNoStack.Error()) {
		t.Errorf("POST stack = %d %s, want 502 carrying gh's message", rec.Code, rec.Body)
	}
}

func TestStackOfAFleetWithNoGitHubSourceIsRefused(t *testing.T) {
	f := mooredService(t, map[string]*pollGitHub{})
	session := f.moor(t, "Nelson", "work", prSeven, prNine)

	if rec := f.post("/api/ships/" + session + "/stack"); rec.Code != http.StatusConflict {
		t.Errorf("POST stack = %d %s, want 409", rec.Code, rec.Body)
	}
	if rec := f.getStack(session); rec.Code != http.StatusBadGateway {
		t.Errorf("GET stack = %d %s, want 502: the fleet has no GitHub access", rec.Code, rec.Body)
	}
}

func TestStackOfAnUnknownShipIsNotFound(t *testing.T) {
	f := mooredService(t, map[string]*pollGitHub{"work": {}})
	if rec := f.getStack("nobody"); rec.Code != http.StatusNotFound {
		t.Errorf("GET stack = %d %s, want 404", rec.Code, rec.Body)
	}
	if rec := f.post("/api/ships/nobody/stack"); rec.Code != http.StatusNotFound {
		t.Errorf("POST stack = %d %s, want 404", rec.Code, rec.Body)
	}
}

func TestStackOfAShipWhoseDetailReadFailsAnswers502(t *testing.T) {
	g := &pollGitHub{detailErr: errNoStack}
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven, prNine)
	if rec := f.getStack(session); rec.Code != http.StatusBadGateway {
		t.Errorf("GET stack = %d %s, want 502", rec.Code, rec.Body)
	}
	if rec := f.post("/api/ships/" + session + "/stack"); rec.Code != http.StatusBadGateway || len(g.linked) != 0 {
		t.Errorf("POST stack = %d %s with %d link calls, want 502 and none", rec.Code, rec.Body, len(g.linked))
	}
}

func TestStackLinkRefusesPRsOfTwoRepositoriesAndRunsNoLink(t *testing.T) {
	const other = "https://github.com/acme/other/pull/9"
	g := &pollGitHub{detail: map[string]moor.Detail{
		prSeven: {Number: 7, State: "OPEN", Head: "b7", Base: "main", DefaultBranch: "main"},
		other:   {Number: 9, State: "OPEN", Head: "b9", Base: "b7", DefaultBranch: "main"},
	}}
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven, other)

	if rec := f.post("/api/ships/" + session + "/stack"); rec.Code != http.StatusConflict || len(g.linked) != 0 {
		t.Errorf("POST stack = %d %s with %d link calls, want 409 and none", rec.Code, rec.Body, len(g.linked))
	}
	var got struct{ Shape string }
	if rec := f.getStack(session); json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Shape != "mixed" {
		t.Errorf("GET stack = %s, want mixed", rec.Body)
	}
}

func TestStackLinkOfAWakingShipIsRefusedAndRunsNoLink(t *testing.T) {
	g := &pollGitHub{}
	chainedSevenNine(g)
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven, prNine)
	if !f.s.startWaking(session) {
		t.Fatal("could not take the waking slot")
	}
	defer f.s.endWaking(session)

	if rec := f.post("/api/ships/" + session + "/stack"); rec.Code != http.StatusConflict || len(g.linked) != 0 {
		t.Errorf("POST stack = %d %s with %d link calls, want 409 and none", rec.Code, rec.Body, len(g.linked))
	}
}
