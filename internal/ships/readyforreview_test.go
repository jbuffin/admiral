package ships

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

type readyResult struct {
	URL     string `json:"url"`
	Outcome string `json:"outcome"`
	Error   string `json:"error"`
}

type readyRig struct{ *mergeRig }

// openDetail is GitHub's fresh read of an open PR, a draft or not.
func openDetail(url string, draft bool) moor.Detail {
	return moor.Detail{URL: url, State: "OPEN", Draft: draft}
}

// newReadyRig moors a ship on prs, each an open draft unless it is named in ready.
func newReadyRig(t *testing.T, ready map[string]bool, prs ...string) *readyRig {
	t.Helper()
	details := map[string]moor.Detail{}
	for _, u := range prs {
		details[u] = openDetail(u, !ready[u])
	}
	return &readyRig{newMergeRig(t, details, prs...)}
}

func (r *readyRig) post(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+r.session+"/ready-for-review", strings.NewReader(body)))
	return rec
}

func urlsBody(urls ...string) string {
	b, _ := json.Marshal(map[string][]string{"prs": urls})
	return string(b)
}

func (r *readyRig) results(t *testing.T, rec *httptest.ResponseRecorder) []readyResult {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("POST ready-for-review = %d %s, want 200", rec.Code, rec.Body)
	}
	var body struct {
		Results []readyResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("answer %s is not JSON: %v", rec.Body, err)
	}
	return body.Results
}

// refused asserts the answer and that no PR was marked.
func (r *readyRig) refused(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Errorf("POST ready-for-review = %d %s, want %d", rec.Code, rec.Body, code)
	}
	if got := r.g.readyLog(); len(got) != 0 {
		t.Errorf("a refused request ran gh pr ready: %v", got)
	}
}

func TestMarkingADraftReadyForReviewRunsGhOnceAndReportsItMarked(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7))
	got := r.results(t, r.post(urlsBody(layerURL(7))))
	if want := []readyResult{{URL: layerURL(7), Outcome: "marked_ready"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("results = %+v, want %+v", got, want)
	}
	if calls := r.g.readyLog(); !reflect.DeepEqual(calls, []string{layerURL(7)}) {
		t.Errorf("gh pr ready calls = %v, want one for %s", calls, layerURL(7))
	}
	r.stillMoored(t)
	r.noMergedOutcome(t)
}

func TestMarkingAStackTriesEveryDraftBottomToTopAndOneFailingDoesNotStopTheRest(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7), layerURL(9), layerURL(11))
	r.g.readyErr = map[string]error{layerURL(9): errors.New("gh: Pull request is in a state that cannot be marked ready")}
	got := r.results(t, r.post(urlsBody(layerURL(7), layerURL(9), layerURL(11))))
	if len(got) != 3 {
		t.Fatalf("results = %+v, want one per PR", got)
	}
	if got[0].URL != layerURL(7) || got[0].Outcome != "marked_ready" {
		t.Errorf("bottom = %+v, want marked_ready", got[0])
	}
	if got[1].URL != layerURL(9) || got[1].Outcome != "failed" || !strings.Contains(got[1].Error, "cannot be marked ready") {
		t.Errorf("middle = %+v, want failed carrying gh's message", got[1])
	}
	if got[2].URL != layerURL(11) || got[2].Outcome != "marked_ready" {
		t.Errorf("top = %+v, want marked_ready: a failure below does not stop it", got[2])
	}
	if calls, want := r.g.readyLog(), []string{layerURL(7), layerURL(9), layerURL(11)}; !reflect.DeepEqual(calls, want) {
		t.Errorf("gh pr ready calls = %v, want %v in order", calls, want)
	}
}

func TestAPRAlreadyReadyForReviewIsSkippedWithNoCall(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, map[string]bool{layerURL(9): true}, layerURL(7), layerURL(9))
	got := r.results(t, r.post(urlsBody(layerURL(7), layerURL(9))))
	if len(got) != 2 || got[0].Outcome != "marked_ready" || got[1].URL != layerURL(9) || got[1].Outcome != "already_ready_for_review" {
		t.Errorf("results = %+v, want the draft marked and the other already_ready_for_review", got)
	}
	if calls := r.g.readyLog(); !reflect.DeepEqual(calls, []string{layerURL(7)}) {
		t.Errorf("gh pr ready calls = %v, want only the draft's", calls)
	}
}

func TestAMarkThatRunsPastGhsLimitIsAFailedLineSayingTheResultIsUnknown(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7))
	r.g.readyErr = map[string]error{layerURL(7): moor.ErrMergeTimeout}
	got := r.results(t, r.post(urlsBody(layerURL(7))))
	if len(got) != 1 || got[0].Outcome != "failed" || !strings.Contains(got[0].Error, "unknown") || strings.Contains(got[0].Error, "merge") {
		t.Errorf("results = %+v, want failed, result unknown, and no word about a merge", got)
	}
	select {
	case <-r.s.kickChan():
	default:
		t.Errorf("no poll was asked for after a mark whose result is unknown")
	}
}

func TestMarkingReadyAsksForAPollSoTheChipsChange(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7))
	r.results(t, r.post(urlsBody(layerURL(7))))
	select {
	case <-r.s.kickChan():
	default:
		t.Errorf("no poll was asked for after marking a PR ready for review")
	}
}

func TestMarkingReadyWithoutAMergeGuardIsForbidden(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7))
	r.s.MergeGuard = nil
	mux := http.NewServeMux()
	r.s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+r.session+"/ready-for-review", strings.NewReader(urlsBody(layerURL(7)))))
	if rec.Code != http.StatusForbidden || len(r.g.readyLog()) != 0 {
		t.Errorf("POST ready-for-review with no guard = %d %s with %v marked, want 403 and none", rec.Code, rec.Body, r.g.readyLog())
	}
}

func TestMarkingReadyOnAShipThatIsNotThereIsNotFound(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7))
	r.session = "no-such-ship"
	r.refused(t, r.post(urlsBody(layerURL(7))), http.StatusNotFound)
}

func TestMarkingReadyOnAReviewShipIsRefused(t *testing.T) {
	t.Parallel()
	g := &pollGitHub{detail: map[string]moor.Detail{prSeven: openDetail(prSeven, true)}}
	g.set(prSeven, readyStatus())
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moorReviewing(t, "Nelson", "work", []string{prSeven}, prSeven)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+session+"/ready-for-review", strings.NewReader(urlsBody(prSeven))))
	if rec.Code != http.StatusConflict || len(g.readyLog()) != 0 {
		t.Errorf("POST ready-for-review on a review ship = %d %s with %v marked, want 409 and none", rec.Code, rec.Body, g.readyLog())
	}
}

func TestMarkingAPRTheShipDoesNotOwnIsRefusedAndMarksNothing(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7))
	r.refused(t, r.post(urlsBody(layerURL(7), "https://github.com/acme/widgets/pull/99")), http.StatusBadRequest)
}

func TestMarkingAPROnAnotherHostIsRefusedAndRunsNoGh(t *testing.T) {
	t.Parallel()
	const foreign = "https://attacker.example/acme/widgets/pull/21"
	r := newReadyRig(t, nil, foreign)
	r.refused(t, r.post(urlsBody(foreign)), http.StatusUnprocessableEntity)
}

func TestARequestWithAClosedPRMarksNoneOfThem(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7), layerURL(9))
	closed := openDetail(layerURL(9), true)
	closed.State = "CLOSED"
	r.g.detail[layerURL(9)] = closed
	r.refused(t, r.post(urlsBody(layerURL(7), layerURL(9))), http.StatusConflict)
}

func TestARequestThatIsNotJSONIsRefused(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7))
	r.refused(t, r.post("{not json"), http.StatusBadRequest)
}

func TestMarkingReadyWhileAMergeOrUpdateIsUnderWayIsRefused(t *testing.T) {
	t.Parallel()
	r := newReadyRig(t, nil, layerURL(7))
	if !r.s.startMerge(r.session) {
		t.Fatal("could not start a merge to hold the ship")
	}
	defer r.s.endMerge(r.session)
	r.refused(t, r.post(urlsBody(layerURL(7))), http.StatusConflict)
}
