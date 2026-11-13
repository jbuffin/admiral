package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/digest"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/ships"
)

// briefRig is a Service with fleets home and work (chat on), whose ships, campaigns and Inbox a test sets.
type briefRig struct {
	*Service
	home string
	mux  *http.ServeMux

	mu        sync.Mutex
	ships     []ships.Ship
	campaigns []follow.Followed
	items     []inbox.Item
	acked     uint64
}

func newBriefRig(t *testing.T) *briefRig {
	t.Helper()
	home := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"home": map[string]any{"name": "Home", "search_roots": []string{home}, "claude": map[string]any{"executable": "/bin/sh"}},
		"work": map[string]any{"name": "Work", "search_roots": []string{home}, "claude": map[string]any{"executable": "/bin/sh"}},
	}})
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := &briefRig{home: home, mux: http.NewServeMux()}
	r.Service = &Service{
		Home: home, Config: func() *config.Config { return cfg },
		Ships: func() []ships.Ship { r.mu.Lock(); defer r.mu.Unlock(); return append([]ships.Ship(nil), r.ships...) },
		Campaigns: func() []follow.Followed {
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]follow.Followed(nil), r.campaigns...)
		},
		Inbox:       func() []inbox.Item { r.mu.Lock(); defer r.mu.Unlock(); return append([]inbox.Item(nil), r.items...) },
		DigestAcked: func(string) uint64 { r.mu.Lock(); defer r.mu.Unlock(); return r.acked },
	}
	r.Routes(r.mux)
	return r
}

func (r *briefRig) set(f func()) { r.mu.Lock(); defer r.mu.Unlock(); f() }

func (r *briefRig) post(t *testing.T, target, body, fleet string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", target, strings.NewReader(body)).WithContext(actor.WithFleet(context.Background(), fleet))
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST %s = %d %s, want 201", target, rec.Code, rec.Body)
	}
	return rec
}

func (r *briefRig) brief(fleet string) string {
	b, _ := os.ReadFile(filepath.Join(r.home, "chat", fleet, "brief.md"))
	return string(b)
}

func (r *briefRig) digest(t *testing.T, fleet string) digest.File {
	t.Helper()
	f, err := digest.Read(r.home, fleet)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func lineTexts(f digest.File) string {
	var out []string
	for _, l := range f.Lines {
		out = append(out, l.Text)
	}
	return strings.Join(out, "\n")
}

func TestABriefHoldsANoteSavedThroughTheActorRoute(t *testing.T) {
	r := newBriefRig(t)
	r.post(t, "/api/chat/notes", `{"text":"deploys go out on Tuesdays"}`, "home")
	r.Tick()
	if got := r.brief("home"); !strings.Contains(got, "deploys go out on Tuesdays") {
		t.Errorf("chat/home/brief.md = %q, want the note", got)
	}
	if got := r.brief("work"); strings.Contains(got, "Tuesdays") {
		t.Errorf("chat/work/brief.md = %q, want no note of home's", got)
	}
}

func TestABriefIsRewrittenOnlyWhenItsTextChanges(t *testing.T) {
	r := newBriefRig(t)
	r.post(t, "/api/chat/notes", `{"text":"first fact"}`, "home")
	r.Tick()
	path := filepath.Join(r.home, "chat", "home", "brief.md")
	if !strings.Contains(r.brief("home"), "first fact") {
		t.Fatalf("brief = %q, want the first note", r.brief("home"))
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	r.Tick()
	if fi, _ := os.Stat(path); !fi.ModTime().Equal(old) {
		t.Errorf("a tick with nothing changed rewrote brief.md (mtime %v)", fi.ModTime())
	}
	r.post(t, "/api/chat/notes", `{"text":"second fact"}`, "home")
	r.Tick()
	if got := r.brief("home"); !strings.Contains(got, "second fact") || !strings.Contains(got, "first fact") {
		t.Errorf("brief after a second note = %q, want both", got)
	}
}

func TestABriefNamesTheShipsAndCampaignsOfItsFleetOnly(t *testing.T) {
	r := newBriefRig(t)
	r.set(func() {
		r.ships = []ships.Ship{
			{Session: "s1", Name: "bot-underway", Fleet: "home", Via: "admiralbot", Status: ships.StatusUnderway},
			{Session: "s2", Name: "mine-needs-you", Fleet: "home", Status: ships.StatusNeedsYou, Reason: "permission"},
			{Session: "s3", Name: "mine-underway", Fleet: "home", Status: ships.StatusUnderway},
			{Session: "s4", Name: "bot-in-work", Fleet: "work", Via: "admiralbot", Status: ships.StatusUnderway},
		}
		r.campaigns = []follow.Followed{
			{ID: "camp-live", URL: "https://github.com/acme/web/issues/777", Fleet: "home"},
			{ID: "camp-ended", URL: "https://github.com/acme/web/issues/888", Fleet: "home", EndedAt: time.Now()},
			{ID: "camp-work", URL: "https://github.com/acme/web/issues/999", Fleet: "work"},
		}
	})
	r.post(t, "/api/chat/notes", `{"text":"anchor"}`, "home")
	r.Tick()
	got := r.brief("home")
	for _, want := range []string{"bot-underway", "mine-needs-you"} {
		if !strings.Contains(got, want) {
			t.Errorf("brief lacks ship %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "camp-live") && !strings.Contains(got, "777") {
		t.Errorf("brief names neither the id nor the issue of the followed campaign:\n%s", got)
	}
	for _, not := range []string{"mine-underway", "bot-in-work", "camp-ended", "888", "camp-work", "999"} {
		if strings.Contains(got, not) {
			t.Errorf("brief holds %q, which is not part of home's brief:\n%s", not, got)
		}
	}
}

func TestABriefHoldsTheFleetsWaitingChatQuestion(t *testing.T) {
	r := newBriefRig(t)
	r.post(t, "/api/chat/questions", `{"title":"Which branch?","questions":[{"id":"q1","text":"Which branch?","multi":false,"options":[{"label":"main"},{"label":"dev"}]}]}`, "home")
	r.Tick()
	if got := r.brief("home"); !strings.Contains(got, "Which branch?") {
		t.Errorf("brief = %q, want AdmiralBot's waiting question", got)
	}
	if got := r.brief("work"); strings.Contains(got, "Which branch?") {
		t.Errorf("work's brief holds home's question: %q", got)
	}
}

func TestShipChangesBecomeDigestLinesForTheShipsFleet(t *testing.T) {
	cases := []struct{ from, to string }{
		{ships.StatusUnderway, ships.StatusSailingOff},
		{ships.StatusUnderway, ships.StatusMoored},
		{ships.StatusUnderway, ships.StatusAnchored},
		{ships.StatusUnderway, ships.StatusNeedsYou},
	}
	for _, via := range []string{"", "admiralbot"} {
		for _, c := range cases {
			t.Run(via+" "+c.from+" to "+c.to, func(t *testing.T) {
				r := newBriefRig(t)
				ship := func(status string) []ships.Ship {
					return []ships.Ship{
						{Session: "s1", Name: "bold-heron", Fleet: "home", Via: via, Status: status},
						{Session: "s2", Name: "other-fleet-ship", Fleet: "work", Status: c.from},
					}
				}
				r.set(func() { r.ships = ship(c.from) })
				r.Tick()
				if f := r.digest(t, "home"); len(f.Lines) != 0 {
					t.Fatalf("the first tick wrote %q: the ships it finds are the baseline, not events", lineTexts(f))
				}
				r.set(func() {
					r.ships = []ships.Ship{
						{Session: "s1", Name: "bold-heron", Fleet: "home", Via: via, Status: c.to},
						{Session: "s2", Name: "other-fleet-ship", Fleet: "work", Status: c.to},
					}
				})
				r.Tick()
				f := r.digest(t, "home")
				if len(f.Lines) != 1 || !strings.Contains(f.Lines[0].Text, "bold-heron") {
					t.Fatalf("home's digest = %q, want one line naming bold-heron", lineTexts(f))
				}
				if strings.Contains(lineTexts(f), "other-fleet-ship") {
					t.Errorf("home's digest holds another fleet's ship")
				}
				if w := r.digest(t, "work"); len(w.Lines) != 1 || !strings.Contains(w.Lines[0].Text, "other-fleet-ship") {
					t.Errorf("work's digest = %q, want its own ship's line", lineTexts(w))
				}
				r.Tick()
				if again := r.digest(t, "home"); len(again.Lines) != 1 {
					t.Errorf("a tick with no change added a line: %q", lineTexts(again))
				}
			})
		}
	}
}

func TestAShipThatKeepsRunningOrIsWokenWritesNoDigestLine(t *testing.T) {
	r := newBriefRig(t)
	r.set(func() {
		r.ships = []ships.Ship{{Session: "s1", Name: "bold-heron", Fleet: "home", Status: ships.StatusMoored}}
	})
	r.Tick()
	r.set(func() {
		r.ships = []ships.Ship{{Session: "s1", Name: "bold-heron", Fleet: "home", Status: ships.StatusUnderway}}
	})
	r.Tick()
	if f := r.digest(t, "home"); len(f.Lines) != 0 {
		t.Errorf("a moored ship woken to underway wrote %q, want nothing", lineTexts(f))
	}
}

func TestANewInboxItemOfTheFleetBecomesADigestLine(t *testing.T) {
	r := newBriefRig(t)
	r.set(func() { r.items = []inbox.Item{{ID: "old", Kind: "question", Session: "s0", Fleet: "home"}} })
	r.Tick()
	if f := r.digest(t, "home"); len(f.Lines) != 0 {
		t.Fatalf("an item already in the Inbox wrote %q, want it as the baseline", lineTexts(f))
	}
	r.set(func() {
		r.items = append(r.items,
			inbox.Item{ID: "new", Kind: "question", Session: "s1", Name: "bold-heron", Fleet: "home"},
			inbox.Item{ID: "elsewhere", Kind: "question", Session: "s2", Fleet: "work"})
	})
	r.Tick()
	if f := r.digest(t, "home"); len(f.Lines) != 1 {
		t.Errorf("home's digest = %q, want one line for the new item", lineTexts(f))
	}
	if f := r.digest(t, "work"); len(f.Lines) != 1 {
		t.Errorf("work's digest = %q, want one line for its own item", lineTexts(f))
	}
	r.Tick()
	if f := r.digest(t, "home"); len(f.Lines) != 1 {
		t.Errorf("a tick with no change added a line: %q", lineTexts(f))
	}
}

func TestTheEngineerAnsweringAChatQuestionBecomesADigestLine(t *testing.T) {
	r := newBriefRig(t)
	rec := r.post(t, "/api/chat/questions", `{"title":"Which branch?","questions":[{"id":"q1","text":"Which branch?","multi":false,"options":[{"label":"main"},{"label":"dev"}]}]}`, "home")
	var q struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	r.Tick()
	if f := r.digest(t, "home"); len(f.Lines) != 0 {
		t.Fatalf("asking wrote %q: only the answer is an event", lineTexts(f))
	}
	path := filepath.Join(r.home, "chat", "home", "questions", inbox.AnswerName(q.ID))
	if err := os.WriteFile(path, []byte(`{"answered":"2026-11-12T10:00:00Z","answers":{"q1":{"selected":["main"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The question cache refreshes on its own clock.
	for end := time.Now().Add(5 * time.Second); len(r.digest(t, "home").Lines) == 0; time.Sleep(50 * time.Millisecond) {
		r.Tick()
		if time.Now().After(end) {
			t.Fatal("within 5s the answer never became a digest line")
		}
	}
	r.Tick()
	if f := r.digest(t, "home"); len(f.Lines) != 1 {
		t.Errorf("digest = %q, want exactly one line for the answer", lineTexts(f))
	}
}

func TestATickDropsTheDigestLinesTheChatHasPrinted(t *testing.T) {
	r := newBriefRig(t)
	if err := digest.Add(r.home, "home", "one", "two"); err != nil {
		t.Fatal(err)
	}
	f := r.digest(t, "home")
	if len(f.Lines) != 2 {
		t.Fatalf("digest = %+v, want two lines", f)
	}
	r.set(func() { r.acked = f.Lines[0].Seq })
	if err := digest.Add(r.home, "home", "three, written after the hook read"); err != nil {
		t.Fatal(err)
	}
	r.Tick()
	got := r.digest(t, "home")
	if len(got.Lines) != 2 || got.Lines[0].Text != "two" || !strings.HasPrefix(got.Lines[1].Text, "three") {
		t.Errorf("digest after the tick = %q, want lines two and three: only those at or below the printed sequence go", lineTexts(got))
	}
	r.set(func() { r.acked = got.Lines[1].Seq })
	r.Tick()
	end := r.digest(t, "home")
	if len(end.Lines) != 0 || end.NextSeq <= got.Lines[1].Seq {
		t.Errorf("emptied digest = %+v, want no lines and next_seq kept above %d", end, got.Lines[1].Seq)
	}
}

func TestABriefListsWaitingQuestionsNewestFirst(t *testing.T) {
	r := newBriefRig(t)
	q := func(title string) string {
		return `{"title":"` + title + `","questions":[{"id":"q","text":"` + title + `","multi":false,"options":[{"label":"a"}]}]}`
	}
	r.post(t, "/api/chat/questions", q("First-asked?"), "home")
	time.Sleep(1100 * time.Millisecond) // question ids carry whole seconds
	r.post(t, "/api/chat/questions", q("Second-asked?"), "home")
	r.Tick()
	got := r.brief("home")
	if a, b := strings.Index(got, "Second-asked?"), strings.Index(got, "First-asked?"); a < 0 || b < 0 || a > b {
		t.Errorf("brief = %q, want the newer question before the older", got)
	}
}

func TestANewChatsBriefHoldsNoQuestionTheStartWithdrew(t *testing.T) {
	l, home, _ := chatRig(t)
	svc := serviceFor(t, l, home)
	svc.Config, svc.Launcher = l.Config, l
	l.BeforeChat = svc.WithdrawQuestions
	svc.Routes(http.NewServeMux())
	fc := svc.chat("work")
	mux := http.NewServeMux()
	svc.Routes(mux)
	req := httptest.NewRequest("POST", "/api/chat/questions", strings.NewReader(`{"title":"Which branch?","questions":[{"id":"q1","text":"Which branch?","multi":false,"options":[{"label":"main"}]}]}`)).WithContext(actor.WithFleet(context.Background(), "work"))
	if rec := httptest.NewRecorder(); func() int { mux.ServeHTTP(rec, req); return rec.Code }() != http.StatusCreated {
		t.Fatal("could not ask the question")
	}
	svc.Tick()
	if b, _ := os.ReadFile(filepath.Join(home, "chat", "work", "brief.md")); !strings.Contains(string(b), "Which branch?") {
		t.Fatalf("brief = %q, want the waiting question before the new chat", b)
	}
	if err := fc.start(); err != nil {
		t.Fatal(err.Msg)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "chat", "work", "brief.md")); strings.Contains(string(b), "Which branch?") || strings.Contains(string(b), "waiting questions") {
		t.Errorf("the new chat's brief = %q, want no questions section: the start withdrew them", b)
	}
}

func TestAShipThatLeavesAndReturnsIsAnEventAgain(t *testing.T) {
	r := newBriefRig(t)
	ship := func(status string) []ships.Ship {
		return []ships.Ship{{Session: "s1", Name: "bold-heron", Fleet: "home", Status: status}}
	}
	r.set(func() { r.ships = ship(ships.StatusUnderway) })
	r.Tick()
	r.set(func() { r.ships = ship(ships.StatusMoored) })
	r.Tick()
	r.set(func() { r.ships = nil })
	r.Tick()
	r.set(func() { r.ships = ship(ships.StatusMoored) })
	r.Tick()
	if f := r.digest(t, "home"); len(f.Lines) != 2 {
		t.Errorf("digest = %q, want a line for each time the ship moored: a session that left is forgotten", lineTexts(f))
	}
}

func TestATickAcksWhatThePaneOptionsSayTheChatPrinted(t *testing.T) {
	r := newBriefRig(t)
	r.Service.DigestAcked = nil
	if err := digest.Add(r.home, "home", "one", "two"); err != nil {
		t.Fatal(err)
	}
	f := r.digest(t, "home")
	fc := r.chat("home")
	fc.mu.Lock()
	fc.st.opts.digest = strconv.FormatUint(f.Lines[0].Seq, 10)
	fc.mu.Unlock()
	r.Tick()
	if got := r.digest(t, "home"); len(got.Lines) != 1 || got.Lines[0].Text != "two" {
		t.Errorf("digest = %q, want only line two after the pane said line one was printed", lineTexts(got))
	}
}

func TestABriefShowsAnOpenCommitmentAndDropsItWhenItIsCancelled(t *testing.T) {
	r := newBriefRig(t)
	path := filepath.Join(r.home, "chat", "home", "ledger.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	append1 := func(line string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	append1(`{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"c-abc123","kind":"commitment","text":"tell me when saltpaw is done","trigger":{"kind":"none"},"action":"remind"}`)
	r.Tick()
	got := r.brief("home")
	if !strings.Contains(got, "## Open commitments") || !strings.Contains(got, "c-abc123") || !strings.Contains(got, "tell me when saltpaw is done") {
		t.Fatalf("chat/home/brief.md = %q, want an Open commitments section with the commitment's id and text", got)
	}
	if strings.Contains(r.brief("work"), "c-abc123") {
		t.Errorf("chat/work/brief.md holds home's commitment")
	}
	append1(`{"v":1,"ts":"2026-11-12T10:00:00Z","event":"cancelled","id":"c-abc123"}`)
	r.Tick()
	if got := r.brief("home"); strings.Contains(got, "c-abc123") || strings.Contains(got, "saltpaw is done") {
		t.Errorf("brief after the cancel = %q, want the commitment gone", got)
	}
}

func TestABriefNamesACampaignCommitmentsCampaignAndCountsItsChildren(t *testing.T) {
	r := newBriefRig(t)
	r.set(func() {
		r.campaigns = []follow.Followed{{ID: "camp-1", URL: "https://github.com/acme/widgets/issues/20", Fleet: "home", Tree: &github.TreeNode{Number: 20, Title: "Ship the harbour", URL: "https://github.com/acme/widgets/issues/20", Repo: "acme/widgets"}}}
	})
	path := filepath.Join(r.home, "chat", "home", "ledger.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"c-camp01","kind":"commitment","text":"work on acme/widgets#20","trigger":{"kind":"campaign","subject":"camp-1"},"action":"work","harness":"claude"}`,
		`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"child_launched","id":"c-camp01","url":"https://github.com/acme/widgets/issues/21","ship":"saltpaw-2"}`,
		`{"v":1,"ts":"2026-11-12T09:02:00Z","event":"child_waiting","id":"c-camp01","url":"https://github.com/acme/widgets/issues/22"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	r.Tick()
	got := r.brief("home")
	for _, want := range []string{"c-camp01", "work on Ship the harbour", "1 launched", "1 waiting"} {
		if !strings.Contains(got, want) {
			t.Errorf("brief = %q, want it to hold %q", got, want)
		}
	}
}
