package ships

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

type mergeRig struct {
	*mooredFixture
	g       *pollGitHub
	session string
}

// newMergeRig moors a ship on prs, each ready on GitHub, whose pull requests GitHub answers with details.
func newMergeRig(t *testing.T, details map[string]moor.Detail, prs ...string) *mergeRig {
	t.Helper()
	g := &pollGitHub{detail: details}
	for _, u := range prs {
		g.set(u, readyStatus())
	}
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	return &mergeRig{mooredFixture: f, g: g, session: f.moor(t, "Nelson", "work", prs...)}
}

func (r *mergeRig) merge(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+r.session+"/merge", strings.NewReader(body)))
	return rec
}

// refused asserts the answer and that nothing merged, the ship is still moored and no merged outcome was written.
func (r *mergeRig) refused(t *testing.T, rec *httptest.ResponseRecorder, code int, text string) {
	t.Helper()
	if rec.Code != code || !strings.Contains(rec.Body.String(), text) {
		t.Errorf("POST merge = %d %s, want %d containing %q", rec.Code, rec.Body, code, text)
	}
	if got := r.g.mergeLog(); len(got) != 0 {
		t.Errorf("a refused merge ran gh: %v", got)
	}
	r.stillMoored(t)
}

func (r *mergeRig) stillMoored(t *testing.T) {
	t.Helper()
	if sh, ok := r.ship(t, "Nelson"); !ok || sh.Status != StatusMoored {
		t.Errorf("ship = %+v, want still moored", sh)
	}
	if got := len(r.records(t)); got != 1 {
		t.Errorf("mooring records = %d, want the ship's own", got)
	}
}

// noMergedOutcome reads the log once, at the end: it closes the writer.
func (r *mergeRig) noMergedOutcome(t *testing.T) {
	t.Helper()
	if ev := r.events(t, r.session, "merged"); len(ev) != 0 {
		t.Errorf("merged outcomes = %v, want none", ev)
	}
}

func TestMergeOfAShipWhoseStackOnGitHubIsNotExactlyItsPRsIsRefused(t *testing.T) {
	d := stackedDetails(2)
	st := d[layerURL(7)].Stack
	st.Size = 3
	st.Entries = append(st.Entries, moor.DetailStackEntry{Position: 3, Number: 99, URL: layerURL(99), State: "OPEN"})
	r := newMergeRig(t, d, layerURL(7), layerURL(9))
	r.refused(t, r.merge("{}"), http.StatusConflict, "neither one GitHub stack nor independent")
}

func TestMergeOfChainedPRsGitHubDoesNotHoldAsAStackIsRefused(t *testing.T) {
	r := newMergeRig(t, chainedDetails(2), layerURL(7), layerURL(9))
	r.refused(t, r.merge("{}"), http.StatusConflict, "stack it on GitHub")
}

func TestMergeOfAGitHubStackNamingAPRIsRefused(t *testing.T) {
	r := newMergeRig(t, stackedDetails(2), layerURL(7), layerURL(9))
	r.refused(t, r.merge(`{"pr":"`+layerURL(7)+`"}`), http.StatusConflict, "name no pr")
}

func TestMergeWhenGitHubDoesNotAnswerTheShapeQuestionIsRefused(t *testing.T) {
	r := newMergeRig(t, stackedDetails(2), layerURL(7), layerURL(9))
	r.g.detailErr = errors.New("gh: HTTP 502")
	r.refused(t, r.merge("{}"), http.StatusBadGateway, "gh: HTTP 502")
}

func TestMergeOfAStackWithALayerThatStoppedBeingReadyIsRefusedAndRunsNoMerge(t *testing.T) {
	r := newMergeRig(t, stackedDetails(2), layerURL(7), layerURL(9))
	// The last poll found both ready; GitHub now says layer 2 is not.
	st := readyStatus()
	st.MergeState = "BLOCKED"
	r.g.set(layerURL(9), st)
	r.refused(t, r.merge("{}"), http.StatusConflict, "not ready")
}

func TestMergeOfASinglePRThatStoppedBeingReadyIsRefused(t *testing.T) {
	r := newMergeRig(t, nil, layerURL(7))
	st := readyStatus()
	st.Review = "CHANGES_REQUESTED"
	st.MergeState = "BLOCKED"
	r.g.set(layerURL(7), st)
	r.refused(t, r.merge("{}"), http.StatusConflict, "not ready")
}

func TestMergeOfAPRNamingAnotherHostIsRefusedAndRunsNoGh(t *testing.T) {
	const foreign = "https://attacker.example/acme/widgets/pull/21"
	for what, prs := range map[string][]string{
		"one PR":     {foreign},
		"a stack":    {layerURL(7), foreign},
		"the second": {foreign, layerURL(7)},
	} {
		t.Run(what, func(t *testing.T) {
			r := newMergeRig(t, stackedDetails(2), prs...)
			r.refused(t, r.merge("{}"), http.StatusUnprocessableEntity, "GitHub host")
			if n := len(r.g.callLog()) + len(r.g.detailLog()); n != 0 {
				t.Errorf("gh was asked %d times about a PR on another host, want none", n)
			}
		})
	}
}

func TestMergeUpToNamesOnlyAPRTheShipOwnsAndOnlyGitHubsTopLayer(t *testing.T) {
	// GitHub's bottom to top is 7, 11, 9; the ship's record ends with 11.
	d := stackedDetails(3)
	st := d[layerURL(7)].Stack
	st.Entries[1], st.Entries[2] = st.Entries[2], st.Entries[1]
	st.Entries[1].Position, st.Entries[2].Position = 2, 3
	r := newMergeRig(t, d, layerURL(7), layerURL(9), layerURL(11))
	r.g.method = "rebase"

	r.refused(t, r.merge(`{"upTo":"https://github.com/acme/widgets/pull/99"}`), http.StatusBadRequest, "upTo is not one of")
	for _, up := range []string{layerURL(7), layerURL(11)} {
		r.refused(t, r.merge(`{"upTo":"`+up+`"}`), http.StatusBadRequest, "whole stack")
	}

	rec := r.merge(`{"upTo":"` + layerURL(9) + `"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST merge up to GitHub's top layer = %d, want 202: %s", rec.Code, rec.Body)
	}
	if got := r.g.mergeLog(); len(got) != 1 || got[0] != "stack 4 rebase" {
		t.Errorf("merges = %v, want one stack 4 rebase", got)
	}
	for _, u := range []string{layerURL(7), layerURL(9), layerURL(11)} {
		if !strings.Contains(rec.Body.String(), u) {
			t.Errorf("answer = %s, want it to name %s", rec.Body, u)
		}
	}
}

func TestMergeNamingAPRTheShipDoesNotOwnIsRefused(t *testing.T) {
	r := newMergeRig(t, independentDetails(2), layerURL(7), layerURL(9))
	r.refused(t, r.merge(`{"pr":"https://github.com/acme/widgets/pull/99"}`), http.StatusBadRequest, "pr is not one of")
}

func TestAFailedGhStackMergeReturnsItsMessageAndKeepsTheShipMoored(t *testing.T) {
	r := newMergeRig(t, stackedDetails(2), layerURL(7), layerURL(9))
	r.g.stackMerr = errors.New("gh stack: layer 2 has a conflict with its base")
	rec := r.merge("{}")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "layer 2 has a conflict with its base") {
		t.Errorf("POST merge = %d %s, want 502 carrying gh's message", rec.Code, rec.Body)
	}
	if got := r.g.mergeLog(); len(got) != 1 || got[0] != "stack 4 merge" {
		t.Errorf("merges = %v, want exactly the failed gh stack merge 4", got)
	}
	r.stillMoored(t)
	r.noMergedOutcome(t)
}

func TestAFailedGhMergeReturnsItsMessageKeepsTheShipMooredAndAllowsARetry(t *testing.T) {
	r := newMergeRig(t, nil, layerURL(7))
	r.g.mergeErr = errors.New("GraphQL: Pull request is not mergeable: the base branch policy prohibits the merge")
	rec := r.merge("{}")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "the base branch policy prohibits the merge") {
		t.Errorf("POST merge = %d %s, want 502 carrying gh's message", rec.Code, rec.Body)
	}
	if got := r.g.mergeLog(); len(got) != 1 || got[0] != "pr "+layerURL(7)+" merge" {
		t.Errorf("merges = %v, want exactly the failed one", got)
	}
	r.stillMoored(t)
	r.noMergedOutcome(t)

	r.g.mu.Lock()
	r.g.mergeErr = nil
	r.g.mu.Unlock()
	if rec := r.merge("{}"); rec.Code != http.StatusAccepted {
		t.Errorf("POST merge after the failure = %d %s, want 202", rec.Code, rec.Body)
	}
	if n := len(r.g.mergeLog()); n != 2 {
		t.Errorf("merges = %d, want the failed one and the retry", n)
	}
}

func TestMergeOfAShipThatIsNotThereIsNotFound(t *testing.T) {
	r := newMergeRig(t, nil, layerURL(7))
	r.session = "no-such-ship"
	if rec := r.merge("{}"); rec.Code != http.StatusNotFound {
		t.Errorf("POST merge = %d %s, want 404", rec.Code, rec.Body)
	}
	if got := r.g.mergeLog(); len(got) != 0 {
		t.Errorf("a refused merge ran gh: %v", got)
	}
}

func TestASecondMergeWhileOneIsRunningIsRefused(t *testing.T) {
	r := newMergeRig(t, nil, layerURL(7))
	r.g.mergeBlock = make(chan struct{})
	first := make(chan int, 1)
	go func() { first <- r.merge("{}").Code }()
	for end := time.Now().Add(10 * time.Second); len(r.g.mergeLog()) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the first merge never reached gh")
		}
	}
	if rec := r.merge("{}"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already under way") {
		t.Errorf("POST merge while one runs = %d %s, want 409", rec.Code, rec.Body)
	}
	close(r.g.mergeBlock)
	if code := <-first; code != http.StatusAccepted {
		t.Errorf("the first POST merge = %d, want 202", code)
	}
	if n := len(r.g.mergeLog()); n != 1 {
		t.Errorf("merges = %d, want one", n)
	}
}

func TestMergeWithoutAGuardIsForbidden(t *testing.T) {
	r := newMergeRig(t, nil, layerURL(7))
	r.s.MergeGuard = nil
	mux := http.NewServeMux()
	r.s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+r.session+"/merge", strings.NewReader("{}")))
	if rec.Code != http.StatusForbidden || len(r.g.mergeLog()) != 0 {
		t.Errorf("POST merge with no guard = %d %s with %v merged, want 403 and none", rec.Code, rec.Body, r.g.mergeLog())
	}
}
