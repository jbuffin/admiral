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

// behindStatus is the live case: approved, green and mergeable, but behind a base that requires up-to-date branches.
func behindStatus() moor.Status {
	st := readyStatus()
	st.MergeState, st.StrictBase = "BEHIND", true
	return st
}

type updateRig struct{ *mergeRig }

// newUpdateRig moors a ship with no valid resume (so no_resume) on prs, each behind its base.
func newUpdateRig(t *testing.T, details map[string]moor.Detail, prs ...string) *updateRig {
	t.Helper()
	r := &updateRig{newMergeRig(t, details, prs...)}
	for _, u := range prs {
		r.g.set(u, behindStatus())
	}
	return r
}

func (r *updateRig) update(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+r.session+"/update-branch", strings.NewReader(body)))
	return rec
}

func (r *updateRig) refused(t *testing.T, rec *httptest.ResponseRecorder, code int, text string) {
	t.Helper()
	if rec.Code != code || !strings.Contains(rec.Body.String(), text) {
		t.Errorf("POST update-branch = %d %s, want %d containing %q", rec.Code, rec.Body, code, text)
	}
	if got := r.g.updateLog(); len(got) != 0 {
		t.Errorf("a refused update ran gh: %v", got)
	}
	r.stillMoored(t)
}

func TestUpdateBranchOfABehindPROnAShipThatCannotResumeRunsGhOnceAndAnswers202(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, nil, layerURL(7))
	rec := r.update(`{"pr":"` + layerURL(7) + `"}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"updated":true`) {
		t.Fatalf("POST update-branch = %d %s, want 202 with updated true", rec.Code, rec.Body)
	}
	if got := r.g.updateLog(); len(got) != 1 || got[0] != layerURL(7) {
		t.Errorf("update-branch calls = %v, want one for %s", got, layerURL(7))
	}
	r.stillMoored(t)
	if ev := r.events(t, r.session, "merged"); len(ev) != 0 {
		t.Errorf("merged outcomes = %v, want none: updating a branch merges nothing", ev)
	}
}

func TestUpdateBranchOfAPRThatIsNotBehindIsRefused(t *testing.T) {
	t.Parallel()
	for name, mod := range map[string]func(*moor.Status){
		"ready":  func(s *moor.Status) { *s = readyStatus() },
		"closed": func(s *moor.Status) { s.State = "CLOSED" },
		"merged": func(s *moor.Status) { s.State = "MERGED" },
		"dirty":  func(s *moor.Status) { s.MergeState = "DIRTY" },
	} {
		t.Run(name, func(t *testing.T) {
			r := newUpdateRig(t, nil, layerURL(7))
			st := behindStatus()
			mod(&st)
			r.g.set(layerURL(7), st)
			r.refused(t, r.update(`{"pr":"`+layerURL(7)+`"}`), http.StatusConflict, "")
		})
	}
}

func TestUpdateBranchNamingAPRTheShipDoesNotOwnIsRefused(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, nil, layerURL(7))
	r.refused(t, r.update(`{"pr":"https://github.com/acme/widgets/pull/99"}`), http.StatusBadRequest, "pr is not one of")
}

func TestUpdateBranchOfALayerOfAGitHubStackIsRefused(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, stackedDetails(2), layerURL(7), layerURL(9))
	r.refused(t, r.update(`{"pr":"`+layerURL(9)+`"}`), http.StatusConflict, "stack")
}

func TestUpdateBranchOfAPRNamingAnotherHostIsRefusedAndRunsNoGh(t *testing.T) {
	t.Parallel()
	const foreign = "https://attacker.example/acme/widgets/pull/21"
	r := newUpdateRig(t, nil, foreign)
	r.refused(t, r.update(`{"pr":"`+foreign+`"}`), http.StatusUnprocessableEntity, "GitHub host")
	if n := len(r.g.callLog()) + len(r.g.detailLog()); n != 0 {
		t.Errorf("gh was asked %d times about a PR on another host, want none", n)
	}
}

func TestUpdateBranchOfAReviewShipsPRIsRefused(t *testing.T) {
	t.Parallel()
	g := &pollGitHub{}
	g.set(prSeven, behindStatus())
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moorReviewing(t, "Nelson", "work", []string{prSeven}, prSeven)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+session+"/update-branch", strings.NewReader(`{"pr":"`+prSeven+`"}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "review") {
		t.Errorf("POST update-branch on a review ship = %d %s, want 409 naming the review", rec.Code, rec.Body)
	}
	if got := g.updateLog(); len(got) != 0 {
		t.Errorf("a refused update ran gh: %v", got)
	}
}

func TestUpdateBranchOfAShipThatIsNotThereIsNotFound(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, nil, layerURL(7))
	r.session = "no-such-ship"
	if rec := r.update(`{"pr":"` + layerURL(7) + `"}`); rec.Code != http.StatusNotFound {
		t.Errorf("POST update-branch = %d %s, want 404", rec.Code, rec.Body)
	}
	if got := r.g.updateLog(); len(got) != 0 {
		t.Errorf("a refused update ran gh: %v", got)
	}
}

func TestAFailedGhUpdateBranchReturnsItsMessageAndKeepsTheShipMoored(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, nil, layerURL(7))
	r.g.updateErr = errors.New("gh: Head branch was modified. Review and try the update again.")
	rec := r.update(`{"pr":"` + layerURL(7) + `"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "Head branch was modified") {
		t.Errorf("POST update-branch = %d %s, want 502 carrying gh's message", rec.Code, rec.Body)
	}
	if got := r.g.updateLog(); len(got) != 1 {
		t.Errorf("update-branch calls = %v, want exactly the failed one", got)
	}
	r.stillMoored(t)
}

func TestAnUpdateBranchThatRunsPastGhsLimitAnswers504AndRefreshesThePoll(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, nil, layerURL(7))
	r.g.updateErr = moor.ErrMergeTimeout
	rec := r.update(`{"pr":"` + layerURL(7) + `"}`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("POST update-branch = %d %s, want 504", rec.Code, rec.Body)
	}
	select {
	case <-r.s.kickChan():
	default:
		t.Errorf("no poll was asked for after an update whose result is unknown")
	}
}

func TestASecondUpdateBranchWhileOneIsRunningIsRefused(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, nil, layerURL(7))
	r.g.updateBlock = make(chan struct{})
	first := make(chan int, 1)
	go func() { first <- r.update(`{"pr":"` + layerURL(7) + `"}`).Code }()
	for end := time.Now().Add(10 * time.Second); len(r.g.updateLog()) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the first update never reached gh")
		}
	}
	if rec := r.update(`{"pr":"` + layerURL(7) + `"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already under way") {
		t.Errorf("POST update-branch while one runs = %d %s, want 409", rec.Code, rec.Body)
	}
	if rec := r.merge("{}"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already under way") {
		t.Errorf("POST merge while an update runs = %d %s, want 409", rec.Code, rec.Body)
	}
	close(r.g.updateBlock)
	if code := <-first; code != http.StatusAccepted {
		t.Errorf("the first POST update-branch = %d, want 202", code)
	}
	if n := len(r.g.updateLog()); n != 1 {
		t.Errorf("update-branch calls = %d, want one", n)
	}
}

func TestUpdateBranchWithoutAGuardIsForbidden(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, nil, layerURL(7))
	r.s.MergeGuard = nil
	mux := http.NewServeMux()
	r.s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+r.session+"/update-branch", strings.NewReader(`{"pr":"`+layerURL(7)+`"}`)))
	if rec.Code != http.StatusForbidden || len(r.g.updateLog()) != 0 {
		t.Errorf("POST update-branch with no guard = %d %s with %v updated, want 403 and none", rec.Code, rec.Body, r.g.updateLog())
	}
}

func TestAMooredPRBehindItsBaseServesWhyItCannotMerge(t *testing.T) {
	t.Parallel()
	r := newUpdateRig(t, nil, layerURL(7))
	r.s.PollMoored(t.Context())
	sh, ok := r.ship(t, "Nelson")
	if !ok || len(sh.PRs) != 1 {
		t.Fatalf("ship = %+v, want one PR", sh)
	}
	if p := sh.PRs[0]; p.Ready || !p.Behind || len(p.MergeReasons) != 1 || p.MergeReasons[0] != "behind" {
		t.Errorf("PR = %+v, want not ready, behind, and the one merge reason behind", p)
	}
}
