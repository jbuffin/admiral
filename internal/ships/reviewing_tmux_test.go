package ships

// A ship launched on someone else's PR or stack carries it as reviewing and moors when its turn ends done.

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

const (
	reviewStack = "https://github.com/acme/widgets/stack/12"
	author      = "dana"
)

// urlOrder gives a source's order the URL the ship is launched on, and text "review it" unless it has its own.
type urlOrder struct{ url, text string }

func (o urlOrder) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	text = o.text
	if text == "" {
		text = "review it"
	}
	return text, "Review it", "", o.url, "p", nil
}
func (urlOrder) Stack(source, id string) (int, string, bool) { return 0, "", false }

func (urlOrder) Launched(source, id string) {}

// launchSpec is how a rig's ship is launched: orderURL or orderText make it a source's order, else text and note are free text.
type launchSpec struct {
	orderURL, orderText string
	text, note, kind    string // kind is the skill kind, "none" when empty
	scout               bool
}

// newReviewingRig is a live ship, saltpaw, launched on orderURL as a source's order; "" launches it from free text.
func newReviewingRig(t *testing.T, orderURL string) *liveRig {
	t.Helper()
	return newLaunchedRig(t, launchSpec{orderURL: orderURL})
}

func newLaunchedRig(t *testing.T, spec launchSpec) *liveRig {
	t.Helper()
	r := newWakeRig(t, 0)
	req := launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Name: "Saltpaw", Session: "saltpaw",
		Skill: launcher.Skill{Kind: cmp.Or(spec.kind, "none")}, Text: spec.text, Note: spec.note, Scout: spec.scout}
	if spec.text == "" && spec.note == "" {
		req.Prompt = "go"
	}
	if spec.orderURL != "" || spec.orderText != "" {
		r.s.Orders = urlOrder{spec.orderURL, spec.orderText}
		req.Source, req.OrderID = "s", "o1"
	}
	if _, lerr := r.s.LaunchOrder(req); lerr != nil {
		t.Fatalf("LaunchOrder = %v", lerr)
	}
	r.started(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	n := &nextRig{wakeRig: r, mux: mux}
	cfg := "[remote \"origin\"]\n\turl = https://github.com/acme/widgets.git\n"
	if err := os.WriteFile(r.checkout+"/.git/config", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &openGH{open: map[string]bool{livePR7: true, livePR9: true}}
	n.s.GitHub = func(string) moor.GitHub { return g }
	n.s.baselineAll()
	return &liveRig{nextRig: n, gh: g, stamp: time.Now()}
}

func (r *liveRig) others(url string) {
	r.gh.pollGitHub.set(url, moor.Status{State: "OPEN", Author: author, Viewer: viewer})
}

func (r *liveRig) poll() { r.s.PollMoored(context.Background()) }

func (r *liveRig) prs(t *testing.T) []moor.PR {
	t.Helper()
	for _, sh := range r.s.List() {
		if sh.Name == "Saltpaw" {
			return sh.PRs
		}
	}
	t.Fatal("no ship Saltpaw")
	return nil
}

func TestAShipLaunchedOnAnotherAuthorsPRCarriesItAsReviewing(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, livePR7)
	r.others(livePR7)
	r.poll()

	got := r.prs(t)
	if len(got) != 1 || got[0].URL != livePR7 || got[0].State != "OPEN" || got[0].Author != author || !got[0].Reviewing {
		t.Fatalf("prs = %+v, want %s open, by %s, reviewing", got, livePR7, author)
	}
	if o := r.options(t, "saltpaw"); o["prs"] != livePR7 || o["reviewing"] != livePR7 {
		t.Errorf("options prs %q reviewing %q, want both %s so a restarted Admiral reads the role back", o["prs"], o["reviewing"], livePR7)
	}
	_, raw, _ := r.wire(t)
	var wire []map[string]any
	if err := json.Unmarshal(raw["prs"], &wire); err != nil || len(wire) != 1 || wire[0]["reviewing"] != true || wire[0]["author"] != author {
		t.Errorf("GET /api/ships prs = %s, want reviewing true and author %q", raw["prs"], author)
	}
}

func TestAShipLaunchedOnItsOwnPRDoesNotGainReviewing(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, livePR7)
	r.s.Candidates = func(string) []string { return []string{livePR7} } // the ship printed the PR it opened
	launched, _ := time.Parse(time.RFC3339, r.options(t, "saltpaw")["launched"])
	r.gh.pollGitHub.set(livePR7, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer, CreatedAt: launched.Add(time.Minute)})
	r.poll()

	got := r.prs(t)
	if len(got) != 1 || got[0].Author != viewer || got[0].Reviewing {
		t.Errorf("prs = %+v, want its own PR by %s, not reviewing", got, viewer)
	}
	if o := r.options(t, "saltpaw"); o["reviewing"] != "" {
		t.Errorf("reviewing option = %q, want none", o["reviewing"])
	}
}

func TestAShipLaunchedFromFreeTextCarriesNoPRAndAsksGitHubNothing(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, "")
	r.others(livePR7)
	r.poll()
	if got := r.prs(t); len(got) != 0 {
		t.Errorf("prs = %+v, want none for a free-text launch", got)
	}
	if calls := r.gh.callLog(); len(calls) != 0 || len(r.gh.stackLog()) != 0 {
		t.Errorf("GitHub was asked %v and stacks %v, want nothing", calls, r.gh.stackLog())
	}
}

const (
	stackL1 = "https://github.com/acme/widgets/pull/21"
	stackL2 = "https://github.com/acme/widgets/pull/22"
	stackL3 = "https://github.com/acme/widgets/pull/23"
	stackL4 = "https://github.com/acme/widgets/pull/24"
)

func TestAShipLaunchedOnAStackCarriesEveryOpenLayerBottomFirstAndReviewsOnlyOthers(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, reviewStack)
	r.gh.stacks = map[string][]string{"acme/widgets/12": {stackL1, stackL2, stackL3, stackL4}}
	r.others(stackL1)
	r.mine(stackL2) // the viewer's own layer
	r.others(stackL3)
	r.gh.pollGitHub.set(stackL4, moor.Status{State: "MERGED", Author: author, Viewer: viewer})
	r.poll()

	r.wantPRs(t, []string{stackL1, stackL2, stackL3}, []string{stackL1, stackL3})
	if o := r.options(t, "saltpaw"); o["reviewing"] != stackL1+","+stackL3 || o["prs"] != stackL1+","+stackL2+","+stackL3 {
		t.Errorf("options prs %q reviewing %q, want all three open layers carried and %s,%s reviewing", o["prs"], o["reviewing"], stackL1, stackL3)
	}
	_, raw, _ := r.wire(t)
	var wire []map[string]any
	if err := json.Unmarshal(raw["prs"], &wire); err != nil || len(wire) != 3 || wire[1]["url"] != stackL2 || wire[1]["author"] != viewer || wire[1]["reviewing"] == true {
		t.Errorf("GET /api/ships prs = %s, want the viewer's layer %s listed second, not reviewing", raw["prs"], stackL2)
	}
}

func TestAShipLaunchedOnAStackOfOnlyTheViewersLayersCarriesThemAndReviewsNothing(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, reviewStack)
	r.gh.stacks = map[string][]string{"acme/widgets/12": {stackL1, stackL2}}
	r.mine(stackL1)
	r.mine(stackL2)
	r.poll()

	r.wantPRs(t, []string{stackL1, stackL2}, nil)
	if o := r.options(t, "saltpaw"); o["reviewing"] != "" {
		t.Errorf("reviewing option = %q, want none: the ship reviews nothing, so Merge is not refused", o["reviewing"])
	}
}

func TestAShipOnAStackOfOnlyTheViewersLayersMoorsCarryingThemWhenItsReportListsNoPRs(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, reviewStack)
	r.gh.stacks = map[string][]string{"acme/widgets/12": {stackL1, stackL2}}
	r.mine(stackL1)
	r.mine(stackL2)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done")
	waitUntil(t, "the poll to carry both layers", func() bool { r.poll(); return len(r.prs(t)) == 2 })
	r.turn(t, "needs_you", "stop")
	r.waitMoored(t)

	rec := readRecord(t, r.mooredFile())
	if !slices.Equal(rec.PRs, []string{stackL1, stackL2}) || len(rec.Reviewing) != 0 {
		t.Errorf("record prs %v reviewing %v, want prs [%s %s] and none reviewing, so Merge is not refused", rec.PRs, rec.Reviewing, stackL1, stackL2)
	}
	r.poll()
	r.wantPRs(t, []string{stackL1, stackL2}, nil)
	if rec := r.postMerge(); rec.Code == http.StatusConflict && strings.Contains(rec.Body.String(), "reviews a pull request") {
		t.Errorf("merge = %d %s, want it not refused as a review for a ship that reviews nothing", rec.Code, rec.Body)
	}
}

func TestAShipOnAMixedStackMoorsCarryingEveryLayerAndReviewingOnlyTheOthers(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, reviewStack)
	r.gh.stacks = map[string][]string{"acme/widgets/12": {stackL1, stackL2, stackL3}}
	r.others(stackL1)
	r.mine(stackL2)
	r.others(stackL3)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done")
	waitUntil(t, "the poll to carry all three layers", func() bool { r.poll(); return len(r.prs(t)) == 3 })
	r.turn(t, "needs_you", "stop")
	r.waitMoored(t)

	rec := readRecord(t, r.mooredFile())
	if !slices.Equal(rec.PRs, []string{stackL1, stackL2, stackL3}) || !slices.Equal(rec.Reviewing, []string{stackL1, stackL3}) {
		t.Errorf("record prs %v reviewing %v, want [%s %s %s] reviewing [%s %s]", rec.PRs, rec.Reviewing, stackL1, stackL2, stackL3, stackL1, stackL3)
	}
	if rec := r.postMerge(); rec.Code != http.StatusConflict {
		t.Errorf("merge = %d %s, want 409 while the ship reviews another author's layer", rec.Code, rec.Body)
	}
}

func TestAStackGitHubDoesNotHaveIsAskedOnceAndCarriesNothing(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, reviewStack) // no stack answer: a 404
	for range 3 {
		r.poll()
	}
	if got := r.prs(t); len(got) != 0 {
		t.Errorf("prs = %+v, want none", got)
	}
	if calls := r.gh.stackLog(); len(calls) != 1 {
		t.Errorf("stack lookups = %v, want one: a 404 is final for the launch", calls)
	}
}

func TestAFailingStackLookupIsRetriedUpToMaxCandidateTriesThenGivenUp(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, reviewStack)
	r.gh.stackErr = context.DeadlineExceeded
	for range maxCandidateTries + 3 {
		r.poll()
	}
	if calls := r.gh.stackLog(); len(calls) != maxCandidateTries {
		t.Errorf("stack lookups = %d, want %d failed tries for the launch", len(calls), maxCandidateTries)
	}
}

func TestAnAnswerlessReviewedPRIsGivenUpAfterMaxCandidateTries(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, livePR7)
	r.gh.failOn = livePR7
	for range maxCandidateTries + 3 {
		r.poll()
	}
	if n := len(r.gh.callLog()); n != maxCandidateTries {
		t.Errorf("PullRequests calls = %d, want %d failed tries for the launch", n, maxCandidateTries)
	}
}

func TestAPrintedCopyOfTheOrderURLDoesNotUndoReviewing(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, livePR7)
	r.s.Candidates = func(string) []string { return []string{livePR7} }
	r.others(livePR7)
	for range 3 {
		r.poll()
	}
	if got := r.prs(t); len(got) != 1 || !got[0].Reviewing || got[0].Author != author {
		t.Errorf("prs = %+v, want %s still carried as reviewing", got, livePR7)
	}
}

func TestAReviewingShipMoorsWhenItsTurnEndsDoneWithAReportThatListsNoPRs(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, livePR7)
	r.others(livePR7)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done")
	waitUntil(t, "the poll to carry the reviewed PR", func() bool { r.poll(); return len(r.prs(t)) == 1 })
	r.turn(t, "needs_you", "stop")
	r.waitMoored(t)

	rec := readRecord(t, r.mooredFile())
	if !slices.Equal(rec.PRs, []string{livePR7}) || !slices.Equal(rec.Reviewing, []string{livePR7}) {
		t.Errorf("record prs %v reviewing %v, want both [%s]", rec.PRs, rec.Reviewing, livePR7)
	}
	r.poll()
	if got := r.prs(t); len(got) != 1 || !got[0].Reviewing || got[0].Author != author {
		t.Errorf("moored prs = %+v, want %s reviewing by %s", got, livePR7, author)
	}
}

func TestAReviewingShipMoorsItsReviewedPRsBeforeTheOnesItsReportListsWithoutDuplicates(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, livePR7)
	r.others(livePR7)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done", livePR9, livePR7)
	waitUntil(t, "the poll to carry the reviewed PR", func() bool { r.poll(); return len(r.prs(t)) >= 1 })
	r.turn(t, "needs_you", "stop")
	r.waitMoored(t)

	rec := readRecord(t, r.mooredFile())
	if !slices.Equal(rec.PRs, []string{livePR7, livePR9}) || !slices.Equal(rec.Reviewing, []string{livePR7}) {
		t.Errorf("record prs %v reviewing %v, want [%s %s] reviewing [%s]", rec.PRs, rec.Reviewing, livePR7, livePR9, livePR7)
	}
}

// A guard: the same done report with no PRs moors nothing for a free-text ship.
func TestAFreeTextShipWithADoneReportThatListsNoPRsStaysLive(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, "")
	r.others(livePR7)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done")
	r.turn(t, "needs_you", "stop")
	holdsFor(t, settleHold(), "the ship moored", func() bool {
		_, err := os.Stat(r.mooredFile())
		return err != nil && r.live(t)
	})
}

// A guard: a PR the ship only printed (a transcript candidate, not part of its order) does not make a done report moor it.
func TestAShipCarryingAPrintedOwnPRWithADoneReportThatListsNoPRsIsNotMoored(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		localOnly bool
	}{{"a local-only ship finishes", true}, {"a free-text ship stays live", false}} {
		t.Run(c.name, func(t *testing.T) {
			r := newReviewingRig(t, "")
			if c.localOnly {
				r.markLocalOnly(t)
			}
			r.s.Candidates = func(string) []string { return []string{livePR7} }
			launched, err := time.Parse(time.RFC3339, r.options(t, "saltpaw")["launched"])
			if err != nil {
				t.Fatal(err)
			}
			r.gh.pollGitHub.set(livePR7, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer, CreatedAt: launched.Add(time.Minute)})
			go r.s.Run(r.ctx)
			r.turn(t, "working", "")
			waitUntil(t, "the poll to carry the printed PR", func() bool { r.poll(); return len(r.prs(t)) == 1 })
			r.writeReport(t, "done")
			r.turn(t, "needs_you", "stop")
			if c.localOnly {
				r.gone(t, "the finished ship to leave the live list")
				waitUntil(t, "the finished record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "finished")) == 1 })
			}
			holdsFor(t, settleHold(), "the ship moored", func() bool {
				_, err := os.Stat(r.mooredFile())
				return err != nil && (c.localOnly || r.live(t))
			})
		})
	}
}

// postMerge asks the ship to merge on a mux whose Merge route is open, since the rig's own routes refuse it with no token check.
func (r *liveRig) postMerge() *httptest.ResponseRecorder {
	r.s.MergeGuard = func(h http.Handler) http.Handler { return h }
	mux := http.NewServeMux()
	r.s.Routes(mux)
	r.mux = mux
	return r.post("merge", "{}")
}

func readRecord(t *testing.T, path string) moor.Record {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no moored record: %v", err)
	}
	var rec moor.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestAStackLayerGitHubFailedToAnswerIsAskedAgainOnTheNextPoll(t *testing.T) {
	t.Parallel()
	const l1, l2 = "https://github.com/acme/widgets/pull/21", "https://github.com/acme/widgets/pull/22"
	r := newReviewingRig(t, reviewStack)
	r.gh.stacks = map[string][]string{"acme/widgets/12": {l1, l2}}
	r.others(l1)
	r.others(l2)
	r.gh.un = []string{l2}
	r.poll()
	_ = r.s.Host.Sync(context.Background()) // the host reads back the reviewing option the poll wrote
	r.gh.un = nil
	r.poll()

	var urls []string
	for _, p := range r.prs(t) {
		if !p.Reviewing {
			t.Errorf("layer %+v, want reviewing", p)
		}
		urls = append(urls, p.URL)
	}
	if !slices.Equal(urls, []string{l1, l2}) {
		t.Errorf("carried layers = %v, want [%s %s] once the second poll answered both", urls, l1, l2)
	}
	if o := r.options(t, "saltpaw"); o["reviewing"] != l1+","+l2 {
		t.Errorf("reviewing option = %q, want %s,%s", o["reviewing"], l1, l2)
	}
}

// A woken ship keeps the role of the record it was moored with, even when GitHub answers nothing for the round.
func TestAWokenReviewingShipReMoorsReviewingWhenGitHubNeverAnswersTheRound(t *testing.T) {
	t.Parallel()
	r := newWakeRig(t, 0)
	err := moor.Write(r.home, moor.Record{Name: "Saltpaw", Session: "saltpaw", Fleet: "work", Harness: "claude", Model: "opus",
		Skill: moor.Skill{Kind: "skill", Name: "review"}, Checkout: r.checkout, Source: "s", OrderID: "o1", Panel: "p",
		URL: "https://github.com/acme/widgets/issues/3", Title: "Fix it", PRs: []string{wakePR}, Reviewing: []string{wakePR},
		Resume: resumeText, MooredAt: time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = https://github.com/acme/widgets.git\n"
	if err := os.WriteFile(r.checkout+"/.git/config", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &openGH{open: map[string]bool{wakePR: true}}
	g.pollGitHub.set(wakePR, moor.Status{State: "OPEN", Author: author, Viewer: viewer})
	r.s.GitHub = func(string) moor.GitHub { return g }
	r.s.MoorRetry = 300 * time.Millisecond
	r.s.PollMoored(context.Background()) // the moored poll the woken round starts from
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatalf("Wake = %v", lerr)
	}
	r.started(t, 1)
	r.s.MergeGuard = func(h http.Handler) http.Handler { return h }
	mux := http.NewServeMux()
	r.s.Routes(mux)
	l := &liveRig{nextRig: &nextRig{wakeRig: r, mux: mux}, gh: g, stamp: time.Now()}
	if o := r.options(t, "saltpaw"); o["reviewing"] != wakePR {
		t.Errorf("woken session's reviewing option = %q, want %s", o["reviewing"], wakePR)
	}

	g.pollGitHub.err = context.DeadlineExceeded
	g.set(nil, context.DeadlineExceeded)
	r.exit(t, "0")
	l.waitMoored(t)

	if rec := readRecord(t, l.mooredFile()); !slices.Equal(rec.Reviewing, []string{wakePR}) {
		t.Errorf("re-moored record reviewing = %v, want [%s]", rec.Reviewing, wakePR)
	}
	if rec := l.post("merge", "{}"); rec.Code != http.StatusConflict {
		t.Errorf("merge = %d %s, want 409 for a ship that reviews a PR", rec.Code, rec.Body)
	}
}

func TestALiveReviewingShipShowsRespondedOnItsReviewedPRWhenTheAuthorPushed(t *testing.T) {
	t.Parallel()
	r := newReviewingRig(t, livePR7)
	r.gh.pollGitHub.set(livePR7, pushedPast())
	r.poll()

	if got := r.prs(t); len(got) != 1 || !got[0].Reviewing || !got[0].Responded {
		t.Errorf("prs = %+v, want %s reviewing and responded", got, livePR7)
	}
}
