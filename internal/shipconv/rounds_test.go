package shipconv

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/ships"
)

type rItem struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	At    string `json:"at"`
	Round int    `json:"round"`
	State string `json:"state"`
}

type rPage struct {
	State string  `json:"state"`
	Items []rItem `json:"items"`
	More  bool    `json:"more"`
}

// rounds gives a fixture ships that have a history of rounds.
type rounds struct {
	f     *fixture
	hist  map[string][]moor.Round // by ship name
	moors map[string]ships.Ship   // moored ships, by session
	live  map[string]int          // a live ship's round, by session
}

func withRounds(f *fixture) *rounds {
	r := &rounds{f: f, hist: map[string][]moor.Round{}, moors: map[string]ships.Ship{}, live: map[string]int{}}
	f.svc.moored = func(session string) (ships.Ship, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		sh, ok := r.moors[session]
		return sh, ok
	}
	f.svc.rounds = func(sh ships.Ship) ([]moor.Round, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return append([]moor.Round(nil), r.hist[sh.Name]...), nil
	}
	f.svc.fleetDir = func(string) (string, bool) { return f.cfg, true }
	f.svc.liveRound = func(sh ships.Ship) int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return r.live[sh.Session]
	}
	return r
}

func rsid(n int) string { return fmt.Sprintf("%08x-0000-4000-8000-%012x", n+1, n+1) }

var t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func userAt(id, text string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "uuid": id, "timestamp": "2026-01-02T03:04:05.000Z",
		"message": map[string]any{"role": "user", "content": text}})
	return string(b) + "\n"
}

// round writes round n's transcript (one user line per text, with ids u-001, u-002, ...) and returns its history entry.
func (r *rounds) round(n int, texts ...string) moor.Round {
	path := filepath.Join(r.f.cfg, "projects", "zz", rsid(n)+".jsonl")
	for i, text := range texts {
		appendTo(r.f.t, path, userAt(fmt.Sprintf("u-%03d", i+1), text))
	}
	return moor.Round{Round: n, SessionID: rsid(n), Fleet: "work", Transcript: path,
		Started: t0.Add(time.Duration(n) * time.Hour), Ended: t0.Add(time.Duration(n)*time.Hour + time.Minute)}
}

// moored adds a moored ship whose name is its session's with -name added.
func (r *rounds) moored(session, harness string, history ...moor.Round) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	r.moors[session] = ships.Ship{Session: session, Name: session + "-name", Harness: harness, Fleet: "work", Status: ships.StatusMoored}
	r.hist[session+"-name"] = history
}

func (r *rounds) get(session, query string) (int, rPage, string) {
	r.f.t.Helper()
	mux := http.NewServeMux()
	r.f.svc.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships/"+session+"/conversation"+query, nil))
	body, _ := io.ReadAll(rec.Result().Body)
	var p rPage
	_ = json.Unmarshal(body, &p)
	return rec.Code, p, string(body)
}

func ids(p rPage) []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.ID)
	}
	return out
}

func TestAMooredShipServesItsRoundsInOrderWithADividerOpeningEachLaterRound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	r0, r1 := r.round(0, "zero a", "zero b"), r.round(1, "one a", "one b")
	r.moored("m", "claude", r0, r1)

	code, p, body := r.get("m", "")
	if code != 200 || p.State != "ok" || p.More {
		t.Fatalf("GET = %d %s, want 200, ok, no more (a moored ship is served from its history, not 404)", code, body)
	}
	if want := []string{"u-001", "u-002", "r1", "r1:u-001", "r1:u-002"}; !reflect.DeepEqual(ids(p), want) {
		t.Fatalf("ids = %v, want %v: round 0 raw and with no divider, a divider r1, then round 1's items prefixed r1:", ids(p), want)
	}
	if d := p.Items[2]; d.Kind != "round" || d.Round != 1 || d.State != "" {
		t.Errorf("divider = %+v, want kind round, round 1 and no state", d)
	}
	if at, err := time.Parse(time.RFC3339Nano, p.Items[2].At); err != nil || !at.Equal(r1.Started) {
		t.Errorf("divider at = %q (%v), want the round's start %s", p.Items[2].At, err, r1.Started)
	}
	if p.Items[3].Text != "one a" || p.Items[1].Text != "zero b" {
		t.Errorf("texts = %q, %q, want round 1's first item and round 0's second", p.Items[3].Text, p.Items[1].Text)
	}
}

func TestALiveShipInALaterRoundServesTheRecordedRoundsThenItsOwn(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	r0, r1 := r.round(0, "zero a"), r.round(1, "one a")
	r.hist["live-name"] = []moor.Round{r0, r1}
	appendTo(t, f.path("zz"), userAt("u-001", "two a"))
	f.ship("live", f.path("zz"))
	f.mu.Lock()
	r.live["live"] = 2
	f.mu.Unlock()
	f.svc.ship = func(session string) (ships.Ship, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		h, ok := f.harness[session]
		return ships.Ship{Session: session, Name: session + "-name", Harness: h, Launched: "L1", Fleet: "work"}, ok
	}

	code, p, body := r.get("live", "")
	want := []string{"u-001", "r1", "r1:u-001", "r2", "r2:u-001"}
	if code != 200 || p.State != "ok" || !reflect.DeepEqual(ids(p), want) {
		t.Errorf("GET = %d %s, want ids %v: the recorded rounds, then a divider r2 and the live round's items prefixed r2:", code, body, want)
	}
}

func TestPagingReadsTheNewestRoundFirstAndRoutesBeforeToTheRoundItNames(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	var many []string
	for i := 1; i <= 250; i++ {
		many = append(many, fmt.Sprintf("one %03d", i))
	}
	var few []string
	for i := 1; i <= 30; i++ {
		few = append(few, fmt.Sprintf("zero %03d", i))
	}
	r.moored("m", "claude", r.round(0, few...), r.round(1, many...))

	_, newest, body := r.get("m", "")
	if newest.State != "ok" || len(newest.Items) != 200 || !newest.More || newest.Items[0].ID != "r1:u-051" || newest.Items[199].ID != "r1:u-250" {
		t.Fatalf("newest page = %.200s, want 200 items r1:u-051..r1:u-250 and more", body)
	}
	_, older, body := r.get("m", "?before="+url.QueryEscape("r1:u-051"))
	if older.State != "ok" || older.More || len(older.Items) != 81 || older.Items[0].ID != "u-001" || older.Items[29].ID != "u-030" ||
		older.Items[30].ID != "r1" || older.Items[31].ID != "r1:u-001" || older.Items[80].ID != "r1:u-050" {
		t.Errorf("page before r1:u-051 = %.300s, want round 0's 30 items, the divider r1, then r1:u-001..050, and no more", body)
	}
	_, inZero, body := r.get("m", "?before=u-020")
	if inZero.State != "ok" || inZero.More || len(inZero.Items) != 19 || inZero.Items[0].ID != "u-001" || inZero.Items[18].ID != "u-019" {
		t.Errorf("page before u-020 = %.200s, want round 0's u-001..019 and no more", body)
	}
	if code, _, body := r.get("m", "?before="+url.QueryEscape("r1:nope")); code != 400 {
		t.Errorf("before an unknown id in round 1 = %d %s, want 400", code, body)
	}
}

func TestARawRoundZeroIdThatLooksPrefixedIsRoundZeroWhenThatRoundIsNotInTheHistory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	path := filepath.Join(f.cfg, "projects", "zz", rsid(0)+".jsonl")
	appendTo(t, path, userAt("u-1", "first")+userAt("r5:x", "second")+userAt("u-3", "third"))
	r0 := moor.Round{Round: 0, SessionID: rsid(0), Fleet: "work", Transcript: path, Started: t0, Ended: t0.Add(time.Minute)}
	r.moored("m", "claude", r0, r.round(1, "one a"))

	code, p, body := r.get("m", "?before="+url.QueryEscape("r5:x"))
	if code != 200 || p.State != "ok" || p.More || !reflect.DeepEqual(ids(p), []string{"u-1"}) {
		t.Errorf("page before r5:x = %d %s, want round 0's u-1 alone: round 5 is not in the history, so the id is round 0's", code, body)
	}
}

func TestARoundThatCannotBeShownKeepsItsDividerAndNeverFailsThePage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	outside := filepath.Join(t.TempDir(), rsid(3)+".jsonl")
	if err := os.WriteFile(outside, []byte(userAt("u-001", "outside")), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(f.cfg, "projects", "zz", rsid(3)+".jsonl")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	none := r.round(1)
	none.Transcript = ""
	gone := r.round(2)
	gone.Transcript = filepath.Join(f.cfg, "projects", "zz", rsid(2)+".jsonl")
	unreadable := r.round(3)
	r.moored("m", "claude", r.round(0, "zero a"), none, gone, unreadable, r.round(4, "four a"))

	code, p, body := r.get("m", "")
	want := []string{"u-001", "r1", "r2", "r3", "r4", "r4:u-001"}
	if code != 200 || p.State != "ok" || !reflect.DeepEqual(ids(p), want) {
		t.Fatalf("GET = %d %s, want ids %v", code, body, want)
	}
	for i, state := range map[int]string{1: "none", 2: "gone", 3: "unreadable", 4: ""} {
		if d := p.Items[i]; d.Kind != "round" || d.Round != i || d.State != state {
			t.Errorf("divider of round %d = %+v, want kind round and state %q", i, d, state)
		}
	}
}

func TestAMooredShipOfAnotherHarnessAnswersOkWithItsDividersOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	r0, r1 := r.round(0), r.round(1)
	r0.Transcript, r1.Transcript = "", ""
	r.moored("m", "codex", r0, r1)

	code, p, body := r.get("m", "")
	if code != 200 || p.State != "ok" || !reflect.DeepEqual(ids(p), []string{"r1"}) || p.Items[0].State != "none" {
		t.Errorf("GET = %d %s, want ok with the one divider r1 in state none", code, body)
	}
	if want := r1.Started.Format(time.RFC3339Nano); p.Items[0].At != want {
		t.Errorf("divider r1 at = %q, want the round's started %q", p.Items[0].At, want)
	}
}

func TestAMooredShipWithNothingToShowAnswersNone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	r.moored("m", "claude")
	r0 := r.round(0)
	r0.Transcript = ""
	r.moored("c", "codex", r0)

	for _, session := range []string{"m", "c"} {
		code, p, body := r.get(session, "")
		if code != 200 || p.State != "none" || len(p.Items) != 0 {
			t.Errorf("GET %s = %d %s, want 200 none with no items (no history, or only an undivided round 0)", session, code, body)
		}
	}
}

func TestALiveRoundWhoseOptionIsNotBoundYetHasADividerWithNoState(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	r.hist["live-name"] = []moor.Round{r.round(0, "zero a")}
	f.ship("live", f.path("zz"))
	f.mu.Lock()
	f.options["live"] = ""
	r.live["live"] = 1
	f.mu.Unlock()
	f.svc.ship = func(session string) (ships.Ship, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		h, ok := f.harness[session]
		return ships.Ship{Session: session, Name: session + "-name", Harness: h, Launched: "L1", Fleet: "work"}, ok
	}

	_, p, body := r.get("live", "")
	if !reflect.DeepEqual(ids(p), []string{"u-001", "r1"}) || p.Items[1].State != "" {
		t.Errorf("GET = %s, want the divider r1 with no state", body)
	}
}

func TestAMooredShipAcksAnOpenAndWatchesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	r0 := r.round(0, "zero a")
	r.moored("m", "claude", r0)
	sub := dial(t, serve(t, f))

	sub.send("conversation_open", "m")
	sub.opened("m")
	appendTo(t, r0.Transcript, userAt("u-002", "zero b"))
	sub.quiet("a moored ship's file, which no round writes any more")
}

func TestPushesFromALiveRoundCarryItsPrefix(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := withRounds(f)
	r.hist["live-name"] = []moor.Round{r.round(0, "zero a")}
	appendTo(t, f.path("zz"), lines(1, 3))
	f.ship("live", f.path("zz"))
	f.mu.Lock()
	r.live["live"] = 1
	f.mu.Unlock()
	f.svc.ship = func(session string) (ships.Ship, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		h, ok := f.harness[session]
		return ships.Ship{Session: session, Name: session + "-name", Harness: h, Launched: "L1", Fleet: "work"}, ok
	}
	sub := dial(t, serve(t, f))

	sub.send("conversation_open", "live")
	sub.opened("live")
	appendTo(t, f.path("zz"), lines(4, 4))
	m := sub.next("the appended item")
	items, _ := m["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "r1:u-004" {
		t.Errorf("pushed %v, want the one item with id r1:u-004", m["items"])
	}
}
