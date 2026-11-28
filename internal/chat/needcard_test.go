package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	admstate "github.com/jbuffin/admiral/internal/state"
)

// needRig is the Inbox and the chat service wired as Admiral wires them, over one home; fleets home and work have their chat on, off has none.
type needRig struct {
	t      *testing.T
	home   string
	cfg    *config.Config
	kept   *admstate.Store
	pokes  atomic.Int64 // Inbox changes, which is what feeds a push
	in     *inbox.Service
	outc   *outcomes.Writer
	svc    *Service
	mux    *http.ServeMux
	cancel context.CancelFunc
	done   chan struct{}        // closed when the poster has returned
	wire   func(*inbox.Service) // sets the hooks of the Inbox begin builds, before the poster runs
}

var shipFleets = map[string]string{
	"s-home": "home", "s-work": "work", "s-off": "off", "odd ship %?#": "work",
	"_chat-work": "work", "_admiral": "work", "nofleet": "", "s-ghost": "ghost",
}

func newNeedRig(t *testing.T) *needRig {
	t.Helper()
	home := t.TempDir()
	raw, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{
		"home": map[string]any{"name": "Home", "search_roots": []string{home}, "claude": map[string]any{"executable": "/bin/sh"}},
		"work": map[string]any{"name": "Work", "search_roots": []string{home}, "claude": map[string]any{"executable": "/bin/sh"}},
		"off":  map[string]any{"name": "Off", "search_roots": []string{home}},
	}})
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, off, ok := cfg.ChatFleet("off"); !ok || off == "" {
		t.Fatalf("fleet off: chat off reason %q, ok %v; the rig needs it configured with its chat off", off, ok)
	}
	st, err := admstate.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	r := &needRig{t: t, home: home, cfg: cfg, kept: st, outc: outcomes.New(filepath.Join(home, "outcomes"), t.Logf)}
	t.Cleanup(r.outc.Close)
	t.Cleanup(func() { r.stop() })
	return r
}

// start builds the Inbox and the chat service as Admiral wires them and runs the card poster.
func (r *needRig) start() {
	r.t.Helper()
	r.begin()
	r.in.Refresh()
}

// begin is start up to the Inbox's first scan, the order cmd/admiral/main.go runs the poster in.
func (r *needRig) begin() {
	r.t.Helper()
	r.in = &inbox.Service{Home: r.home, Hub: server.NewHub(), Kept: r.kept, Outcomes: r.outc,
		Ship: func(session string) (inbox.ShipInfo, bool) {
			fleet, ok := shipFleets[session]
			return inbox.ShipInfo{Name: session, Harness: "claude", Fleet: fleet, FleetName: fleet}, ok
		}}
	if r.wire != nil {
		r.wire(r.in)
	}
	r.svc = &Service{Home: r.home, Config: func() *config.Config { return r.cfg }, Inbox: r.in}
	r.in.OnChange = func() { r.pokes.Add(1); r.svc.InboxChanged() }
	r.in.OnSettled = r.svc.InboxSettled
	r.mux = http.NewServeMux()
	r.svc.Routes(r.mux)
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	go func() { defer close(r.done); r.svc.RunNeedCards(ctx) }()
}

func (r *needRig) stop() {
	if r.cancel != nil {
		r.cancel()
		<-r.done // a pass still running would write beside the next service or the removed TempDir
		r.cancel = nil
	}
}

// restart is an Admiral restart: new services over the same home.
func (r *needRig) restart() {
	r.t.Helper()
	r.stop()
	r.start()
}

func needQuestion(id, created string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "question", "created": created,
		"title": "Which way?", "order_url": "https://example.com/issues/9",
		"question": map[string]any{"questions": []any{
			map[string]any{"id": "1", "text": "Pick one", "recommended": "Alpha", "multi": false,
				"options": []any{map[string]any{"label": "Alpha", "description": "first"}, map[string]any{"label": "Beta", "description": "second"}}},
			map[string]any{"id": "2", "text": "Pick some", "multi": true,
				"options": []any{map[string]any{"label": "X", "description": "x"}, map[string]any{"label": "Y", "description": "y"}}},
		}}}
}

func (r *needRig) shipDir(session string) string { return filepath.Join(r.home, "inbox", session) }

func (r *needRig) write(session, name string, v any) {
	r.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.MkdirAll(r.shipDir(session), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.shipDir(session), name), b, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// ask writes a question item without telling the Inbox, which sees it on its next Refresh.
func (r *needRig) ask(session, id, created string) {
	r.t.Helper()
	r.write(session, id+".json", needQuestion(id, created))
}

type needCard struct {
	Ship       string           `json:"ship"`
	Item       string           `json:"item"`
	ItemKind   string           `json:"item_kind"`
	Questions  []map[string]any `json:"questions"`
	State      string           `json:"state"`
	AnsweredIn string           `json:"answered_in"`
}

type needEntry struct {
	ID   string
	Text string
	Card needCard
	raw  json.RawMessage
}

func cardsOf(t *testing.T, msgs []Message) []needEntry {
	t.Helper()
	var out []needEntry
	for _, m := range msgs {
		if m.Kind != chatlog.KindNeedCard {
			continue
		}
		e := needEntry{ID: m.ID, Text: m.Text, raw: m.Card}
		if err := json.Unmarshal(m.Card, &e.Card); err != nil {
			t.Fatalf("need card %s: %v\n%s", m.ID, err, m.Card)
		}
		out = append(out, e)
	}
	return out
}

// served is the need cards in the fleet's chat as GET /api/chat/<fleet>/session serves them; the fleet's chat is on.
func (r *needRig) served(fleet string) []needEntry {
	r.t.Helper()
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/chat/"+fleet+"/session", nil))
	var snap Snapshot
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &snap) != nil {
		r.t.Fatalf("GET /api/chat/%s/session = %d %s, want 200 with the snapshot", fleet, rec.Code, rec.Body)
	}
	return cardsOf(r.t, snap.Messages)
}

// servedQuietly is served for another goroutine: it never fails the test and returns nothing on a bad answer.
func (r *needRig) servedQuietly(fleet string) []needEntry {
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/chat/"+fleet+"/session", nil))
	var snap Snapshot
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &snap) != nil {
		return nil
	}
	var out []needEntry
	for _, m := range snap.Messages {
		e := needEntry{ID: m.ID, Text: m.Text}
		if m.Kind == chatlog.KindNeedCard && json.Unmarshal(m.Card, &e.Card) == nil {
			out = append(out, e)
		}
	}
	return out
}

// logged is the need cards in the fleet's chat log, which a fleet with its chat off keeps too.
func (r *needRig) logged(fleet string) []needEntry {
	r.t.Helper()
	es, err := chatlog.New(r.home, time.Now).Recent(fleet, 500)
	if err != nil {
		r.t.Fatal(err)
	}
	var msgs []Message
	for _, e := range es {
		msgs = append(msgs, Message{ID: e.ID, Kind: e.Kind, Text: e.Text, Card: e.Card})
	}
	return cardsOf(r.t, msgs)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("within 10s %s never happened", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// holdCards fails the first time check returns a reason within the window; call it after the test saw what the window starts from.
func holdCards(t *testing.T, window time.Duration, check func() string) {
	t.Helper()
	end := time.Now().Add(window)
	for time.Now().Before(end) {
		if why := check(); why != "" {
			t.Fatal(why)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *needRig) waitLogged(fleet string, n int) []needEntry {
	r.t.Helper()
	var got []needEntry
	waitFor(r.t, "the "+fleet+" chat log holding a need card count of "+string(rune('0'+n)), func() bool { got = r.logged(fleet); return len(got) == n })
	return got
}

// waitState waits until the served card is in the state (and answered_in, when given) and returns it.
func (r *needRig) waitState(fleet, id, state, in string) needEntry {
	r.t.Helper()
	var got needEntry
	waitFor(r.t, "card "+id+" served as "+state+" "+in, func() bool {
		for _, c := range r.served(fleet) {
			if c.ID == id {
				got = c
				return c.Card.State == state && c.Card.AnsweredIn == in
			}
		}
		return false
	})
	return got
}

func (r *needRig) post(fleet, card string, body any) *httptest.ResponseRecorder {
	r.t.Helper()
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/chat/"+fleet+"/need-cards/"+card+"/answer", strings.NewReader(string(b))))
	return rec
}

func tapBeta() map[string]any {
	return map[string]any{"answers": map[string]any{"1": map[string]any{"choice": "Beta"}, "2": map[string]any{"choice": []string{"X"}}}}
}

func (r *needRig) answerFile(session, id string) ([]byte, bool) {
	b, err := os.ReadFile(filepath.Join(r.shipDir(session), id+".answer.json"))
	return b, err == nil
}

var safeCardID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func TestAShipsQuestionPostsOneNeedCardInItsOwnFleetsChatWhetherOrNotThatChatIsOn(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.ask("s-off", "q-2", "2026-11-20T10:01:00Z")
	r.ask("nofleet", "q-3", "2026-11-20T10:02:00Z")
	r.ask("orphan", "q-4", "2026-11-20T10:03:00Z")
	r.start()

	work := r.waitLogged("work", 1)
	off := r.waitLogged("off", 1)
	holdCards(t, 300*time.Millisecond, func() string {
		if n := len(r.logged("home")); n != 0 {
			return "the home chat has a need card for a ship of another fleet"
		}
		if n := len(r.logged("work")) + len(r.logged("off")); n != 2 {
			return "the fleets' chats hold more than one card each for one question each, or a card for the ship in no fleet"
		}
		return ""
	})
	if c := work[0].Card; c.Ship != "s-work" || c.Item != "q-1" {
		t.Errorf("work's card = %+v, want ship s-work and item q-1", c)
	}
	if c := off[0].Card; c.Ship != "s-off" || c.Item != "q-2" {
		t.Errorf("off's card = %+v, want ship s-off and item q-2: a fleet with its chat off still gets its card", c)
	}
}

func TestQuestionsAlreadyOpenAtStartEachGetACardAndARestartPostsNothingTwice(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.ask("s-work", "q-2", "2026-11-20T10:01:00Z")
	r.start()
	before := r.waitLogged("work", 2)

	r.restart()
	r.ask("s-work", "q-3", "2026-11-20T10:02:00Z")
	r.in.Refresh()
	after := r.waitLogged("work", 3)
	holdCards(t, 300*time.Millisecond, func() string {
		if n := len(r.logged("work")); n != 3 {
			return "after a restart the work chat holds a different number of cards than the two it had plus one new question"
		}
		return ""
	})
	seen := map[string]bool{}
	for _, c := range after {
		if seen[c.ID] {
			t.Errorf("card id %s appears twice", c.ID)
		}
		seen[c.ID] = true
	}
	for _, c := range before {
		if !seen[c.ID] {
			t.Errorf("card %s from before the restart is gone; a restart must keep it and post nothing new for it", c.ID)
		}
	}
}

func TestQuestionsOfTheChatAndOfAdmiralPostNoCard(t *testing.T) {
	r := newNeedRig(t)
	r.ask("_chat-work", "q-chat", "2026-11-20T10:00:00Z")
	r.ask("_admiral", "q-admiral", "2026-11-20T10:01:00Z")
	r.start()
	r.ask("s-work", "q-ship", "2026-11-20T10:02:00Z")
	r.in.Refresh()

	cards := r.waitLogged("work", 1)
	holdCards(t, 300*time.Millisecond, func() string {
		if n := len(r.logged("work")); n != 1 {
			return "the work chat holds a card for a question of AdmiralBot's chat or of Admiral itself"
		}
		return ""
	})
	if cards[0].Card.Item != "q-ship" {
		t.Errorf("the one card is for item %q, want the ship's q-ship", cards[0].Card.Item)
	}
}

func TestTheHistoryAndTheSnapshotServeTheCardWithItsItemKindChoicesAndState(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.ask("odd ship %?#", "q-1", "2026-11-20T10:00:00Z")
	r.start()
	r.waitLogged("work", 2)

	want := needQuestion("q-1", "")["question"].(map[string]any)["questions"]
	wantJSON, _ := json.Marshal(want)
	byShip := map[string]needEntry{}
	for _, c := range r.served("work") {
		byShip[c.Card.Ship] = c
		var got any
		_ = json.Unmarshal(mustMarshal(c.Card.Questions), &got)
		var exp any
		_ = json.Unmarshal(wantJSON, &exp)
		if c.Card.Item != "q-1" || c.Card.ItemKind != "question" || c.Card.State != "open" || c.Card.AnsweredIn != "" || !reflect.DeepEqual(got, exp) {
			t.Errorf("card for %q = %+v, want item q-1, item_kind question, state open, no answered_in and the item's questions as the Inbox serves them", c.Card.Ship, c.Card)
		}
		if c.Text != "Which way?" {
			t.Errorf("card text = %q, want the item's title", c.Text)
		}
		if !safeCardID.MatchString(c.ID) {
			t.Errorf("card id %q for ship %q, want one URL path segment of [A-Za-z0-9_-]", c.ID, c.Card.Ship)
		}
	}
	if len(byShip) != 2 {
		t.Fatalf("served cards for ships %v, want one for s-work and one for the odd ship name", byShip)
	}
	if byShip["s-work"].ID == byShip["odd ship %?#"].ID {
		t.Errorf("two ships' questions with one item id share the card id %s", byShip["s-work"].ID)
	}
}

func TestAnsweringThroughTheCardWritesTheAnswerFileAndTheCardReadsAnsweredInTheChat(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.start()
	card := r.waitLogged("work", 1)[0]

	rec := r.post("work", card.ID, tapBeta())
	if rec.Code != http.StatusOK {
		t.Fatalf("POST need card answer = %d %s, want 200", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"answered":"q-1"`) {
		t.Errorf("answer body = %s, want the Inbox's {\"answered\":\"q-1\"}", rec.Body)
	}
	raw, ok := r.answerFile("s-work", "q-1")
	if !ok {
		t.Fatal("the answer file inbox/s-work/q-1.answer.json was not written: the ship would never receive the answer")
	}
	var got struct {
		ID, Kind string
		Answers  map[string]inbox.QuestionAnswer
	}
	if json.Unmarshal(raw, &got) != nil || got.ID != "q-1" || got.Kind != "question" || string(got.Answers["1"].Choice) != `"Beta"` || string(got.Answers["2"].Choice) != `["X"]` {
		t.Errorf("answer file = %s, want question q-1 answered Beta and [X]", raw)
	}
	c := r.waitState("work", card.ID, "answered", "chat")
	if c.Card.Item != "q-1" {
		t.Errorf("answered card = %+v", c.Card)
	}
}

func TestACardOfAFleetWithItsChatOffIsAnsweredThroughItsRoute(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-off", "q-1", "2026-11-20T10:00:00Z")
	r.start()
	card := r.waitLogged("off", 1)[0]

	if rec := r.post("off", card.ID, tapBeta()); rec.Code != http.StatusOK {
		t.Fatalf("POST need card answer in a fleet with its chat off = %d %s, want 200: the route is served whether or not the chat is on", rec.Code, rec.Body)
	}
	if _, ok := r.answerFile("s-off", "q-1"); !ok {
		t.Error("the answer file was not written")
	}
}

func TestAnAnswerGivenAnyOtherWayReadsAnsweredInTheInboxOrRemoved(t *testing.T) {
	for _, tc := range []struct {
		name         string
		act          func(r *needRig)
		state, where string
	}{
		{"answered in the Inbox", func(r *needRig) {
			if err := r.in.AnswerQuestion("s-work", "q-1", inbox.QuestionRequest{Answers: betaAnswers()}); err != nil {
				t.Fatal(err)
			}
		}, "answered", "inbox"},
		{"answered by the ship itself", func(r *needRig) {
			r.write("s-work", "q-1.answer.json", map[string]any{"version": 1, "id": "q-1", "kind": "question", "answered": "2026-11-20T11:00:00Z", "answers": map[string]any{}})
			r.in.Refresh()
		}, "answered", "inbox"},
		{"removed in the Inbox", func(r *needRig) {
			if _, err := r.in.RemoveItem("s-work", "q-1"); err != nil {
				t.Fatal(err)
			}
		}, "removed", ""},
		{"removed by an answer file marked removed", func(r *needRig) {
			r.write("s-work", "q-1.answer.json", map[string]any{"version": 1, "id": "q-1", "kind": "question", "removed": true})
			r.in.Refresh()
		}, "removed", ""},
		{"vanished with no answer file", func(r *needRig) {
			if err := os.Remove(filepath.Join(r.shipDir("s-work"), "q-1.json")); err != nil {
				t.Fatal(err)
			}
			r.in.Refresh()
		}, "removed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNeedRig(t)
			r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
			r.start()
			card := r.waitLogged("work", 1)[0]
			tc.act(r)
			r.waitState("work", card.ID, tc.state, tc.where)
		})
	}
}

func TestACardKeepsItsSettledStateWhenAKeptInboxIsReleased(t *testing.T) {
	for _, tc := range []struct {
		name  string
		act   func(t *testing.T, r *needRig, card string)
		where string
	}{
		{"answered in the Inbox", func(t *testing.T, r *needRig, _ string) {
			if err := r.in.AnswerQuestion("s-work", "q-1", inbox.QuestionRequest{Answers: betaAnswers()}); err != nil {
				t.Fatal(err)
			}
		}, "inbox"},
		{"answered through the card", func(t *testing.T, r *needRig, card string) {
			if rec := r.post("work", card, tapBeta()); rec.Code != 200 {
				t.Fatalf("POST = %d %s", rec.Code, rec.Body)
			}
		}, "chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNeedRig(t)
			if err := r.kept.KeepInbox("s-work", admstate.KeptShip{Name: "s-work", Fleet: "work", Harness: "claude"}); err != nil {
				t.Fatal(err)
			}
			r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
			r.start()
			card := r.waitLogged("work", 1)[0]

			var states []string
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() { // every state the card is served in, from before the answer to well after the release
				defer close(done)
				for {
					select {
					case <-stop:
						return
					default:
					}
					for _, c := range r.servedQuietly("work") {
						if c.ID == card.ID {
							states = append(states, c.Card.State+"/"+c.Card.AnsweredIn)
						}
					}
					time.Sleep(2 * time.Millisecond)
				}
			}()
			tc.act(t, r, card.ID)
			waitFor(t, "the kept inbox's files released", func() bool {
				_, err := os.Stat(r.shipDir("s-work"))
				return os.IsNotExist(err)
			})
			r.waitState("work", card.ID, "answered", tc.where)
			holdCards(t, 200*time.Millisecond, func() string {
				for _, c := range r.served("work") {
					if c.ID == card.ID && (c.Card.State != "answered" || c.Card.AnsweredIn != tc.where) {
						return "the card left answered/" + tc.where + " after the release"
					}
				}
				return ""
			})
			close(stop)
			<-done
			wrong := map[string]bool{"answered/" + map[string]string{"chat": "inbox", "inbox": "chat"}[tc.where]: true, "removed/": true}
			for _, s := range states {
				if wrong[s] {
					t.Errorf("the card was served as %s at some point; states seen in order: %v, want only open and answered/%s", s, states, tc.where)
					break
				}
			}
			r.restart()
			if c := r.waitState("work", card.ID, "answered", tc.where); c.Card.Item != "q-1" {
				t.Errorf("after a restart the card = %+v", c.Card)
			}
		})
	}
}

func TestOnlyTheFirstAnswerCountsAndALateOneChangesNothing(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.ask("s-home", "q-1", "2026-11-20T10:00:00Z")
	r.ask("s-work", "q-2", "2026-11-20T10:01:00Z")
	r.start()
	var work, home, second needEntry
	for _, c := range r.waitLogged("work", 2) {
		if c.Card.Item == "q-1" {
			work = c
		} else {
			second = c
		}
	}
	home = r.waitLogged("home", 1)[0]

	t.Run("a second tap on the same card", func(t *testing.T) {
		if rec := r.post("work", work.ID, tapBeta()); rec.Code != 200 {
			t.Fatalf("first answer = %d %s", rec.Code, rec.Body)
		}
		first, _ := r.answerFile("s-work", "q-1")
		other := map[string]any{"answers": map[string]any{"1": map[string]any{"choice": "Alpha"}, "2": map[string]any{"choice": []string{"Y"}}}}
		rec := r.post("work", work.ID, other)
		if rec.Code != http.StatusConflict || errorOf(rec) != "item q-1 is already answered" {
			t.Errorf("second answer = %d %s, want 409 \"item q-1 is already answered\"", rec.Code, rec.Body)
		}
		if after, _ := r.answerFile("s-work", "q-1"); string(after) != string(first) {
			t.Errorf("the answer file changed: %s, was %s", after, first)
		}
	})
	t.Run("a tap after the Inbox answered the item", func(t *testing.T) {
		if err := r.in.AnswerQuestion("s-work", "q-2", inbox.QuestionRequest{Answers: map[string]inbox.QuestionAnswer{
			"1": {Choice: json.RawMessage(`"Alpha"`)}, "2": {Choice: json.RawMessage(`["Y"]`)}}}); err != nil {
			t.Fatal(err)
		}
		first, _ := r.answerFile("s-work", "q-2")
		rec := r.post("work", second.ID, tapBeta())
		if rec.Code != http.StatusConflict || errorOf(rec) != "item q-2 is already answered" {
			t.Errorf("answer after the Inbox's = %d %s, want 409 \"item q-2 is already answered\"", rec.Code, rec.Body)
		}
		if after, _ := r.answerFile("s-work", "q-2"); string(after) != string(first) {
			t.Errorf("the Inbox's answer file changed: %s, was %s", after, first)
		}
	})
	t.Run("a card named in another fleet's path", func(t *testing.T) {
		if rec := r.post("work", home.ID, tapBeta()); rec.Code != http.StatusNotFound {
			t.Errorf("home's card answered through work's path = %d %s, want 404", rec.Code, rec.Body)
		}
		if _, ok := r.answerFile("s-home", "q-1"); ok {
			t.Error("an answer file was written for a card of another fleet")
		}
		if rec := r.post("nowhere", home.ID, tapBeta()); rec.Code != http.StatusNotFound {
			t.Errorf("an unknown fleet = %d, want 404", rec.Code)
		}
		if rec := r.post("home", "need-nope", tapBeta()); rec.Code != http.StatusNotFound {
			t.Errorf("an unknown card = %d, want 404", rec.Code)
		}
	})
	t.Run("an invalid answer is refused and the card stays open", func(t *testing.T) {
		if rec := r.post("home", home.ID, map[string]any{"answers": map[string]any{"1": map[string]any{"choice": "Gamma"}}}); rec.Code != http.StatusBadRequest {
			t.Errorf("an answer naming no option = %d %s, want the Inbox's 400", rec.Code, rec.Body)
		}
		if _, ok := r.answerFile("s-home", "q-1"); ok {
			t.Error("an invalid answer wrote an answer file")
		}
		if c := r.served("home")[0]; c.Card.State != "open" {
			t.Errorf("card state = %q after a refused answer, want open", c.Card.State)
		}
	})
}

func TestACardNeverAnswersAnItemThatReusesItsSessionAndId(t *testing.T) {
	r := newNeedRig(t)
	if err := r.kept.KeepInbox("s-work", admstate.KeptShip{Name: "s-work", Fleet: "work", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.start()
	old := r.waitLogged("work", 1)[0]
	if err := r.in.AnswerQuestion("s-work", "q-1", inbox.QuestionRequest{Answers: betaAnswers()}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the kept inbox's files released", func() bool { _, err := os.Stat(r.shipDir("s-work")); return os.IsNotExist(err) })

	// The session name is reused and its new ship writes an item with the same id.
	r.ask("s-work", "q-1", "2026-11-22T09:00:00Z")
	r.in.Refresh()
	cards := r.waitLogged("work", 2)
	if cards[0].ID == cards[1].ID {
		t.Fatalf("both questions share the card id %s: a reused session and item id must get its own card", cards[0].ID)
	}
	if rec := r.post("work", old.ID, tapBeta()); rec.Code != http.StatusConflict {
		t.Errorf("the old card's tap on a reused item = %d %s, want 409", rec.Code, rec.Body)
	}
	if _, ok := r.answerFile("s-work", "q-1"); ok {
		t.Error("the old card wrote an answer to the new ship's question")
	}
	newCard := cards[0]
	if newCard.ID == old.ID {
		newCard = cards[1]
	}
	if rec := r.post("work", newCard.ID, tapBeta()); rec.Code != 200 {
		t.Errorf("the new card's tap = %d %s, want 200", rec.Code, rec.Body)
	}
}

func TestPostingCardsWritesNothingInTheInboxAndMarksNothingSeen(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.ask("s-home", "q-1", "2026-11-20T10:01:00Z")
	snapshot := func() map[string]string {
		out := map[string]string{}
		_ = filepath.WalkDir(r.home, func(p string, d os.DirEntry, err error) error {
			rel, _ := filepath.Rel(r.home, p)
			if err == nil && !d.IsDir() && strings.HasPrefix(rel, "inbox") {
				b, _ := os.ReadFile(p)
				out[rel] = string(b)
			}
			return nil
		})
		return out
	}
	before := snapshot()
	listed := func() string { b, _ := json.Marshal(r.in.List()); return string(b) }
	r.start()
	itemsBefore := listed()
	r.waitLogged("work", 1)
	r.waitLogged("home", 1)
	pokes := r.pokes.Load()
	holdCards(t, 200*time.Millisecond, func() string {
		if !reflect.DeepEqual(snapshot(), before) {
			return "posting the cards changed the files under inbox/"
		}
		if listed() != itemsBefore {
			return "posting the cards changed what the Inbox lists"
		}
		if r.pokes.Load() != pokes {
			return "posting the cards changed the Inbox, which is what feeds a push"
		}
		return ""
	})
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func betaAnswers() map[string]inbox.QuestionAnswer {
	return map[string]inbox.QuestionAnswer{"1": {Choice: json.RawMessage(`"Beta"`)}, "2": {Choice: json.RawMessage(`["X"]`)}}
}
