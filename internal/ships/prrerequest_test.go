package ships

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

func (f *mooredFixture) reRequest(session, pr, login string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	body := `{"pr":"` + pr + `","login":"` + login + `"}`
	f.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/"+session+"/pr/rerequest", strings.NewReader(body)))
	return rec
}

func (g *pollGitHub) setDetail(d moor.Detail) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.detail = map[string]moor.Detail{d.URL: d}
}

func person(login string) moor.DetailActor { return moor.DetailActor{Login: login} }

func reviewedBy(login, state string) moor.DetailReview {
	return moor.DetailReview{Author: person(login), State: state}
}

// reviewedPR is octocat's PR: hubot approved it, a bot commented, and monalisa never reviewed.
func reviewedPR() moor.Detail {
	return moor.Detail{
		Number: 7, URL: prSeven, State: "OPEN", Author: person("octocat"),
		Reviews: []moor.DetailReview{
			reviewedBy("hubot", "APPROVED"),
			{Author: moor.DetailActor{Login: "copilot-pull-request-reviewer", Bot: true}, State: "COMMENTED"},
			reviewedBy("octocat", "COMMENTED"),
			reviewedBy("defunkt", "CHANGES_REQUESTED"),
		},
		Requested: []moor.DetailActor{person("defunkt"), person("monalisa")},
	}
}

func TestReRequestAsksGitHubToReRequestAPersonOrBotWhoReviewedAndIsNotPending(t *testing.T) {
	for _, login := range []string{"hubot", "copilot-pull-request-reviewer"} {
		t.Run(login, func(t *testing.T) {
			g := &pollGitHub{}
			g.setDetail(reviewedPR())
			f := mooredService(t, map[string]*pollGitHub{"work": g})
			session := f.moor(t, "Nelson", "work", prSeven)

			if rec := f.reRequest(session, prSeven, login); rec.Code != http.StatusOK {
				t.Fatalf("re-request %s = %d %s, want 200", login, rec.Code, rec.Body)
			}
			if got := g.reRequestLog(); len(got) != 1 || got[0] != prSeven+" "+login {
				t.Errorf("GitHub was asked %v, want one request for %s on %s", got, login, prSeven)
			}
			if got := g.detailLog(); len(got) != 1 {
				t.Errorf("the PR was read %v, want one fresh read to check the rule", got)
			}
		})
	}
}

func TestReRequestRefusesWhatGitHubsPageDoesNotOfferWithoutCallingGitHub(t *testing.T) {
	for _, c := range []struct{ why, login string }{
		{"the PR's author, who also reviewed", "octocat"},
		{"someone requested again after reviewing", "defunkt"},
		{"someone requested who never reviewed", "monalisa"},
		{"someone who never reviewed and was never requested", "torvalds"},
		{"no login", ""},
	} {
		t.Run(c.why, func(t *testing.T) {
			g := &pollGitHub{}
			g.setDetail(reviewedPR())
			f := mooredService(t, map[string]*pollGitHub{"work": g})
			session := f.moor(t, "Nelson", "work", prSeven)

			rec := f.reRequest(session, prSeven, c.login)
			if rec.Code < 400 || rec.Code >= 500 || rec.Code == http.StatusNotFound {
				t.Errorf("re-request of %s = %d %s, want a 4xx other than 404 with a JSON error", c.why, rec.Code, rec.Body)
			}
			wantError(t, rec, rec.Code, "a refused request")
			if got := g.reRequestLog(); len(got) != 0 {
				t.Errorf("GitHub was asked %v, want no request for %s", got, c.why)
			}
		})
	}
}

func TestReRequestRefusesAPRTheShipDoesNotCarryAndAShipThatIsNotThere(t *testing.T) {
	g := &pollGitHub{}
	g.setDetail(reviewedPR())
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)

	wantError(t, f.reRequest(session, prNine, "hubot"), http.StatusNotFound, "a PR the ship does not carry")
	wantError(t, f.reRequest("nobody", prSeven, "hubot"), http.StatusNotFound, "a ship that does not exist")
	if got := g.reRequestLog(); len(got) != 0 {
		t.Errorf("GitHub was asked %v, want no request for a refused PR", got)
	}
}

func TestReRequestReturnsGitHubsRefusalAndAFailedRead(t *testing.T) {
	t.Run("GitHub refuses the request", func(t *testing.T) {
		g := &pollGitHub{reRequestErr: errors.New("Could not resolve to a User with the login of 'hubot'.")}
		g.setDetail(reviewedPR())
		f := mooredService(t, map[string]*pollGitHub{"work": g})
		session := f.moor(t, "Nelson", "work", prSeven)

		rec := f.reRequest(session, prSeven, "hubot")
		if rec.Code < 400 || !strings.Contains(rec.Body.String(), "Could not resolve to a User with the login of 'hubot'.") {
			t.Errorf("re-request = %d %s, want an error status carrying GitHub's message", rec.Code, rec.Body)
		}
	})
	t.Run("the fresh read fails", func(t *testing.T) {
		g := &pollGitHub{detailErr: errors.New("gh: HTTP 502")}
		f := mooredService(t, map[string]*pollGitHub{"work": g})
		session := f.moor(t, "Nelson", "work", prSeven)

		rec := f.reRequest(session, prSeven, "hubot")
		wantError(t, rec, http.StatusBadGateway, "a failing read, as the detail route answers it")
		if got := g.reRequestLog(); len(got) != 0 {
			t.Errorf("GitHub was asked %v, want no request when the PR could not be read", got)
		}
	})
}
