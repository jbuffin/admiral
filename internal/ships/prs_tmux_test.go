package ships

// A running ship carries the PRs it opened; the poll finds them, a wake keeps the old ones and the exit moors them.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
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

func TestTheOneBatchedQueryOfAFleetAsksForRunningAndMooredShipsTogether(t *testing.T) {
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
	if len(calls) != 1 || !slices.Contains(calls[0], prSeven) || !slices.Contains(calls[0], prNine) {
		t.Errorf("PullRequests calls = %v, want one call holding the running ship's candidate and the moored ship's PR", calls)
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

// reportPath is where the harness of start n was told to write its report.
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
	}{
		{"no report moors the ship with the PR it carries", map[string]string{prSeven: "OPEN"}, "", []string{prSeven}},
		{"a report that leaves out a carried PR does not remove it", map[string]string{prSeven: "OPEN"}, `["` + prNine + `"]`, []string{prSeven, prNine}},
		{"a PR in both the report and the carried ones is stored once", map[string]string{prSeven: "OPEN"}, `["` + prSeven + `","` + prNine + `"]`, []string{prSeven, prNine}},
		{"a carried PR that merged is dropped, an open one stays", map[string]string{prSeven: "OPEN", prNine: "MERGED"}, "", []string{prSeven}},
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
				if err := os.WriteFile(reportPath(env), []byte(`{"version":1,"result":"done","why":"w","prs":`+c.report+`}`), 0o600); err != nil {
					t.Fatal(err)
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
