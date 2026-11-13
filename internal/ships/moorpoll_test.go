package ships

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
)

const (
	prSeven = "https://github.com/acme/widgets/pull/7"
	prNine  = "https://github.com/acme/widgets/pull/9"
	prOther = "https://github.com/globex/app/pull/3"
)

// pollGitHub answers the batched pull request query for one fleet's client.
type pollGitHub struct {
	mu       sync.Mutex
	status   map[string]moor.Status // a URL left out is NOT_FOUND
	err      error                  // a transport failure
	mergeErr error                  // what MergePR answers
	un       []string               // PRs the answer leaves unanswered, with an *moor.Unanswered
	failOn   string                 // a call that asks for this URL fails outright
	block    chan struct{}          // when set, a call waits for it to close
	calls    [][]string
	active   int
	most     int

	exitCheck   bool                   // OpenPRs answers from status instead of failing
	detail      map[string]moor.Detail // what PullRequestDetail answers per URL
	detailErr   error
	detailBlock chan struct{} // when set, a detail call waits for it to close or its ctx to end
	detailCalls []string
}

func (g *pollGitHub) Host() string { return "github.com" }

// OpenPRs is what mooring at exit asks; it answers from status only once a test sets exitCheck.
func (g *pollGitHub) OpenPRs(_ context.Context, urls []string) (map[string]bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.exitCheck {
		return nil, errors.New("the poll uses the batched query")
	}
	out := map[string]bool{}
	for _, u := range urls {
		if st, ok := g.status[u]; ok {
			out[u] = st.State == "OPEN"
		}
	}
	return out, nil
}

// PullRequestDetail answers detail[url], counts each call and fails with detailErr or waits for detailBlock when set.
func (g *pollGitHub) PullRequestDetail(ctx context.Context, url string) (moor.Detail, error) {
	g.mu.Lock()
	g.detailCalls = append(g.detailCalls, url)
	d, err, block := g.detail[url], g.detailErr, g.detailBlock
	g.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return moor.Detail{}, ctx.Err()
		}
	}
	return d, err
}

func (g *pollGitHub) detailLog() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.detailCalls...)
}

func (g *pollGitHub) PullRequests(_ context.Context, urls []string) (map[string]moor.Status, error) {
	g.mu.Lock()
	g.calls = append(g.calls, append([]string(nil), urls...))
	g.active++
	g.most = max(g.most, g.active)
	block, err := g.block, g.err
	g.mu.Unlock()
	if block != nil {
		<-block
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if g.failOn != "" && slices.Contains(urls, g.failOn) {
		err = errors.New("the query failed")
	}
	if err != nil {
		return nil, err
	}
	out := map[string]moor.Status{}
	for _, u := range urls {
		if st, ok := g.status[u]; ok {
			out[u] = st
		}
	}
	if len(g.un) > 0 {
		return out, &moor.Unanswered{URLs: g.un}
	}
	return out, nil
}

func (g *pollGitHub) DefaultMergeMethod(context.Context, string) (string, error) { return "merge", nil }

func (g *pollGitHub) MergePR(context.Context, string, string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.mergeErr
}

func (g *pollGitHub) StackPRs(context.Context, string, string) ([]moor.StackLayer, error) {
	return nil, nil
}

func (g *pollGitHub) MergeStack(context.Context, string, string, string) error {
	return g.MergePR(context.Background(), "", "")
}

func (g *pollGitHub) set(url string, st moor.Status) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.status == nil {
		g.status = map[string]moor.Status{}
	}
	g.status[url] = st
}

func (g *pollGitHub) setErr(err error) { g.mu.Lock(); g.err = err; g.mu.Unlock() }

func (g *pollGitHub) callLog() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.calls...)
}

type mooredFixture struct {
	s    *Service
	home string
	mux  *http.ServeMux
}

// mooredService is a Service with no tmux at all: a moored ship has no session to find.
func mooredService(t *testing.T, gh map[string]*pollGitHub) *mooredFixture {
	t.Helper()
	home := t.TempDir()
	st, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work"}, {Key: "home", Name: "Home"}}}
	w := outcomes.New(filepath.Join(home, "outcomes"), t.Logf)
	t.Cleanup(w.Close)
	s := &Service{
		Host:     sessionhost.New("no-such-tmux", filepath.Join(home, "tmux.sock")),
		Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return cfg }},
		State:    st, Hub: server.NewHub(), Outcomes: w,
		MergeGuard: func(h http.Handler) http.Handler { return h },
		GitHub: func(fleet string) moor.GitHub {
			if g := gh[fleet]; g != nil {
				return g
			}
			return nil
		},
	}
	mux := http.NewServeMux()
	s.Routes(mux)
	return &mooredFixture{s: s, home: home, mux: mux}
}

func (f *mooredFixture) moor(t *testing.T, name, fleet string, prs ...string) string {
	t.Helper()
	session := strings.ToLower(name)
	rec := moor.Record{Name: name, Session: session, Fleet: fleet, Harness: "claude", Checkout: "/src/widgets", PRs: prs,
		MooredAt: time.Now().UTC().Truncate(time.Second)}
	if err := moor.Write(f.home, rec); err != nil {
		t.Fatal(err)
	}
	return session
}

func (f *mooredFixture) post(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
	return rec
}

func (f *mooredFixture) ship(t *testing.T, name string) (Ship, bool) {
	t.Helper()
	for _, sh := range f.s.List() {
		if sh.Name == name {
			return sh, true
		}
	}
	return Ship{}, false
}

func (f *mooredFixture) records(t *testing.T) []moor.Record {
	t.Helper()
	got, err := moor.List(f.home)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// events flushes the log and returns the records of one event for a ship.
func (f *mooredFixture) events(t *testing.T, session, event string) []map[string]any {
	t.Helper()
	f.s.Outcomes.Close()
	files, _ := filepath.Glob(filepath.Join(f.home, "outcomes", "*.jsonl"))
	var out []map[string]any
	for _, file := range files {
		fh, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil && m["ship"] == session && m["event"] == event {
				out = append(out, m)
			}
		}
		fh.Close()
	}
	return out
}

func finalStates(t *testing.T, ev map[string]any) map[string]string {
	t.Helper()
	list, _ := ev["prs"].([]any)
	out := map[string]string{}
	for _, p := range list {
		m, _ := p.(map[string]any)
		out[m["url"].(string)], _ = m["state"].(string)
	}
	return out
}

func TestAMooredShipShowsItsPRsAsOpenUntilTheFirstPollAnswers(t *testing.T) {
	g := &pollGitHub{}
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moor(t, "Nelson", "work", prSeven)
	sh, ok := f.ship(t, "Nelson")
	if !ok || len(sh.PRs) != 1 || sh.PRs[0].URL != prSeven || sh.PRs[0].State != "OPEN" || sh.PRs[0].Review != "" || sh.PRs[0].Checks != "" {
		t.Fatalf("before the first poll = %+v, want the PR with its url and state OPEN only", sh.PRs)
	}
}

func TestPollMooredServesTheChipsOnTheShip(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "OPEN", Review: "CHANGES_REQUESTED", Checks: "FAILURE", Mergeable: "CONFLICTING"})
	g.set(prNine, moor.Status{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE", MergeState: "CLEAN"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moor(t, "Nelson", "work", prSeven, prNine)

	f.s.PollMoored(context.Background())

	sh, ok := f.ship(t, "Nelson")
	if !ok || sh.Status != StatusMoored || len(sh.PRs) != 2 {
		t.Fatalf("ship after a poll = %+v, want still moored with two PRs", sh)
	}
	want := []moor.PR{
		{URL: prSeven, State: "OPEN", Review: "CHANGES_REQUESTED", Checks: "FAILURE", Mergeable: "CONFLICTING", Conflicts: true},
		{URL: prNine, State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE", Ready: true},
	}
	for i, w := range want {
		if sh.PRs[i] != w {
			t.Errorf("PR %d = %+v, want %+v", i, sh.PRs[i], w)
		}
	}
	if _, err := time.Parse(time.RFC3339, sh.PolledAt); err != nil || sh.Stale {
		t.Errorf("polled_at = %q, stale = %v; want an RFC 3339 time and not stale", sh.PolledAt, sh.Stale)
	}
}

func TestPollMooredAsksOnceForEveryShipOfAFleetClient(t *testing.T) {
	work, home := &pollGitHub{}, &pollGitHub{}
	work.set(prSeven, moor.Status{State: "OPEN"})
	work.set(prNine, moor.Status{State: "OPEN"})
	home.set(prOther, moor.Status{State: "OPEN"})
	f := mooredService(t, map[string]*pollGitHub{"work": work, "home": home})
	f.moor(t, "Nelson", "work", prSeven)
	f.moor(t, "Hood", "work", prNine)
	f.moor(t, "Drake", "home", prOther)

	f.s.PollMoored(context.Background())

	if c := work.callLog(); len(c) != 1 || len(c[0]) != 2 {
		t.Errorf("work's client was asked %v, want one call for both ships' PRs", c)
	}
	if c := home.callLog(); len(c) != 1 || len(c[0]) != 1 || c[0][0] != prOther {
		t.Errorf("home's client was asked %v, want one call for its ship's PR", c)
	}
}

func TestPollMooredSailsOffAShipWhoseEveryPRIsDone(t *testing.T) {
	for name, c := range map[string]struct {
		a, b   *moor.Status // nil: GitHub answers NOT_FOUND
		reason string
		states [2]string
	}{
		"every PR merged":             {&moor.Status{State: "MERGED"}, &moor.Status{State: "MERGED"}, "merged", [2]string{"MERGED", "MERGED"}},
		"every PR closed":             {&moor.Status{State: "CLOSED"}, &moor.Status{State: "CLOSED"}, "closed", [2]string{"CLOSED", "CLOSED"}},
		"a closed PR and a NOT_FOUND": {&moor.Status{State: "CLOSED"}, nil, "closed", [2]string{"CLOSED", "CLOSED"}},
		"a merged PR and a closed":    {&moor.Status{State: "MERGED"}, &moor.Status{State: "CLOSED"}, "prs_done", [2]string{"MERGED", "CLOSED"}},
		"a merged PR and a NOT_FOUND": {&moor.Status{State: "MERGED"}, nil, "prs_done", [2]string{"MERGED", "CLOSED"}},
	} {
		t.Run(name, func(t *testing.T) {
			g := &pollGitHub{}
			if c.a != nil {
				g.set(prSeven, *c.a)
			}
			if c.b != nil {
				g.set(prNine, *c.b)
			}
			f := mooredService(t, map[string]*pollGitHub{"work": g})
			session := f.moor(t, "Nelson", "work", prSeven, prNine)

			f.s.PollMoored(context.Background())

			if _, ok := f.ship(t, "Nelson"); ok {
				t.Errorf("the ship is still listed")
			}
			if recs := f.records(t); len(recs) != 0 {
				t.Errorf("records = %v, want none: the name is free", recs)
			}
			evs := f.events(t, session, "unmoored")
			if len(evs) != 1 {
				t.Fatalf("unmoored events = %v, want exactly one", evs)
			}
			if evs[0]["reason"] != c.reason {
				t.Errorf("reason = %v, want %s", evs[0]["reason"], c.reason)
			}
			got := finalStates(t, evs[0])
			if got[prSeven] != c.states[0] || got[prNine] != c.states[1] {
				t.Errorf("the event's PR states = %v, want %v", got, c.states)
			}
			if n := len(f.events(t, session, "moored")); n != 0 {
				t.Errorf("ending wrote a second moored event: %d", n)
			}
		})
	}
}

func TestPollMooredKeepsAShipWithAnOpenPR(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "MERGED"})
	g.set(prNine, moor.Status{State: "OPEN", Review: "APPROVED"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven, prNine)

	f.s.PollMoored(context.Background())

	sh, ok := f.ship(t, "Nelson")
	if !ok || sh.Status != StatusMoored || sh.PRs[0].State != "MERGED" || sh.PRs[1].State != "OPEN" {
		t.Errorf("ship = %+v, want moored with one merged and one open PR", sh)
	}
	if len(f.records(t)) != 1 || len(f.events(t, session, "unmoored")) != 0 {
		t.Errorf("a ship with an open PR ended")
	}
}

func TestATransportFailureKeepsTheChipsAndMarksThePollStale(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)
	f.s.PollMoored(context.Background())

	g.setErr(errors.New("gh: HTTP 502"))
	f.s.PollMoored(context.Background())

	sh, ok := f.ship(t, "Nelson")
	if !ok || sh.Status != StatusMoored {
		t.Fatalf("a failing gh ended the ship: %+v", sh)
	}
	if sh.PRs[0].Review != "APPROVED" || sh.PRs[0].Checks != "SUCCESS" {
		t.Errorf("chips after the failed poll = %+v, want the last known ones", sh.PRs[0])
	}
	if !sh.Stale {
		t.Errorf("stale = false after a failed poll")
	}
	if _, err := time.Parse(time.RFC3339, sh.PolledAt); err != nil {
		t.Errorf("polled_at = %q, want the time it was last polled: %v", sh.PolledAt, err)
	}
	if len(f.records(t)) != 1 || len(f.events(t, session, "unmoored")) != 0 {
		t.Errorf("a transport failure ended mooring")
	}

	g.setErr(nil)
	f.s.PollMoored(context.Background())
	if sh, _ := f.ship(t, "Nelson"); sh.Stale {
		t.Errorf("stale stays true after a poll that answered")
	}
}

func TestAFailedFirstPollNeverEndsMooring(t *testing.T) {
	g := &pollGitHub{err: errors.New("gh: HTTP 502")}
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moor(t, "Nelson", "work", prSeven)
	f.s.PollMoored(context.Background())
	sh, ok := f.ship(t, "Nelson")
	if !ok || sh.PRs[0].State != "OPEN" || !sh.Stale {
		t.Errorf("ship = %+v, want it moored, its PR still OPEN and the poll stale", sh)
	}
}

func TestRefreshAnswers202AtOnceAndKicksesNeverOverlap(t *testing.T) {
	g := &pollGitHub{block: make(chan struct{})}
	g.set(prSeven, moor.Status{State: "OPEN"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moor(t, "Nelson", "work", prSeven)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.s.RunMoored(ctx); close(done) }()
	t.Cleanup(func() { stop(); <-done })

	post := func() int {
		got := make(chan int, 1)
		go func() { got <- f.post("/api/moored/refresh").Code }()
		select {
		case c := <-got:
			return c
		case <-time.After(3 * time.Second):
			t.Fatal("POST /api/moored/refresh did not answer while a poll was running")
			return 0
		}
	}
	if c := post(); c != http.StatusAccepted {
		t.Fatalf("POST /api/moored/refresh = %d, want 202", c)
	}
	waitUntil(t, "a poll is running", func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.active == 1 })
	for range 4 {
		if c := post(); c != http.StatusAccepted {
			t.Errorf("a kick during a poll = %d, want 202", c)
		}
	}
	close(g.block)
	waitUntil(t, "the polls finish", func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.active == 0 && len(g.calls) >= 1 })
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.most != 1 {
		t.Errorf("%d polls ran at once, want one", g.most)
	}
	if len(g.calls) > 2 {
		t.Errorf("5 kicks made %d polls, want them coalesced into at most 2", len(g.calls))
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for: %s", what)
		}
	}
}

func TestDismissEndsAMooredShip(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "OPEN"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)

	rec := f.post("/api/ships/" + session + "/dismiss")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), session) {
		t.Fatalf("Dismiss on a moored ship = %d %s, want 200 naming the ship", rec.Code, rec.Body)
	}
	if _, ok := f.ship(t, "Nelson"); ok {
		t.Errorf("the dismissed ship is still listed")
	}
	if len(f.records(t)) != 0 {
		t.Errorf("Dismiss left the record")
	}
	evs := f.events(t, session, "unmoored")
	if len(evs) != 1 || evs[0]["reason"] != "dismissed" || finalStates(t, evs[0])[prSeven] != "OPEN" {
		t.Errorf("unmoored events = %v, want one with reason dismissed and the PR still OPEN", evs)
	}
	if again := f.post("/api/ships/" + session + "/dismiss"); again.Code != http.StatusNotFound {
		t.Errorf("a second Dismiss = %d, want 404: the ship is gone", again.Code)
	}
}

func TestUnfollowEndsAMooredShip(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "OPEN"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)

	rec := f.post("/api/ships/" + session + "/unfollow")
	if rec.Code != http.StatusOK {
		t.Fatalf("Don't follow reviews = %d %s, want 200", rec.Code, rec.Body)
	}
	if _, ok := f.ship(t, "Nelson"); ok || len(f.records(t)) != 0 {
		t.Errorf("the ship or its record is still there")
	}
	evs := f.events(t, session, "unmoored")
	if len(evs) != 1 || evs[0]["reason"] != "unfollowed" {
		t.Errorf("unmoored events = %v, want one with reason unfollowed", evs)
	}
}

func TestUnfollowRefusesAShipThatIsNotMoored(t *testing.T) {
	f := mooredService(t, nil)
	if rec := f.post("/api/ships/nobody/unfollow"); rec.Code != http.StatusNotFound {
		t.Errorf("Don't follow reviews on an unknown ship = %d, want 404", rec.Code)
	}
}

// A live ship is refused by Dismiss unless it is anchored; Don't follow reviews refuses it the same way.
func TestUnfollowRefusesALiveShipAsDismissDoes(t *testing.T) {
	s, _ := tailService(t)
	s.Launcher = &launcher.Launcher{Home: t.TempDir(), Config: func() *config.Config { return &config.Config{} }}
	mux := http.NewServeMux()
	s.Routes(mux)
	post := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
		return rec
	}
	dismiss, unfollow := post("/api/ships/alpha/dismiss"), post("/api/ships/alpha/unfollow")
	if dismiss.Code != http.StatusConflict {
		t.Fatalf("Dismiss on a live ship = %d, want 409", dismiss.Code)
	}
	if unfollow.Code != dismiss.Code {
		t.Errorf("Don't follow reviews on a live ship = %d %s, want the refusal Dismiss gives (%d)", unfollow.Code, unfollow.Body, dismiss.Code)
	}
}

func TestTwoEndingsOfOneShipWriteOneEventAndRemoveTheRecordOnce(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "MERGED"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)

	var wg sync.WaitGroup
	codes := make(chan int, 4)
	wg.Add(4)
	go func() { defer wg.Done(); f.s.PollMoored(context.Background()); codes <- 0 }()
	for _, p := range []string{"dismiss", "unfollow", "dismiss"} {
		go func() { defer wg.Done(); codes <- f.post("/api/ships/" + session + "/" + p).Code }()
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c >= 500 {
			t.Errorf("an ending that lost the race answered %d", c)
		}
	}
	if evs := f.events(t, session, "unmoored"); len(evs) != 1 {
		t.Errorf("unmoored events = %v, want exactly one", evs)
	}
	if len(f.records(t)) != 0 {
		t.Errorf("record not removed")
	}
	if _, ok := f.ship(t, "Nelson"); ok {
		t.Errorf("the ship is still listed")
	}
}

func TestAnUnansweredPRKeepsItsShipMooredAndStaleWhileTheFleetsOthersAreAnswered(t *testing.T) {
	g := &pollGitHub{un: []string{prSeven}}
	g.set(prNine, moor.Status{State: "MERGED"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moor(t, "Nelson", "work", prSeven)
	f.moor(t, "Hood", "work", prNine)

	f.s.PollMoored(context.Background())

	sh, ok := f.ship(t, "Nelson")
	if !ok || sh.Status != StatusMoored || !sh.Stale || sh.PRs[0].State != "OPEN" {
		t.Errorf("the ship with an unanswered PR = %+v, want moored, stale and its PR still OPEN", sh)
	}
	if _, ok := f.ship(t, "Hood"); ok {
		t.Errorf("the ship whose PR was answered merged is still listed")
	}
}

func TestAPollThatAnswersAfterItsShipEndedSaysNothingOfTheNextShipOfTheSession(t *testing.T) {
	g := &pollGitHub{block: make(chan struct{})}
	g.set(prSeven, moor.Status{State: "MERGED"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)
	f.s.List() // loads the records

	polled := make(chan struct{})
	go func() { f.s.PollMoored(context.Background()); close(polled) }()
	waitUntil(t, "the poll is asking", func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.active == 1 })
	if rec := f.post("/api/ships/" + session + "/dismiss"); rec.Code != http.StatusOK {
		t.Fatalf("Dismiss = %d", rec.Code)
	}
	f.s.setMoored(moor.Record{Name: "Nelson", Session: session, Fleet: "work", PRs: []string{prSeven}, MooredAt: time.Now().Add(time.Minute).UTC().Truncate(time.Second)})
	close(g.block)
	<-polled

	sh, ok := f.ship(t, "Nelson")
	if !ok || sh.Status != StatusMoored || sh.PRs[0].State != "OPEN" || sh.PolledAt != "" {
		t.Errorf("the next ship of the session = %+v, want moored with its PR OPEN and no poll", sh)
	}
}

func TestAMooredRecordThatCannotBeRemovedAnswers500AndStaysMoored(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "OPEN"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)
	f.s.List()
	dir := filepath.Join(f.home, "moored")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if os.Remove(filepath.Join(dir, "no-such")) == nil || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop this user")
	}
	for _, path := range []string{"dismiss", "unfollow"} {
		if rec := f.post("/api/ships/" + session + "/" + path); rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "its moored record cannot be removed") || strings.Contains(rec.Body.String(), f.home) {
			t.Errorf("%s with a record that stays = %d %s, want 500 without the path", path, rec.Code, rec.Body)
		}
	}
	if sh, ok := f.ship(t, "Nelson"); !ok || sh.Status != StatusMoored {
		t.Errorf("the ship = %+v, want it kept for the next poll", sh)
	}
}

// u1 writes the record before it kills the session, so for a moment a record's session is still live.
func TestARecordWhoseSessionIsStillLiveIsNotMoored(t *testing.T) {
	s, _ := tailService(t)
	home := t.TempDir()
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work"}}}
	s.Launcher = &launcher.Launcher{Home: home, Config: func() *config.Config { return cfg }}
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "MERGED"})
	s.GitHub = func(string) moor.GitHub { return g }
	s.Hub, s.Outcomes = server.NewHub(), nil
	if err := moor.Write(home, moor.Record{Name: "alpha", Session: "alpha", Fleet: "work", PRs: []string{prSeven}, MooredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Routes(mux)

	s.PollMoored(context.Background())
	for _, path := range []string{"dismiss", "unfollow"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/alpha/"+path, nil))
		if rec.Code != http.StatusConflict {
			t.Errorf("%s on a live ship with a record = %d, want the 409 a live ship gets", path, rec.Code)
		}
	}
	if n := len(g.callLog()); n != 0 {
		t.Errorf("the poll asked GitHub %d times about a ship that is not moored", n)
	}
	if recs, _ := moor.List(home); len(recs) != 1 {
		t.Errorf("records = %v, want the record kept", recs)
	}
}

func TestSailingOffAMooredSliceShipReleasesEveryIssue(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, moor.Status{State: "MERGED"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	if err := moor.Write(f.home, moor.Record{Name: "Slice", Session: "slice", Fleet: "work", Harness: "claude", Checkout: "/src/widgets",
		URL: sliceA, Issues: []string{sliceA, sliceB}, PRs: []string{prSeven}, MooredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	ka, _ := ClaimKey(sliceA)
	kb, _ := ClaimKey(sliceB)
	if c := f.s.Claims(); c[ka] != "Slice" || c[kb] != "Slice" {
		t.Fatalf("Claims = %v, want both issues held while the slice ship is moored", c)
	}

	f.s.PollMoored(context.Background())

	if _, ok := f.ship(t, "Slice"); ok {
		t.Fatalf("the ship is still listed")
	}
	if c := f.s.Claims(); c[ka] != "" || c[kb] != "" {
		t.Errorf("Claims = %v, want neither issue held once the slice ship sailed off", c)
	}
}

const prEleven = "https://github.com/acme/widgets/pull/11"

func readyStatus() moor.Status {
	return moor.Status{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE", MergeState: "CLEAN"}
}

func TestAMooredStackServesWhetherItIsReadyAndUpToWhichLayer(t *testing.T) {
	notReady := readyStatus()
	notReady.Checks = "PENDING"
	for name, c := range map[string]struct {
		layers [3]moor.Status
		ready  bool
		upTo   string
	}{
		"every layer ready":     {[3]moor.Status{readyStatus(), readyStatus(), readyStatus()}, true, prEleven},
		"the top layer pending": {[3]moor.Status{readyStatus(), readyStatus(), notReady}, false, prNine},
		"the lowest not ready":  {[3]moor.Status{notReady, readyStatus(), readyStatus()}, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			g := &pollGitHub{}
			for i, u := range []string{prSeven, prNine, prEleven} {
				g.set(u, c.layers[i])
			}
			f := mooredService(t, map[string]*pollGitHub{"work": g})
			f.moor(t, "Nelson", "work", prSeven, prNine, prEleven)

			f.s.PollMoored(context.Background())

			sh, ok := f.ship(t, "Nelson")
			if !ok || sh.StackReady != c.ready || sh.ReadyUpTo != c.upTo {
				t.Errorf("ship = ready %v, up to %q; want ready %v, up to %q", sh.StackReady, sh.ReadyUpTo, c.ready, c.upTo)
			}
		})
	}
}

func TestAMergeThatRunsPastItsLimitAnswers504AndAsksForAPoll(t *testing.T) {
	g := &pollGitHub{mergeErr: moor.ErrMergeTimeout}
	g.set(prSeven, moor.Status{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE", MergeState: "CLEAN"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	session := f.moor(t, "Nelson", "work", prSeven)

	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/"+session+"/merge", strings.NewReader("{}")))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("POST merge = %d %s, want 504", rec.Code, rec.Body)
	}
	select {
	case <-f.s.kickChan():
	default:
		t.Errorf("no poll was asked for after a merge whose result is unknown")
	}
}
