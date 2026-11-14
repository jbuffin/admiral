package ships

// A ship launched on someone else's PR or stack carries it as reviewing and moors when its turn ends done.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

const (
	reviewStack = "https://github.com/acme/widgets/stack/12"
	author      = "dana"
)

// urlOrder gives a source's order the URL the ship is launched on.
type urlOrder struct{ url string }

func (o urlOrder) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	return "review it", "Review it", "", o.url, "p", nil
}
func (urlOrder) Launched(source, id string) {}

// newReviewingRig is a live ship, saltpaw, launched on orderURL as a source's order; "" launches it from free text.
func newReviewingRig(t *testing.T, orderURL string) *liveRig {
	t.Helper()
	r := newWakeRig(t, 0)
	req := launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Name: "Saltpaw", Session: "saltpaw",
		Prompt: "go", Skill: launcher.Skill{Kind: "none"}}
	if orderURL != "" {
		r.s.Orders = urlOrder{orderURL}
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

func TestAShipLaunchedOnAStackCarriesEveryOpenLayerNotByTheViewerBottomFirst(t *testing.T) {
	const l1, l2, l3, l4 = "https://github.com/acme/widgets/pull/21", "https://github.com/acme/widgets/pull/22",
		"https://github.com/acme/widgets/pull/23", "https://github.com/acme/widgets/pull/24"
	r := newReviewingRig(t, reviewStack)
	r.gh.stacks = map[string][]string{"acme/widgets/12": {l1, l2, l3, l4}}
	r.others(l1)
	r.gh.pollGitHub.set(l2, moor.Status{State: "OPEN", Author: viewer, Viewer: viewer}) // the viewer's own layer
	r.others(l3)
	r.gh.pollGitHub.set(l4, moor.Status{State: "MERGED", Author: author, Viewer: viewer})
	r.poll()

	var urls []string
	for _, p := range r.prs(t) {
		if !p.Reviewing || p.Author != author {
			t.Errorf("layer %+v, want reviewing by %s", p, author)
		}
		urls = append(urls, p.URL)
	}
	if !slices.Equal(urls, []string{l1, l3}) {
		t.Errorf("carried layers = %v, want [%s %s]: open, not the viewer's, bottom first", urls, l1, l3)
	}
	if o := r.options(t, "saltpaw"); o["reviewing"] != l1+","+l3 {
		t.Errorf("reviewing option = %q, want %s,%s", o["reviewing"], l1, l3)
	}
}

func TestAStackGitHubDoesNotHaveIsAskedOnceAndCarriesNothing(t *testing.T) {
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
	r := newReviewingRig(t, "")
	r.others(livePR7)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done")
	r.turn(t, "needs_you", "stop")
	holdsFor(t, 1500*time.Millisecond, "the ship moored", func() bool {
		_, err := os.Stat(r.mooredFile())
		return err != nil && r.live(t)
	})
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

	old := moor.RetryWindow
	moor.RetryWindow = 300 * time.Millisecond
	t.Cleanup(func() { moor.RetryWindow = old })
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
