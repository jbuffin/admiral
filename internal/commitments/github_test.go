package commitments_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/ledger"
	"github.com/jbuffin/admiral/internal/moor"
)

const (
	prURL    = "https://github.com/acme/widgets/pull/7"
	prURL2   = "https://github.com/acme/widgets/pull/9"
	issueURL = "https://github.com/acme/widgets/issues/3"
	gheURL   = "https://ghe.corp.example/acme/widgets/pull/4"
)

type ghCall struct {
	host string
	kind string // pr or issue
	urls []string
}

// fakeGH answers for one host: a URL missing from its maps is an item GitHub answers NOT_FOUND for.
type fakeGH struct {
	w    *ghWorld
	host string

	mu       sync.Mutex
	prs      map[string]string
	issues   map[string]string
	repoGone map[string]bool // URLs whose repository GitHub cannot look in
	err      error
	block    chan struct{} // while non-nil, a call waits for it to close
}

func (f *fakeGH) set(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func (f *fakeGH) answer(kind string, urls []string, ctx context.Context) (map[string]string, []string, error) {
	f.w.record(ghCall{f.host, kind, append([]string(nil), urls...)})
	f.mu.Lock()
	block, err := f.block, f.err
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	src := f.prs
	if kind == "issue" {
		src = f.issues
	}
	out := map[string]string{}
	var unanswered []string
	for _, u := range urls {
		if f.repoGone[u] {
			unanswered = append(unanswered, u)
		} else if s, ok := src[u]; ok {
			out[u] = s
		}
	}
	return out, unanswered, nil
}

func (f *fakeGH) PullRequests(ctx context.Context, urls []string) (map[string]moor.Status, error) {
	got, un, err := f.answer("pr", urls, ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]moor.Status{}
	for u, s := range got {
		out[u] = moor.Status{State: s}
	}
	if len(un) > 0 {
		return out, &moor.Unanswered{URLs: un}
	}
	return out, nil
}

func (f *fakeGH) IssueStates(ctx context.Context, urls []string) (map[string]string, error) {
	got, un, err := f.answer("issue", urls, ctx)
	if err != nil {
		return nil, err
	}
	if len(un) > 0 {
		return got, &moor.Unanswered{URLs: un}
	}
	return got, nil
}

type ghWorld struct {
	mu     sync.Mutex
	calls  []ghCall
	looked int
	logs   []string
	hosts  map[string]*fakeGH
}

func (w *ghWorld) record(c ghCall) { w.mu.Lock(); defer w.mu.Unlock(); w.calls = append(w.calls, c) }

func (w *ghWorld) lookup(_, host string) commitments.GitHub {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.looked++
	if f, ok := w.hosts[host]; ok {
		return f
	}
	return nil
}

func (w *ghWorld) logf(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logs = append(w.logs, fmt.Sprintf(format, args...))
}

// polls counts the polls: each one looks up a client per host, found or not.
func (w *ghWorld) polls() int { w.mu.Lock(); defer w.mu.Unlock(); return w.looked }
func (w *ghWorld) ghCalls() []ghCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]ghCall(nil), w.calls...)
}
func (w *ghWorld) logLines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.logs...)
}

func (w *ghWorld) callsOf(host, kind string) []ghCall {
	var out []ghCall
	for _, c := range w.ghCalls() {
		if c.host == host && c.kind == kind {
			out = append(out, c)
		}
	}
	return out
}

type ghRig struct {
	*rig
	*ghWorld
	gh *fakeGH // github.com
}

func newGHRig(t *testing.T) *ghRig {
	t.Helper()
	w := &ghWorld{hosts: map[string]*fakeGH{}}
	g := &ghRig{rig: newRig(t), ghWorld: w}
	g.gh = g.host("github.com")
	g.wire()
	return g
}

func (g *ghRig) host(name string) *fakeGH {
	f := &fakeGH{w: g.ghWorld, host: name, prs: map[string]string{}, issues: map[string]string{}, repoGone: map[string]bool{}}
	g.ghWorld.hosts[name] = f
	return f
}

func (g *ghRig) wire() {
	g.r.GitHubFor = g.ghWorld.lookup
	g.r.Logf = g.ghWorld.logf
}

func (g *ghRig) restart() { g.rig.restart(); g.wire() }

// run starts the runner and returns the function that stops it; the test's end stops it too.
func (g *ghRig) run() (stop func()) {
	g.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r := g.r
	go func() { r.Run(ctx); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				g.t.Error("Run did not return within 2s of its context ending")
			}
		})
	}
	g.t.Cleanup(stop)
	return stop
}

// advanceUntil moves the fake clock one poll interval at a time, so a poller that waits on the runner's clock wakes, until ok.
func (g *ghRig) advanceUntil(what string, ok func() bool) {
	g.t.Helper()
	for end := time.Now().Add(1500 * time.Millisecond); ; time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
		if time.Now().After(end) {
			g.t.Fatalf("within 1.5s of fake-clock polls: %s", what)
		}
		g.clk.set(g.clk.Now().Add(commitments.GHPoll))
	}
}

// settle lets the runner poll a few times when the closed commitments leave nothing to look up, so a poll cannot be counted.
func (g *ghRig) settle() {
	for range 3 {
		g.clk.set(g.clk.Now().Add(commitments.GHPoll))
		time.Sleep(60 * time.Millisecond)
	}
}

// pollsMore advances the clock until the runner has polled n more times.
func (g *ghRig) pollsMore(n int) {
	g.t.Helper()
	want := g.polls() + n
	g.advanceUntil(fmt.Sprintf("%d more polls", n), func() bool { return g.polls() >= want })
	time.Sleep(50 * time.Millisecond) // a verdict from the last poll reaches the runner
}

func prCommitment(kind, url string) ledger.Commitment {
	return ledger.Commitment{Text: "tell me about " + url, Trigger: ledger.Trigger{Kind: kind, Subject: url}, Action: ledger.ActionRemind}
}

func launchOn(kind, url string) ledger.Commitment {
	c := prCommitment(kind, url)
	c.Action, c.Launch = ledger.ActionLaunch, []byte(launchSpec)
	return c
}

// The GitHub rows of the trigger-and-drop table, the package's single source of the rules (every row has a test).
var githubRows = []struct {
	name    string
	kind    string
	subject string
	state   string // "" is an item GitHub answers NOT_FOUND for
	want    string // fires, waits or drops
}{
	{"pr_merged fires on MERGED", ledger.TriggerPRMerged, prURL, "MERGED", fires},
	{"pr_merged waits on OPEN", ledger.TriggerPRMerged, prURL, "OPEN", waits},
	{"pr_merged drops on CLOSED: closed without merging", ledger.TriggerPRMerged, prURL, "CLOSED", drops},
	{"pr_merged drops when the PR is gone", ledger.TriggerPRMerged, prURL, "", drops},
	{"pr_closed fires on CLOSED", ledger.TriggerPRClosed, prURL, "CLOSED", fires},
	{"pr_closed fires on MERGED", ledger.TriggerPRClosed, prURL, "MERGED", fires},
	{"pr_closed waits on OPEN", ledger.TriggerPRClosed, prURL, "OPEN", waits},
	{"pr_closed drops when the PR is gone", ledger.TriggerPRClosed, prURL, "", drops},
	{"issue_closed fires on CLOSED", ledger.TriggerIssueClosed, issueURL, "CLOSED", fires},
	{"issue_closed waits on OPEN", ledger.TriggerIssueClosed, issueURL, "OPEN", waits},
	{"issue_closed drops when the issue is gone", ledger.TriggerIssueClosed, issueURL, "", drops},
}

func TestEachGitHubRowOfTheTriggerTable(t *testing.T) {
	for _, row := range githubRows {
		t.Run(row.name, func(t *testing.T) {
			g := newGHRig(t)
			c := g.add(prCommitment(row.kind, row.subject))
			if row.state != "" {
				g.gh.set(func() {
					if row.kind == ledger.TriggerIssueClosed {
						g.gh.issues[row.subject] = row.state
					} else {
						g.gh.prs[row.subject] = row.state
					}
				})
			}
			g.run()
			// A poll that fires or drops closes the commitment, so no later poll has a subject to look up.
			if row.want == waits {
				g.pollsMore(2)
			} else {
				g.pollsMore(1)
			}
			switch row.want {
			case fires:
				g.wantCard(c.ID, commitments.OutcomeReminded)
			case drops:
				card := g.wantCard(c.ID, commitments.OutcomeDropped)
				if strings.TrimSpace(card.Why) == "" {
					t.Errorf("the drop card says nothing about why: %+v", card)
				}
				if got := g.open(); len(got) != 0 {
					t.Errorf("open commitments after the drop = %+v, want none", got)
				}
			case waits:
				g.wantWaiting(c.ID)
			}
		})
	}
}

func TestAPRDroppedForNotMergedSaysItClosedWithoutMerging(t *testing.T) {
	g := newGHRig(t)
	c := g.add(prCommitment(ledger.TriggerPRMerged, prURL))
	g.gh.set(func() { g.gh.prs[prURL] = "CLOSED" })
	g.run()
	g.pollsMore(1)
	card := g.wantCard(c.ID, commitments.OutcomeDropped)
	if !strings.Contains(strings.ToLower(card.Why), "merg") {
		t.Errorf("drop reason = %q, want it to say the PR closed without merging", card.Why)
	}
}

func TestAGitHubTriggerFiresItsLaunchOnceAcrossARestart(t *testing.T) {
	g := newGHRig(t)
	c := g.add(launchOn(ledger.TriggerPRMerged, prURL))
	g.gh.set(func() { g.gh.prs[prURL] = "OPEN" })
	stop := g.run()
	g.pollsMore(1)
	if n := len(g.ships.requests()); n != 0 {
		t.Fatalf("%d launches while the PR is open, want none", n)
	}
	g.gh.set(func() { g.gh.prs[prURL] = "MERGED" })
	g.pollsMore(1)
	waitFor(t, "the merged PR never launched", func() bool { return len(g.ships.requests()) == 1 })
	req := g.ships.requests()[0].req
	if req.CommitmentKey != "commitment:"+c.ID || !req.Autopilot {
		t.Errorf("launch request = %+v, want the commitment key and autopilot", req)
	}
	g.wantCard(c.ID, commitments.OutcomeLaunched)

	stop()
	g.restart()
	g.run()
	g.settle()
	if n := len(g.ships.requests()); n != 1 {
		t.Errorf("%d launches after a restart, want the one", n)
	}
	if n := len(g.cards.cards()); n != 1 {
		t.Errorf("%d cards after a restart, want the one: %+v", n, g.cards.cards())
	}
}

func TestAGitHubReminderCardIsPostedOnceAcrossARestart(t *testing.T) {
	for _, row := range githubRows {
		if row.want != fires {
			continue
		}
		t.Run(row.name, func(t *testing.T) {
			g := newGHRig(t)
			g.add(prCommitment(row.kind, row.subject))
			g.gh.set(func() { g.gh.prs[prURL], g.gh.issues[issueURL] = row.state, row.state })
			stop := g.run()
			g.pollsMore(1)
			stop()
			g.restart()
			g.run()
			g.settle()
			if n := len(g.cards.cards()); n != 1 {
				t.Errorf("%d cards, want the one reminder: %+v", n, g.cards.cards())
			}
		})
	}
}

// A failed call, a repository GitHub cannot look in and a host with no client are all "not answered": the commitment waits quietly.
func TestAGitHubFailureKeepsTheCommitmentOpenWithNoCard(t *testing.T) {
	cases := []struct {
		name string
		arm  func(g *ghRig)
	}{
		{"a call error", func(g *ghRig) { g.gh.set(func() { g.gh.err = errors.New("Couldn't reach github.com") }) }},
		{"a repository-level NOT_FOUND", func(g *ghRig) {
			g.gh.set(func() { g.gh.repoGone[prURL], g.gh.repoGone[issueURL] = true, true })
		}},
		{"no client for the subject's host", func(g *ghRig) { delete(g.ghWorld.hosts, "github.com") }},
	}
	for _, tc := range cases {
		for _, kind := range []string{ledger.TriggerPRMerged, ledger.TriggerPRClosed, ledger.TriggerIssueClosed} {
			t.Run(tc.name+" "+kind, func(t *testing.T) {
				g := newGHRig(t)
				url := prURL
				if kind == ledger.TriggerIssueClosed {
					url = issueURL
				}
				c := g.add(launchOn(kind, url))
				tc.arm(g)
				g.run()
				g.pollsMore(3)
				g.wantWaiting(c.ID)
				if n := len(g.ships.requests()); n != 0 {
					t.Errorf("%d launches, want none", n)
				}
			})
		}
	}
}

func TestAGitHubFailureIsLoggedOncePerSubjectPerStreak(t *testing.T) {
	g := newGHRig(t)
	g.add(prCommitment(ledger.TriggerPRMerged, prURL))
	g.gh.set(func() { g.gh.err = errors.New("rate limited") })
	g.run()
	g.pollsMore(1)
	if n := len(g.logLines()); n != 1 {
		t.Fatalf("%d log lines after the first failure, want 1: %q", n, g.logLines())
	}
	if !strings.Contains(g.logLines()[0], prURL) {
		t.Errorf("log line %q does not name the subject", g.logLines()[0])
	}
	g.pollsMore(3)
	if n := len(g.logLines()); n != 1 {
		t.Errorf("%d log lines after four failures in a row, want still 1: %q", n, g.logLines())
	}

	g.gh.set(func() { g.gh.err = nil; g.gh.prs[prURL] = "OPEN" })
	g.pollsMore(1)
	g.gh.set(func() { g.gh.err = errors.New("rate limited again") })
	g.pollsMore(2)
	if n := len(g.logLines()); n != 2 {
		t.Errorf("%d log lines after a recovery and a new failure, want 2: %q", n, g.logLines())
	}
}

func TestANoClientFailureIsLoggedOnce(t *testing.T) {
	g := newGHRig(t)
	g.add(prCommitment(ledger.TriggerIssueClosed, issueURL))
	delete(g.ghWorld.hosts, "github.com")
	g.run()
	g.pollsMore(3)
	if n := len(g.logLines()); n != 1 {
		t.Errorf("%d log lines for a host with no client over three polls, want 1: %q", n, g.logLines())
	}
}

func TestAShipChangeMakesNoGitHubCall(t *testing.T) {
	g := newGHRig(t)
	g.add(prCommitment(ledger.TriggerPRMerged, prURL))
	g.gh.set(func() { g.gh.prs[prURL] = "OPEN" })
	g.run()
	g.pollsMore(1)
	calls, looked := len(g.ghCalls()), g.polls()

	c := g.add(remindAt(g.clk.Now().Add(time.Second)))
	g.clk.set(g.clk.Now().Add(2 * time.Second))
	for range 3 {
		g.r.Changed()
	}
	waitFor(t, "the runner never looked after Changed: the due reminder is not posted", func() bool {
		_, ok := g.cardFor(c.ID, commitments.OutcomeReminded)
		return ok
	})
	if n := len(g.ghCalls()); n != calls {
		t.Errorf("%d gh calls after ship changes, want the %d of the poll", n, calls)
	}
	if n := g.polls(); n != looked {
		t.Errorf("%d client lookups after ship changes, want %d", n, looked)
	}
}

func TestOnePollMakesOneBatchedCallPerHostCoveringEverySubject(t *testing.T) {
	g := newGHRig(t)
	ghe := g.host("ghe.corp.example")
	g.add(prCommitment(ledger.TriggerPRMerged, prURL))
	g.add(prCommitment(ledger.TriggerPRClosed, prURL))
	g.add(prCommitment(ledger.TriggerPRClosed, prURL2))
	g.add(prCommitment(ledger.TriggerIssueClosed, issueURL))
	g.add(prCommitment(ledger.TriggerPRMerged, gheURL))
	g.gh.set(func() { g.gh.prs[prURL], g.gh.prs[prURL2], g.gh.issues[issueURL] = "OPEN", "OPEN", "OPEN" })
	ghe.set(func() { ghe.prs[gheURL] = "OPEN" })
	g.run()
	g.advanceUntil("the first poll's calls", func() bool { return len(g.ghCalls()) >= 3 })
	time.Sleep(100 * time.Millisecond)
	before := len(g.ghCalls())
	g.pollsMore(1)
	calls := g.ghCalls()[before:]

	count := map[string]int{}
	for _, c := range calls {
		count[c.host+" "+c.kind]++
	}
	want := map[string]int{"github.com pr": 1, "github.com issue": 1, "ghe.corp.example pr": 1}
	for k, n := range want {
		if count[k] != n {
			t.Errorf("one poll made %d %q calls, want %d: %+v", count[k], k, n, calls)
		}
	}
	if len(calls) != 3 {
		t.Errorf("one poll made %d calls, want 3 (a PR and an issue batch on github.com, a PR batch on the other host): %+v", len(calls), calls)
	}
	for _, c := range calls {
		if c.host == "github.com" && c.kind == "pr" && (len(c.urls) != 2 || !(contains(c.urls, prURL) && contains(c.urls, prURL2))) {
			t.Errorf("the github.com PR call asked for %v, want each of the two PRs once", c.urls)
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestAPollerBlockedOnGitHubDoesNotStopATimeTriggerFiring(t *testing.T) {
	g := newGHRig(t)
	g.add(prCommitment(ledger.TriggerPRMerged, prURL))
	block := make(chan struct{})
	g.gh.set(func() { g.gh.block = block })
	g.run()
	g.t.Cleanup(func() { close(block) })
	g.advanceUntil("the poll never reached GitHub", func() bool { return len(g.ghCalls()) >= 1 })

	c := g.add(remindAt(g.clk.Now().Add(time.Second)))
	g.clk.set(g.clk.Now().Add(2 * time.Second))
	g.r.Changed()
	waitFor(t, "a time trigger did not fire while a GitHub call blocks: the poll holds the tick's lock", func() bool {
		_, ok := g.cardFor(c.ID, commitments.OutcomeReminded)
		return ok
	})
}

// The aged card.

const aged = 14 * 24 * time.Hour

func TestANoTriggerCommitmentOlderThanFourteenDaysGetsOneStillWantThisCard(t *testing.T) {
	g := newRig(t)
	c := g.add(remindNone())
	pr := g.add(prCommitment(ledger.TriggerPRMerged, prURL))

	g.clk.set(c.TS.Add(aged - time.Minute))
	g.tick()
	if n := len(g.cards.cards()); n != 0 {
		t.Fatalf("%d cards for a commitment younger than 14 days, want none: %+v", n, g.cards.cards())
	}

	g.clk.set(c.TS.Add(aged + time.Minute))
	g.tick()
	card, ok := g.cardFor(c.ID, commitments.OutcomeAged)
	if !ok {
		t.Fatalf("no aged card after 14 days among %+v", g.cards.cards())
	}
	if card.ID != "commitment-"+c.ID+"-aged" || card.Commitment != c.ID || card.Text != c.Text {
		t.Errorf("card = %+v, want ID commitment-%s-aged for the commitment and its text", card, c.ID)
	}
	got, open := g.find(c.ID)
	if !open {
		t.Fatalf("the aged commitment closed, want it open until the engineer cancels it")
	}
	if !got.Shown {
		t.Errorf("the commitment is not marked shown after its aged card")
	}
	if _, ok := g.cardFor(pr.ID, commitments.OutcomeAged); ok {
		t.Errorf("a commitment with a trigger got an aged card")
	}

	posts := g.cards.posts
	g.clk.set(c.TS.Add(aged + 24*time.Hour))
	g.tick()
	g.restart()
	g.tick()
	if g.cards.posts != posts {
		t.Errorf("%d posts after a second tick and a restart, want the %d of the first: the card shows once", g.cards.posts, posts)
	}
	if _, open := g.find(c.ID); !open {
		t.Errorf("the commitment closed after the later ticks, want it open")
	}
}

func TestTheRunnerWakesAtTheFourteenDayMarkOfANoTriggerCommitment(t *testing.T) {
	g := newRig(t)
	c := g.add(remindNone())
	mark := c.TS.Add(aged)
	g.clk.set(mark.Add(-time.Minute))
	g.startRun()
	waitFor(t, "Run never armed a timer for the 14-day mark", func() bool { return g.clk.hasDeadline(mark) })
	g.clk.set(mark.Add(time.Second))
	waitFor(t, "no aged card after the clock passed the mark", func() bool {
		_, ok := g.cardFor(c.ID, commitments.OutcomeAged)
		return ok
	})
}

// The outcome-log cache.

func TestAnUnchangedOutcomeLogIsNotReadAgainAndAnAppendedRecordIsSeen(t *testing.T) {
	g := newRig(t)
	c := g.add(shipTrigger(ledger.TriggerShipSailed))
	g.launched("saltpaw", t0.Add(-time.Hour))
	var mu sync.Mutex
	reads := 0
	g.r.ReadFile = func(path string) ([]byte, error) {
		mu.Lock()
		reads++
		mu.Unlock()
		return os.ReadFile(path)
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return reads }

	g.tick()
	first := count()
	if first == 0 {
		t.Fatalf("the first tick read no outcome-log file through the read seam")
	}
	g.tick()
	g.tick()
	if got := count(); got != first {
		t.Errorf("%d reads after two more ticks over an unchanged log, want the first tick's %d", got, first)
	}

	g.endedClean("saltpaw", t0.Add(-time.Minute))
	g.tick()
	if got := count(); got <= first {
		t.Errorf("%d reads after a record was appended, want the file read again", got)
	}
	g.wantCard(c.ID, commitments.OutcomeReminded)
}
