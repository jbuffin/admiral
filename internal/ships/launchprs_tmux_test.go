package ships

// A ship carries the PRs its launch names from the first poll, whatever launched it.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

func (r *liveRig) mine(url string) {
	r.gh.pollGitHub.set(url, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer})
}

func (r *liveRig) finished(url, state string) {
	r.gh.pollGitHub.set(url, moor.Status{State: state, Author: author, Viewer: viewer})
}

// wantPRs checks the ship's PRs, in order, and which of them it reviews.
func (r *liveRig) wantPRs(t *testing.T, prs, reviewing []string) {
	t.Helper()
	var gotPRs, gotReviewing []string
	for _, p := range r.prs(t) {
		gotPRs = append(gotPRs, p.URL)
		if p.Reviewing {
			gotReviewing = append(gotReviewing, p.URL)
		}
	}
	if !slices.Equal(gotPRs, prs) || !slices.Equal(gotReviewing, reviewing) {
		t.Errorf("prs %v reviewing %v, want prs %v reviewing %v", gotPRs, gotReviewing, prs, reviewing)
	}
}

func (r *liveRig) forgetTheLaunch() {
	r.s.mooredMu.Lock()
	r.s.running, r.s.mooredPolls = nil, nil // a new process knows nothing of the launch
	r.s.mooredMu.Unlock()
}

func TestAShipLaunchedOnItsOwnPRCarriesItWithoutReviewingBeforeItPrintsAnything(t *testing.T) {
	r := newReviewingRig(t, livePR7)
	r.mine(livePR7)
	r.poll()

	r.wantPRs(t, []string{livePR7}, nil)
	if o := r.options(t, "saltpaw"); o["prs"] != livePR7 || o["reviewing"] != "" {
		t.Errorf("options prs %q reviewing %q, want prs %s and no reviewing so a restarted Admiral still carries it", o["prs"], o["reviewing"], livePR7)
	}
}

func TestAShipLaunchedOnAPRThatIsAStackLayerCarriesOnlyThatPRAndNeverAsksForTheStack(t *testing.T) {
	r := newReviewingRig(t, livePR7)
	r.gh.stacks = map[string][]string{"acme/widgets/12": {livePR7, livePR8, livePR9}}
	r.mine(livePR7)
	r.mine(livePR8)
	r.others(livePR9)
	for range 3 {
		r.poll()
	}

	r.wantPRs(t, []string{livePR7}, nil)
	if calls := r.gh.stackLog(); len(calls) != 0 {
		t.Errorf("stack lookups = %v, want none for a PR URL launch", calls)
	}
}

func TestAFreeTextShipCarriesAPRNamedAmongItsWordsAndShowsItOnTheWire(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{text: "please review " + livePR7 + " and report back"})
	r.others(livePR7)
	r.poll()

	r.wantPRs(t, []string{livePR7}, []string{livePR7})
	if !r.live(t) {
		t.Fatal("the ship is not live")
	}
	_, raw, _ := r.wire(t)
	var wire []map[string]any
	if err := json.Unmarshal(raw["prs"], &wire); err != nil || len(wire) != 1 || wire[0]["url"] != livePR7 || wire[0]["reviewing"] != true {
		t.Errorf("GET /api/ships prs = %s, want %s reviewing while the ship still runs", raw["prs"], livePR7)
	}
}

func TestAFreeTextShipCarriesAPRNamedInItsNote(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{note: "context: " + livePR7, text: "look at the failing check"})
	r.others(livePR7)
	r.poll()

	r.wantPRs(t, []string{livePR7}, []string{livePR7})
}

func TestAShipWhoseWholePromptIsAPRURLCarriesIt(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{text: livePR7})
	r.others(livePR7)
	r.poll()

	r.wantPRs(t, []string{livePR7}, []string{livePR7})
}

func TestACommodoreLaunchOnAPRURLCarriesIt(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{kind: "commodore", text: livePR7})
	r.others(livePR7)
	r.poll()

	r.wantPRs(t, []string{livePR7}, []string{livePR7})
}

func TestEveryOpenPRTheLaunchTextNamesIsCarriedAndTheOwnOneIsNotReviewing(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{text: "compare " + livePR7 + ", " + livePR8 + " and " + livePR9})
	r.others(livePR7)
	r.mine(livePR8)
	r.others(livePR9)
	r.poll()

	r.wantPRs(t, []string{livePR7, livePR8, livePR9}, []string{livePR7, livePR9})
	if o := r.options(t, "saltpaw"); o["prs"] != livePR7+","+livePR8+","+livePR9 || o["reviewing"] != livePR7+","+livePR9 {
		t.Errorf("options prs %q reviewing %q, want all three carried and %s,%s reviewing", o["prs"], o["reviewing"], livePR7, livePR9)
	}
}

func TestAClosedAndAMergedLaunchPRAreNotCarriedAndAreNotAskedAboutAgain(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{text: livePR7 + " " + livePR8 + " " + livePR9})
	r.finished(livePR7, "CLOSED")
	r.finished(livePR8, "MERGED")
	r.others(livePR9)
	r.poll()
	r.wantPRs(t, []string{livePR9}, []string{livePR9})

	r.others(livePR7) // reopened after the launch was judged
	r.poll()
	r.wantPRs(t, []string{livePR9}, []string{livePR9})
}

func TestAnOwnOrderPRThatIsClosedIsNotCarried(t *testing.T) {
	r := newReviewingRig(t, livePR7)
	r.finished(livePR7, "CLOSED")
	r.poll()

	r.wantPRs(t, nil, nil)
}

func TestAFreeTextLaunchNamesNoPRWhenItHoldsOnlyAnIssueOrAnotherHost(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{text: "fix https://github.com/acme/widgets/issues/3, see https://ghe.example.com/acme/widgets/pull/7"})
	r.others(livePR7)
	r.poll()

	r.wantPRs(t, nil, nil)
	if calls := r.gh.callLog(); len(calls) != 0 {
		t.Errorf("GitHub was asked %v, want nothing for text that names no PR on its host", calls)
	}
}

func TestASourceOrdersTextThatCitesAPRDoesNotAttachIt(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{orderText: "Broken since " + livePR7 + ", see " + livePR9})
	r.others(livePR7)
	r.others(livePR9)
	r.poll()

	r.wantPRs(t, nil, nil)
	if calls := r.gh.callLog(); len(calls) != 0 {
		t.Errorf("GitHub was asked %v, want nothing: an order's text is not the ship's launch PR", calls)
	}
}

func TestAnAdmiralRestartBeforeTheFirstPollStillJudgesTheLaunchPRs(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{text: "review " + livePR7 + " and " + livePR8})
	r.forgetTheLaunch()
	r.others(livePR7)
	r.mine(livePR8)
	r.poll()

	r.wantPRs(t, []string{livePR7, livePR8}, []string{livePR7})
}

func TestLaunchPRsJudgedBeforeAnAdmiralRestartStayCarriedAfterIt(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{text: "review " + livePR7 + " and " + livePR8})
	r.others(livePR7)
	r.mine(livePR8)
	r.poll()
	_ = r.s.Host.Sync(context.Background()) // the host reads back the options the poll wrote

	r.forgetTheLaunch()

	r.wantPRs(t, []string{livePR7, livePR8}, []string{livePR7})
	r.poll()
	r.wantPRs(t, []string{livePR7, livePR8}, []string{livePR7})
}

func TestALaunchWithAPRInItsTextAsksThePollerToLookAtOnceAndOneWithNoneDoesNot(t *testing.T) {
	for _, c := range []struct {
		name string
		spec launchSpec
		want int
	}{
		{"a PR in the text", launchSpec{text: "review " + livePR7}, 1},
		{"a PR in the note", launchSpec{note: livePR7, text: "go"}, 1},
		{"no PR", launchSpec{text: "fix the build"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newLaunchedRig(t, c.spec)
			if got := len(r.s.kickChan()); got != c.want {
				t.Errorf("pending poll kicks = %d, want %d", got, c.want)
			}
		})
	}
}

func TestAScoutCarriesNoPRItsLaunchTextNamesAndAsksGitHubNothing(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{scout: true, text: "review " + livePR7})
	r.others(livePR7)
	r.poll()

	r.wantPRs(t, nil, nil)
	if calls := r.gh.callLog(); len(calls) != 0 {
		t.Errorf("GitHub was asked %v, want nothing for a scout", calls)
	}
}

func TestALaunchNamingMoreThanTwentyPRsCarriesOnlyTheFirstTwenty(t *testing.T) {
	var urls []string
	for i := 1; i <= 21; i++ {
		u := fmt.Sprintf("https://github.com/acme/widgets/pull/%d", 100+i)
		urls = append(urls, u)
	}
	r := newLaunchedRig(t, launchSpec{text: strings.Join(urls, " ")})
	for _, u := range urls {
		r.others(u)
	}
	r.poll()
	r.poll()

	r.wantPRs(t, urls[:maxFreshPerPoll], urls[:maxFreshPerPoll])
}

func TestALaunchPRWhoseFirstAskFailsIsCarriedAfterALaterPollAnswers(t *testing.T) {
	r := newLaunchedRig(t, launchSpec{text: "review " + livePR7})
	r.others(livePR7)
	r.gh.failOn = livePR7
	r.poll()
	r.wantPRs(t, nil, nil)

	r.gh.mu.Lock()
	r.gh.failOn = ""
	r.gh.mu.Unlock()
	r.poll()
	r.wantPRs(t, []string{livePR7}, []string{livePR7})
}
