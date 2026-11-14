package ships

// The PR detail route serves one PR a ship carries, read fresh from GitHub through the ship's fleet client.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

func (f *mooredFixture) detail(session, prURL string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships/"+session+"/pr?url="+url.QueryEscape(prURL), nil))
	return rec
}

func wantFailure(t *testing.T, rec *httptest.ResponseRecorder, why string) {
	t.Helper()
	var body struct{ Error string }
	if rec.Code < 500 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
		t.Fatalf("%s: code %d, body %q; want a 5xx with a JSON error", why, rec.Code, rec.Body)
	}
}

func sevenDetail(title string) moor.Detail {
	return moor.Detail{Number: 7, URL: prSeven, Title: title, Body: strings.Repeat("Description line.\n", 500), State: "OPEN", Head: "feat", Base: "main", DefaultBranch: "trunk",
		Stack: &moor.DetailStack{Number: 4, Size: 2, URL: "https://github.com/acme/widgets/stack/4"}}
}

func TestTheDetailRouteServesAMooredShipsPRReadFreshEveryTime(t *testing.T) {
	g := &pollGitHub{detail: map[string]moor.Detail{prSeven: sevenDetail("First")}}
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moor(t, "Nelson", "work", prSeven)

	rec := f.detail("nelson", prSeven)
	var got moor.Detail
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Title != "First" || got.Number != 7 || got.Body != sevenDetail("").Body ||
		got.Stack == nil || got.Stack.URL != "https://github.com/acme/widgets/stack/4" || got.DefaultBranch != "trunk" {
		t.Fatalf("detail = %d %.200s, want 200 with the PR's title, full body, stack and default branch", rec.Code, rec.Body)
	}

	g.mu.Lock()
	g.detail[prSeven] = sevenDetail("Second")
	g.mu.Unlock()
	rec = f.detail("nelson", prSeven)
	if json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Title != "Second" {
		t.Errorf("the second read = %.200s, want the title GitHub has now, not a cached one", rec.Body)
	}
	if calls := g.detailLog(); len(calls) != 2 {
		t.Errorf("GitHub was asked %v, want one single-PR read per request", calls)
	}
	if g.callLog() != nil {
		t.Errorf("the batched query ran %v, want the detail read alone", g.callLog())
	}
}

func TestTheDetailRouteRefusesAPRTheShipDoesNotCarryAndAShipThatIsNotThere(t *testing.T) {
	g := &pollGitHub{detail: map[string]moor.Detail{prSeven: sevenDetail("T"), prNine: sevenDetail("Other")}}
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moor(t, "Nelson", "work", prSeven)

	wantError(t, f.detail("nelson", prNine), http.StatusNotFound, "a PR the ship does not carry")
	wantError(t, f.detail("nobody", prSeven), http.StatusNotFound, "a ship that does not exist")
	wantError(t, f.detail("nelson", "https://example.com/not/a/pr"), http.StatusNotFound, "a URL that is no PR")
	if calls := g.detailLog(); len(calls) != 0 {
		t.Errorf("GitHub was asked %v, want no call for a refused request", calls)
	}
}

func TestTheDetailRouteAnswersAnErrorWhenGitHubFailsTimesOutOrIsNotConfigured(t *testing.T) {
	t.Run("GitHub fails", func(t *testing.T) {
		g := &pollGitHub{detailErr: errors.New("gh: HTTP 502")}
		f := mooredService(t, map[string]*pollGitHub{"work": g})
		f.moor(t, "Nelson", "work", prSeven)
		wantFailure(t, f.detail("nelson", prSeven), "a failing read")
	})
	t.Run("GitHub does not answer in time", func(t *testing.T) {
		old := detailTimeout
		detailTimeout = 50 * time.Millisecond
		t.Cleanup(func() { detailTimeout = old })
		g := &pollGitHub{detailBlock: make(chan struct{})}
		t.Cleanup(func() { close(g.detailBlock) })
		f := mooredService(t, map[string]*pollGitHub{"work": g})
		f.moor(t, "Nelson", "work", prSeven)
		start := time.Now()
		rec := f.detail("nelson", prSeven)
		wantFailure(t, rec, "a read that never answers")
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("the request took %s, want it cut at the timeout", took)
		}
	})
	t.Run("the fleet has no client", func(t *testing.T) {
		f := mooredService(t, map[string]*pollGitHub{})
		f.moor(t, "Nelson", "work", prSeven)
		wantFailure(t, f.detail("nelson", prSeven), "a fleet with no GitHub")
	})
}

func TestTheDetailRouteServesAPRARunningShipCarries(t *testing.T) {
	r := newPRRig(t)
	r.gh.mu.Lock()
	r.gh.detail = map[string]moor.Detail{prSeven: sevenDetail("Running")}
	r.gh.mu.Unlock()
	r.candidates = []string{prSeven}
	r.mine(prSeven, "OPEN", time.Minute)
	r.s.PollMoored(t.Context())

	get := func(u string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships/saltpaw/pr?url="+url.QueryEscape(u), nil))
		return rec
	}
	var got moor.Detail
	if rec := get(prSeven); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Title != "Running" {
		t.Errorf("detail = %d %.200s, want 200 with the PR's title", rec.Code, rec.Body)
	}
	wantError(t, get(prNine), http.StatusNotFound, "a PR the running ship does not carry")
}
