package ships

// Moor counts a live ship's carried PRs that no poll has answered yet.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// carry writes the ship's carried PR the way Admiral does, at session scope, and waits for tmux to show it.
func (r *liveRig) carry(t *testing.T, url string) {
	t.Helper()
	if err := r.s.Host.SetOptions(context.Background(), "saltpaw", map[string]string{"prs": url}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the carried PR to be on the session", func() bool {
		_ = r.s.Host.Sync(context.Background())
		return r.options(t, "saltpaw")["prs"] == url
	})
}

func (r *liveRig) wantMooredWith(t *testing.T, url string) {
	t.Helper()
	r.waitMoored(t)
	moored := outcomeLines(t, r.home, "saltpaw", "moored")
	if len(moored) != 1 {
		t.Fatalf("moored records = %v, want one", moored)
	}
	if prs, _ := moored[0]["prs"].([]any); len(prs) != 1 || prs[0] != url {
		t.Errorf("moored prs = %v, want [%s]", moored[0]["prs"], url)
	}
}

func TestMoorMoorsAShipWithNoReportForTheOpenPRItCarriesBeforeAnyPoll(t *testing.T) {
	t.Parallel()
	for _, woken := range []bool{false, true} {
		for _, route := range []string{"stop", "return"} {
			name := route
			if woken {
				name += " woken"
			}
			t.Run(name, func(t *testing.T) {
				r := newLiveRig(t, woken)
				if !woken {
					r.carry(t, livePR7)
				}
				rec := r.post(route, `{"moor":true}`)
				if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"moored":"saltpaw"}` {
					t.Fatalf("%s with moor = %d %s, want 200 {\"moored\":\"saltpaw\"}", route, rec.Code, rec.Body)
				}
				r.wantMooredWith(t, livePR7)
			})
		}
	}
}

func TestMoorMoorsACarriedPRAfterAnAdmiralRestartBeforeTheFirstPoll(t *testing.T) {
	t.Parallel()
	r := newLiveRig(t, false)
	r.carry(t, livePR7)
	r.gh.pollGitHub.set(livePR7, moor.Status{State: "OPEN"})
	r.s.PollMoored(context.Background())

	r.s.mooredMu.Lock()
	r.s.running, r.s.mooredPolls = nil, nil // a new process knows nothing of the launch
	r.s.mooredMu.Unlock()

	rec := r.post("stop", `{"moor":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop with moor after a restart = %d %s, want 200", rec.Code, rec.Body)
	}
	r.wantMooredWith(t, livePR7)
}

func TestMoorRefusesAShipWhoseCarriedPRIsNotOpenOrNotConfirmedAndLeavesItAlone(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		setup func(r *liveRig)
		want  string
	}{
		{"a carried PR GitHub says is closed", func(r *liveRig) { r.gh.set(map[string]bool{}, nil) },
			"ship saltpaw has no open PR to moor"},
		{"GitHub failing past the retry window", func(r *liveRig) {
			r.gh.set(nil, errors.New("gh is down"))
			r.s.MoorRetry = 300 * time.Millisecond
		}, "GitHub did not confirm its PRs"},
	} {
		for _, route := range []string{"stop", "return"} {
			t.Run(c.name+" on "+route, func(t *testing.T) {
				t.Parallel()
				r := newLiveRig(t, false)
				r.carry(t, livePR7)
				c.setup(r)
				rec := r.post(route, `{"moor":true}`)
				if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), c.want) {
					t.Fatalf("%s with moor = %d %s, want 409 %q", route, rec.Code, rec.Body, c.want)
				}
				holdsFor(t, settleHold(), "the refused ship was touched", func() bool {
					_, err := os.Stat(r.mooredFile())
					names := r.eventNames(t)
					return r.live(t) && err != nil && !contains(names, "moored") && !contains(names, "stopped") &&
						!contains(names, "ended") && !contains(names, "returning")
				})
			})
		}
	}
}

func TestACleanExitMoorsAShipWithNoReportForTheOpenPRItCarriesBeforeAnyPoll(t *testing.T) {
	t.Parallel()
	r := newPRRig(t)
	r.gh.mu.Lock()
	r.gh.exitCheck = true
	r.gh.mu.Unlock()
	r.mine(prSeven, "OPEN", time.Minute)
	if err := r.s.Host.SetOptions(context.Background(), "saltpaw", map[string]string{"prs": prSeven}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the carried PR to be on the session", func() bool {
		_ = r.s.Host.Sync(context.Background())
		return r.options(t, "saltpaw")["prs"] == prSeven
	})

	r.exit(t, "0")
	waitUntil(t, "the ship to be moored", func() bool {
		recs, err := moor.List(r.home)
		return err == nil && len(recs) == 1
	})
	recs, _ := moor.List(r.home)
	if !slices.Equal(recs[0].PRs, []string{prSeven}) {
		t.Errorf("moored PRs = %v, want [%s]", recs[0].PRs, prSeven)
	}
}
