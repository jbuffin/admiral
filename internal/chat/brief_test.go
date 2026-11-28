package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/ships"
)

// briefRig is a Service with fleets home and work (chat on), whose ships and campaigns a test sets.
type briefRig struct {
	*Service
	home string
	mux  *http.ServeMux

	mu        sync.Mutex
	ships     []ships.Ship
	campaigns []follow.Followed
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

func TestABriefHoldsANoteSavedThroughTheActorRoute(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestABriefListsWaitingQuestionsNewestFirst(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestABriefShowsAnOpenCommitmentAndDropsItWhenItIsCancelled(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
