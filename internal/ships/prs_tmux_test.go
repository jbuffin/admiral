package ships

// A running ship carries the PRs it opened; the poll finds them, a wake keeps the old ones and the exit moors them.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

const viewer = "fake-viewer"

// prRig is a running ship, saltpaw, whose transcript candidates the test sets and whose GitHub answers the poll.
type prRig struct {
	*nextRig
	gh         *pollGitHub
	launched   time.Time
	candidates []string
}

func newPRRig(t *testing.T) *prRig {
	t.Helper()
	r := &prRig{nextRig: newNextRig(t)}
	r.gh = r.polling()
	r.s.Candidates = func(session string) []string {
		if session == "saltpaw" {
			return r.candidates
		}
		return nil
	}
	l, err := time.Parse(time.RFC3339, r.options(t, "saltpaw")["launched"])
	if err != nil {
		t.Fatalf("the ship's launched option: %v", err)
	}
	r.launched = l
	return r
}

// mine answers a PR the fleet's gh user opened at the offset from the ship's launch.
func (r *prRig) mine(url, state string, after time.Duration) {
	r.gh.set(url, moor.Status{State: state, Author: viewer, Viewer: viewer, CreatedAt: r.launched.Add(after)})
}

func (r *prRig) ship(t *testing.T) Ship {
	t.Helper()
	for _, sh := range r.s.List() {
		if sh.Name == "Saltpaw" {
			return sh
		}
	}
	t.Fatal("no ship Saltpaw")
	return Ship{}
}

func prURLs(prs []moor.PR) []string {
	var out []string
	for _, p := range prs {
		out = append(out, p.URL)
	}
	return out
}

func TestARunningShipCarriesAPRTheFleetsUserOpenedAfterItLaunched(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{prSeven}
	r.mine(prSeven, "OPEN", time.Minute)

	if sh := r.ship(t); len(sh.PRs) != 0 || sh.CarriesPRs {
		t.Fatalf("before the poll: prs %v, carries_prs %v; want none", prURLs(sh.PRs), sh.CarriesPRs)
	}
	r.s.PollMoored(context.Background())

	sh := r.ship(t)
	if got := prURLs(sh.PRs); !slices.Equal(got, []string{prSeven}) || sh.PRs[0].State != "OPEN" || !sh.CarriesPRs || sh.Stale {
		t.Errorf("after one poll: prs %v, carries_prs %v, stale %v; want [%s] open, carried, not stale", got, sh.CarriesPRs, sh.Stale, prSeven)
	}
	if _, err := time.Parse(time.RFC3339, sh.PolledAt); err != nil {
		t.Errorf("polled_at = %q, want an RFC 3339 time", sh.PolledAt)
	}

	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships", nil))
	var body struct {
		Ships []map[string]any `json:"ships"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Ships) != 1 || body.Ships[0]["carries_prs"] != true || body.Ships[0]["prs"] == nil {
		t.Errorf("GET /api/ships = %s, want the ship with prs and carries_prs true", rec.Body)
	}
	wantError(t, r.returnToPort("saltpaw"), http.StatusConflict, "a ship carrying an open PR it opened itself")
}

func TestARunningShipDoesNotCarryAPRItDidNotOpenSinceItLaunched(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{prSeven, prNine, prOther, wakePR}
	r.mine(prSeven, "OPEN", time.Minute)
	r.gh.set(prNine, moor.Status{State: "OPEN", Author: "someone-else", Viewer: viewer, CreatedAt: r.launched.Add(time.Minute)}) // someone else's
	r.mine(prOther, "OPEN", -time.Hour)                                                                                          // opened before this launch
	// wakePR has no answer: GitHub does not know it.

	r.s.PollMoored(context.Background())

	if got := prURLs(r.ship(t).PRs); !slices.Equal(got, []string{prSeven}) {
		t.Errorf("prs = %v, want only %s: another author's, an earlier one and an unknown one stay out", got, prSeven)
	}
}

func TestAFleetsPollAsksForRunningAndMooredShipsTogetherAndFreshCandidatesApart(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{prSeven}
	r.mine(prSeven, "OPEN", time.Minute)
	r.gh.set(prNine, moor.Status{State: "OPEN"})
	if err := moor.Write(r.home, moor.Record{Name: "Hood", Session: "hood", Fleet: "work", Harness: "claude", Checkout: r.checkout,
		PRs: []string{prNine}, MooredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	r.s.mooredMu.Lock()
	r.s.mooredLoaded = false
	r.s.mooredMu.Unlock()

	r.s.PollMoored(context.Background())

	calls := r.gh.callLog()
	if len(calls) != 2 || !slices.Equal(calls[0], []string{prSeven}) || !slices.Equal(calls[1], []string{prNine}) {
		t.Errorf("PullRequests calls = %v, want the fresh candidate in one call and the moored PR in another", calls)
	}
	r.candidates = nil
	r.s.PollMoored(context.Background())
	if calls := r.gh.callLog(); len(calls) != 3 || !slices.Contains(calls[2], prSeven) || !slices.Contains(calls[2], prNine) {
		t.Errorf("PullRequests calls = %v, want a later poll to ask for the carried and moored PRs together", calls)
	}
}

func TestAFailedPollKeepsWhatARunningShipCarriesAndMarksItStale(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{prSeven}
	r.mine(prSeven, "OPEN", time.Minute)
	r.s.PollMoored(context.Background())
	first := r.ship(t)
	if len(first.PRs) != 1 {
		t.Fatalf("the first poll left prs %v, want one", prURLs(first.PRs))
	}

	r.gh.setErr(context.DeadlineExceeded)
	r.s.PollMoored(context.Background())

	sh := r.ship(t)
	if got := prURLs(sh.PRs); !slices.Equal(got, []string{prSeven}) || !sh.Stale || sh.PolledAt == "" || !sh.CarriesPRs {
		t.Errorf("after a failed poll: prs %v, stale %v, polled_at %q, carries_prs %v; want the PR kept and marked stale", got, sh.Stale, sh.PolledAt, sh.CarriesPRs)
	}
}

func TestAFleetWithNoGitHubClientIsSkippedWhileAnotherFleetsShipsAreAnswered(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{prSeven}
	r.mine(prSeven, "OPEN", time.Minute)
	r.s.GitHub = func(fleet string) moor.GitHub {
		if fleet == "work" {
			return r.gh
		}
		return nil
	}
	if err := moor.Write(r.home, moor.Record{Name: "Drake", Session: "drake", Fleet: "home", Harness: "claude", Checkout: r.checkout,
		PRs: []string{prOther}, MooredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	r.s.mooredMu.Lock()
	r.s.mooredLoaded = false
	r.s.mooredMu.Unlock()

	r.s.PollMoored(context.Background())

	if got := prURLs(r.ship(t).PRs); !slices.Equal(got, []string{prSeven}) {
		t.Errorf("the work ship's prs = %v, want [%s]", got, prSeven)
	}
	if calls := r.gh.callLog(); len(calls) != 1 || slices.Contains(calls[0], prOther) {
		t.Errorf("work's client was asked %v, want one call that leaves out the home fleet's PR", calls)
	}
}

func TestAWokenShipShowsItsMooredPRsAtOnceAndKeepsThemBesideTheNewOnes(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0) // the record holds wakePR
	g := r.polling()
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}
	r.started(t, 1)
	r.s.Candidates = func(string) []string { return []string{prNine} }
	var woken Ship
	for _, sh := range r.s.List() {
		if sh.Name == "Saltpaw" {
			woken = sh
		}
	}
	if got := prURLs(woken.PRs); !slices.Equal(got, []string{wakePR}) || !woken.CarriesPRs {
		t.Fatalf("before any poll: prs %v, carries_prs %v; want the moored record's [%s], carried", got, woken.CarriesPRs, wakePR)
	}

	launched, err := time.Parse(time.RFC3339, r.options(t, "saltpaw")["launched"])
	if err != nil {
		t.Fatal(err)
	}
	// The old PR was opened before this launch, yet it stays: the record confirmed it. The new one must pass the launch-time rule.
	g.set(wakePR, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer, CreatedAt: launched.Add(-48 * time.Hour)})
	g.set(prNine, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer, CreatedAt: launched.Add(time.Minute)})
	g.set(prOther, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer, CreatedAt: launched.Add(-time.Hour)})
	r.s.Candidates = func(string) []string { return []string{prNine, prOther} }

	r.s.PollMoored(context.Background())

	for _, sh := range r.s.List() {
		if sh.Name == "Saltpaw" {
			woken = sh
		}
	}
	if got := prURLs(woken.PRs); !slices.Equal(got, []string{wakePR, prNine}) {
		t.Errorf("after a poll: prs %v, want the old %s and the new %s, and not %s, which this launch did not open", got, wakePR, prNine, prOther)
	}
}

// reportPath is where the harness was told to write its report.
func reportPath(env string) string {
	for _, l := range strings.Split(env, "\n") {
		if v, ok := strings.CutPrefix(l, "ADMIRAL_REPORT="); ok {
			return v
		}
	}
	return ""
}

func TestACleanExitMoorsTheShipWithItsCarriedPRsMergedWithTheReports(t *testing.T) {
	for _, c := range []struct {
		name    string
		carried map[string]string // url to state
		report  string            // the report's prs, or empty for no report
		want    []string
		merged  bool // GitHub says the carried PRs merged after the poll
	}{
		{"no report moors the ship with the PR it carries", map[string]string{prSeven: "OPEN"}, "", []string{prSeven}, false},
		{"a report that leaves out a carried PR does not remove it", map[string]string{prSeven: "OPEN"}, `[{"url":"` + prNine + `"}]`, []string{prSeven, prNine}, false},
		{"a PR in both the report and the carried ones is stored once", map[string]string{prSeven: "OPEN"}, `[{"url":"` + prSeven + `"},{"url":"` + prNine + `"}]`, []string{prSeven, prNine}, false},
		{"a carried PR that merged is dropped, an open one stays", map[string]string{prSeven: "OPEN", prNine: "MERGED"}, "", []string{prSeven}, false},
		{"a carried PR that merged since the poll is dropped and the ship sails off", map[string]string{prSeven: "OPEN"}, "", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newPRRig(t)
			r.gh.mu.Lock()
			r.gh.exitCheck = true
			r.gh.mu.Unlock()
			for u, state := range c.carried {
				r.candidates = append(r.candidates, u)
				r.mine(u, state, time.Minute)
			}
			slices.Sort(r.candidates)
			r.gh.set(prNine, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer, CreatedAt: r.launched.Add(time.Minute)})
			if _, carried := c.carried[prNine]; carried {
				r.mine(prNine, c.carried[prNine], time.Minute)
			}
			r.s.PollMoored(context.Background())
			if len(r.ship(t).PRs) != len(c.carried) {
				t.Fatalf("the poll left prs %v, want the %d carried", prURLs(r.ship(t).PRs), len(c.carried))
			}
			if c.report != "" {
				_, env := r.started(t, 1)
				if err := os.WriteFile(reportPath(env), []byte(`{"version":2,"result":"done","why":"w","prs":`+c.report+`}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if c.merged {
				for u := range c.carried {
					r.mine(u, "MERGED", time.Minute)
				}
			}
			r.exit(t, "0")
			waitUntil(t, "the ship to leave its pane", func() bool {
				_ = r.s.Host.Sync(context.Background())
				for _, sess := range r.s.Host.Sessions() {
					if sess.Name == "saltpaw" {
						return false
					}
				}
				return true
			})
			recs, err := moor.List(r.home)
			if c.want == nil {
				if err != nil || len(recs) != 0 {
					t.Fatalf("moored records = %+v, %v; want the ship to sail off", recs, err)
				}
				return
			}
			if err != nil || len(recs) != 1 {
				t.Fatalf("moored records = %+v, %v; want the ship moored", recs, err)
			}
			got := slices.Clone(recs[0].PRs)
			slices.Sort(got)
			want := slices.Clone(c.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("moored PRs = %v, want %v", recs[0].PRs, c.want)
			}
		})
	}
}

func TestACandidateNotOnTheFleetsHostOrWithAnUnusableNumberIsNeverAsked(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{
		"https://ghe.example.com/acme/widgets/pull/7",
		"https://github.com/acme/widgets/pull/07",
		"https://github.com/acme/widgets/pull/2147483648",
		"https://github.com/acme/widgets/pull/99999999999",
		"https://GitHub.com/acme/widgets/pull/9",
	}
	r.mine(prNine, "OPEN", time.Minute)

	r.s.PollMoored(context.Background())

	if calls := r.gh.callLog(); len(calls) != 1 || !slices.Equal(calls[0], []string{prNine}) {
		t.Errorf("PullRequests calls = %v, want one call for %s alone: the other host's URL, the zero-led and the oversized numbers stay out", calls, prNine)
	}
	if got := prURLs(r.ship(t).PRs); !slices.Equal(got, []string{prNine}) {
		t.Errorf("prs = %v, want [%s], rebuilt on the client's host", got, prNine)
	}
}

func TestAtMostTwentyFreshCandidatesAreAskedInOnePollAndTheRestWaitForTheNext(t *testing.T) {
	r := newPRRig(t)
	var urls []string
	for i := 1; i <= 25; i++ {
		u := "https://github.com/acme/widgets/pull/" + strconv.Itoa(100+i)
		urls = append(urls, u)
		r.mine(u, "OPEN", time.Minute)
	}
	r.candidates = urls

	r.s.PollMoored(context.Background())
	if calls := r.gh.callLog(); len(calls) != 1 || len(calls[0]) != 20 {
		t.Fatalf("PullRequests calls = %v, want one call of 20", calls)
	}
	r.s.PollMoored(context.Background())
	if got := len(r.ship(t).PRs); got != 25 {
		t.Errorf("after two polls the ship carries %d PRs, want all 25", got)
	}
}

func TestACandidateInARepositoryGitHubCannotFindDoesNotStaleTheShipOrHideItsRealPR(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{prOther, prSeven}
	r.mine(prSeven, "OPEN", time.Minute)
	r.gh.mu.Lock()
	r.gh.un = []string{prOther}
	r.gh.mu.Unlock()

	for range maxCandidateTries {
		r.s.PollMoored(context.Background())
	}

	sh := r.ship(t)
	if got := prURLs(sh.PRs); !slices.Equal(got, []string{prSeven}) || sh.Stale || !sh.CarriesPRs {
		t.Errorf("prs %v, stale %v, carries_prs %v; want [%s] carried and not stale", got, sh.Stale, sh.CarriesPRs, prSeven)
	}
	n := len(r.gh.callLog())
	r.s.PollMoored(context.Background())
	for _, c := range r.gh.callLog()[n:] {
		if slices.Contains(c, prOther) {
			t.Errorf("a later call %v still asks for the repository GitHub cannot find", c)
		}
	}
}

func TestACandidateQueryThatFailsOutrightLeavesMooredShipsAnsweredAndIsDroppedAfterThreeTries(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{prSeven}
	r.gh.mu.Lock()
	r.gh.failOn = prSeven
	r.gh.mu.Unlock()
	r.gh.set(prNine, moor.Status{State: "OPEN"})
	if err := moor.Write(r.home, moor.Record{Name: "Hood", Session: "hood", Fleet: "work", Harness: "claude", Checkout: r.checkout,
		PRs: []string{prNine}, MooredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	r.s.mooredMu.Lock()
	r.s.mooredLoaded = false
	r.s.mooredMu.Unlock()

	for range maxCandidateTries {
		r.s.PollMoored(context.Background())
	}

	var hood Ship
	for _, sh := range r.s.List() {
		if sh.Name == "Hood" {
			hood = sh
		}
	}
	if hood.Name == "" || hood.Stale || hood.PolledAt == "" {
		t.Errorf("the moored ship = %+v, want it answered and not stale beside a failing candidate query", hood)
	}
	if sh := r.ship(t); sh.Stale {
		t.Errorf("the running ship is stale, want only a carried PR to stale it")
	}
	n := len(r.gh.callLog())
	r.s.PollMoored(context.Background())
	for _, c := range r.gh.callLog()[n:] {
		if slices.Contains(c, prSeven) {
			t.Errorf("a call %v asks for the candidate that failed %d times", c, maxCandidateTries)
		}
	}
}

func TestACarriedPRSurvivesAnAdmiralRestartWithNoTranscriptToReadItFrom(t *testing.T) {
	r := newPRRig(t)
	r.candidates = []string{prSeven}
	r.mine(prSeven, "OPEN", time.Minute)
	r.s.PollMoored(context.Background())

	r.s.mooredMu.Lock()
	r.s.running, r.s.mooredPolls = nil, nil // a new process knows nothing of the launch
	r.s.mooredMu.Unlock()
	r.s.Candidates = func(string) []string { return nil }
	waitUntil(t, "the carried PR to be on the session", func() bool {
		_ = r.s.Host.Sync(context.Background())
		return r.options(t, "saltpaw")["prs"] == prSeven
	})

	sh := r.ship(t)
	if got := prURLs(sh.PRs); !slices.Equal(got, []string{prSeven}) || !sh.CarriesPRs {
		t.Errorf("after the restart: prs %v, carries_prs %v; want [%s] carried", got, sh.CarriesPRs, prSeven)
	}
	wantError(t, r.returnToPort("saltpaw"), http.StatusConflict, "a ship carrying an open PR it opened itself")
}

func TestAWokenShipWhoseMooredPollSawItsPRMergedShowsItMergedBeforeAnyPoll(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0)
	r.s.mooredMu.Lock()
	r.s.polls()["saltpaw"] = polled{status: map[string]moor.Status{wakePR: {State: "MERGED"}}, at: time.Now().UTC()}
	r.s.mooredMu.Unlock()
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}
	r.started(t, 1)

	for _, sh := range r.s.List() {
		if sh.Name == "Saltpaw" {
			if len(sh.PRs) != 1 || sh.PRs[0].State != "MERGED" || sh.CarriesPRs {
				t.Errorf("the woken ship: prs %+v, carries_prs %v; want [%s] MERGED and not carried", sh.PRs, sh.CarriesPRs, wakePR)
			}
			return
		}
	}
	t.Fatal("no ship Saltpaw")
}

func TestMoorOnAShipWhoseOnlyOpenPRIsACarriedOneMoorsItWithThatPR(t *testing.T) {
	r := newPRRig(t)
	r.gh.mu.Lock()
	r.gh.exitCheck = true
	r.gh.mu.Unlock()
	r.candidates = []string{prSeven}
	r.mine(prSeven, "OPEN", time.Minute)
	r.s.PollMoored(context.Background())
	if got := r.s.openPRsOf("saltpaw"); len(got) != 0 {
		t.Fatalf("the report check cached %v for a ship with no report", got)
	}
	if sh := r.ship(t); !slices.Equal(sh.OpenPRs, []string{prSeven}) {
		t.Fatalf("open_prs = %v, want the carried [%s]", sh.OpenPRs, prSeven)
	}

	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/saltpaw/return", strings.NewReader(`{"moor":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("return with moor = %d %s, want 200", rec.Code, rec.Body)
	}
	recs, err := moor.List(r.home)
	if err != nil || len(recs) != 1 || !slices.Equal(recs[0].PRs, []string{prSeven}) {
		t.Fatalf("moored records = %+v, %v; want one with [%s]", recs, err, prSeven)
	}
}
