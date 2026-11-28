package chat

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/ships"
)

// stopBook is the ships list the rig serves and the stop answers it was asked for.
type stopBook struct {
	mu    sync.Mutex
	list  []ships.Ship
	calls []stopCall
	err   error
}

type stopCall struct {
	Session string
	Answer  ships.StopAnswer
}

func (r *needRig) listShips() []ships.Ship {
	r.stops.mu.Lock()
	defer r.stops.mu.Unlock()
	return slices.Clone(r.stops.list)
}

// setShips replaces the ships the rig lists and tells the poster, as a change in the ships' list does.
func (r *needRig) setShips(list ...ships.Ship) {
	r.stops.mu.Lock()
	r.stops.list = list
	r.stops.mu.Unlock()
	if r.svc != nil {
		r.svc.ShipsChanged()
	}
}

func (r *needRig) answerStop(_ context.Context, session string, a ships.StopAnswer) error {
	r.stops.mu.Lock()
	defer r.stops.mu.Unlock()
	r.stops.calls = append(r.stops.calls, stopCall{session, a})
	return r.stops.err
}

func (r *needRig) failStops(err error) {
	r.stops.mu.Lock()
	defer r.stops.mu.Unlock()
	r.stops.err = err
}

func (r *needRig) stopCalls() []stopCall {
	r.stops.mu.Lock()
	defer r.stops.mu.Unlock()
	return slices.Clone(r.stops.calls)
}

// needing is a ship of the rig's fleets that waits on the prompt stopID for the reason.
func needing(name, fleet, reason, stopID string) ships.Ship {
	return ships.Ship{Session: name, Name: name, Fleet: fleet, Harness: "claude", Status: ships.StatusNeedsYou, Reason: reason,
		StopID: stopID, StopSince: "2026-11-20T10:00:00Z", Launched: "2026-11-20T09:00:00Z", FirstLaunched: "2026-11-20T09:00:00Z"}
}

// resumed is the same ship back at work: no reason, no stop.
func resumed(s ships.Ship) ships.Ship {
	s.Status, s.Reason, s.StopID, s.StopSince = ships.StatusUnderway, "", "", ""
	return s
}

// cardsFor is the cards served in the fleet's chat for the ship, oldest first.
func (r *needRig) cardsFor(fleet, ship string) []needEntry {
	r.t.Helper()
	var out []needEntry
	for _, c := range r.served(fleet) {
		if c.Card.Ship == ship {
			out = append(out, c)
		}
	}
	return out
}

// cardFor waits for the ship's one card and returns it.
func (r *needRig) cardFor(fleet, ship string) needEntry {
	r.t.Helper()
	var got []needEntry
	waitFor(r.t, "a need card for "+ship+" in the "+fleet+" chat", func() bool { got = r.cardsFor(fleet, ship); return len(got) > 0 })
	if len(got) != 1 {
		r.t.Fatalf("%s has %d need cards in the %s chat, want 1", ship, len(got), fleet)
	}
	return got[0]
}

func TestAPermissionPromptPostsOneCardWithItsToolInItsShipsFleet(t *testing.T) {
	r := newNeedRig(t)
	copilot := needing("s-home", "home", "notification:permission_prompt", "p-home")
	copilot.Harness = "copilot"
	r.setShips(needing("s-work", "work", "permission:Bash", "p-work"), copilot)
	r.start()

	c := r.cardFor("work", "s-work")
	if got := c.Card; got.ItemKind != "permission" || got.Item != "p-work" || got.State != NeedOpen || got.Tool != "Bash" || !got.Answerable {
		t.Errorf("work's card = %+v, want an open answerable permission card for tool Bash whose item is the stop id p-work", got)
	}
	if c.Text == "" || !safeCardID.MatchString(c.ID) {
		t.Errorf("card text %q and id %q, want a line of text and one URL path segment of [A-Za-z0-9_-]", c.Text, c.ID)
	}
	h := r.cardFor("home", "s-home")
	if got := h.Card; got.ItemKind != "permission" || got.Item != "p-home" || !got.Answerable {
		t.Errorf("home's card = %+v, want an answerable permission card for Copilot's notification:permission_prompt", got)
	}
	if n := len(r.waitLogged("work", 1)); n != 1 {
		t.Errorf("the work chat holds %d need cards, want only its own ship's", n)
	}
}

func TestAFinishedTurnPostsAStopCardAndAShipNotStartedPostsOneWithNoAnswer(t *testing.T) {
	r := newNeedRig(t)
	r.setShips(needing("s-work", "work", "stop", "t-1"), needing("s-home", "home", "not_started", "n-1"))
	r.start()

	if got := r.cardFor("work", "s-work").Card; got.ItemKind != "stop" || got.Item != "t-1" || got.State != NeedOpen || !got.Answerable {
		t.Errorf("work's card = %+v, want an open answerable stop card with the stop id t-1", got)
	}
	if got := r.cardFor("home", "s-home").Card; got.ItemKind != "not_started" || got.Item != "n-1" || got.State != NeedOpen || got.Answerable {
		t.Errorf("home's card = %+v, want an open not_started card with nothing to answer", got)
	}
}

func TestOtherNeedsYouReasonsAndShipsOutsideAFleetPostNoCard(t *testing.T) {
	r := newNeedRig(t)
	quiet := []ships.Ship{
		needing("q-question", "work", "question", "x1"), needing("q-crew", "work", "crew", "x2"), needing("q-idle", "work", "idle", "x3"),
		needing("q-unknown", "work", "unknown", "x4"), needing("q-bell", "work", "bell", "x5"),
		needing("q-elicit", "work", "notification:elicitation_dialog", "x6"), needing("q-idleprompt", "work", "notification:idle_prompt", "x7"),
		needing("_chat-work", "work", "permission:Bash", "x8"), needing("_admiral", "work", "stop", "x9"), needing("nofleet", "", "permission:Bash", "x10"),
	}
	r.setShips(append(quiet, needing("s-work", "work", "permission:Bash", "p1"))...)
	r.start()

	r.cardFor("work", "s-work")
	holdCards(t, 300*time.Millisecond, func() string {
		if n := len(r.logged("work")); n != 1 {
			return "the work chat holds a need card for a reason no card is posted for, for AdmiralBot's chat, for Admiral itself, or for a ship in no fleet"
		}
		return ""
	})
}

func TestAPermissionPromptAMenuCannotAnswerPostsACardWithNoAnswer(t *testing.T) {
	r := newNeedRig(t)
	codex := needing("q-codex", "work", "permission:WebSearch", "u4")
	codex.Harness = "codex"
	r.setShips(needing("q-ask", "work", "permission:AskUserQuestion", "u1"), needing("q-plan", "work", "permission:ExitPlanMode", "u2"),
		needing("q-bare", "work", "permission", "u3"), codex)
	r.start()

	for _, name := range []string{"q-ask", "q-plan", "q-bare", "q-codex"} {
		if got := r.cardFor("work", name).Card; got.ItemKind != "permission" || got.State != NeedOpen || got.Answerable {
			t.Errorf("%s's card = %+v, want an open permission card with nothing to answer: the menu is answered in the terminal", name, got)
		}
	}
}

func TestAStopWithNoIdentityYetPostsNoCardUntilItHasOne(t *testing.T) {
	r := newNeedRig(t)
	r.setShips(needing("s-work", "work", "permission:Bash", ""))
	r.start()
	r.setShips(needing("s-work", "work", "permission:Bash", ""), needing("s-home", "home", "stop", "t-1"))
	r.cardFor("home", "s-home")
	if n := len(r.logged("work")); n != 0 {
		t.Fatalf("the work chat holds %d cards for a stop with no id, want none", n)
	}
	r.setShips(needing("s-work", "work", "permission:Bash", "p-1"), needing("s-home", "home", "stop", "t-1"))
	if got := r.cardFor("work", "s-work").Card.Item; got != "p-1" {
		t.Errorf("the card's item = %q, want the stop id p-1 the ship got", got)
	}
}

func TestAnIdenticalPromptAfterAnEndedOneGetsItsOwnCardAndARestartPostsNothingTwice(t *testing.T) {
	r := newNeedRig(t)
	first := needing("s-work", "work", "permission:Bash", "p-1")
	r.setShips(first)
	r.start()
	one := r.cardFor("work", "s-work")

	r.setShips(needing("s-work", "work", "permission:Bash", "p-2"))
	var cards []needEntry
	waitFor(t, "a second card for the identical prompt", func() bool { cards = r.cardsFor("work", "s-work"); return len(cards) == 2 })
	if cards[0].ID != one.ID || cards[1].ID == one.ID || cards[1].Card.Item != "p-2" {
		t.Errorf("cards = %+v, want the first card kept and a new one for stop p-2", cards)
	}
	r.waitState("work", one.ID, "settled_terminal", "")

	r.restart()
	r.setShips(needing("s-work", "work", "permission:Bash", "p-2"), needing("s-home", "home", "stop", "t-1"))
	r.cardFor("home", "s-home")
	if n := len(r.logged("work")); n != 2 {
		t.Errorf("after a restart the work chat holds %d cards, want the same two", n)
	}
}

func TestAStopThatEndsWithNoRouteAnsweringItSettlesInTheTerminalOnce(t *testing.T) {
	r := newNeedRig(t)
	ask := needing("s-work", "work", "permission:Bash", "p-1")
	r.setShips(ask, needing("s-home", "home", "stop", "t-1"))
	r.start()
	pa := r.cardFor("work", "s-work")
	stop := r.cardFor("home", "s-home")

	// A permission denied at the terminal turns the prompt into a finished turn, with a new stop.
	r.setShips(needing("s-work", "work", "stop", "t-2"), resumed(needing("s-home", "home", "stop", "t-1")))
	r.waitState("work", pa.ID, "settled_terminal", "")
	r.waitState("home", stop.ID, "settled_terminal", "")
	waitFor(t, "a stop card for the turn the denied prompt became", func() bool { return len(r.cardsFor("work", "s-work")) == 2 })

	// A settled card never reopens, not for the same stop id and not after a restart.
	r.restart()
	r.setShips(ask, needing("s-home", "home", "stop", "t-3"))
	waitFor(t, "the home chat's new card", func() bool { return len(r.cardsFor("home", "s-home")) == 2 })
	if got := r.waitState("work", pa.ID, "settled_terminal", ""); got.Card.AnsweredIn != "" {
		t.Errorf("settled card = %+v, want no answered_in", got.Card)
	}
	if n := len(r.cardsFor("work", "s-work")); n != 2 {
		t.Errorf("the work ship has %d cards after its settled stop id came back, want the same two", n)
	}
}

func TestAStopAnsweredThroughAShipsRouteReadsAnsweredInTheInboxOrViaAdmiralBot(t *testing.T) {
	r := newNeedRig(t)
	a := needing("s-work", "work", "permission:Bash", "p-1")
	b := needing("s-home", "home", "stop", "t-1")
	r.setShips(a, b)
	r.start()
	ca := r.cardFor("work", "s-work")
	cb := r.cardFor("home", "s-home")

	byHand, byBot := resumed(a), resumed(b)
	byHand.StopAnswered = &ships.StopAnswered{StopID: "p-1"}
	byBot.StopAnswered = &ships.StopAnswered{StopID: "t-1", Via: "admiralbot"}
	r.setShips(byHand, byBot)
	r.waitState("work", ca.ID, "answered", "inbox")
	r.waitState("home", cb.ID, "answered", "admiralbot")
}

func TestAShipThatMoorsAnchorsOrLeavesTheListMovesOnItsOpenCards(t *testing.T) {
	r := newNeedRig(t)
	r.setShips(needing("s-moor", "work", "stop", "t-1"), needing("s-anchor", "work", "permission:Bash", "p-1"), needing("s-gone", "work", "stop", "t-2"))
	r.start()
	ids := map[string]string{}
	for _, n := range []string{"s-moor", "s-anchor", "s-gone"} {
		ids[n] = r.cardFor("work", n).ID
	}

	moored, anchored := needing("s-moor", "work", "", ""), needing("s-anchor", "work", "", "")
	moored.Status, anchored.Status = ships.StatusMoored, ships.StatusAnchored
	r.setShips(moored, anchored)
	for _, id := range ids {
		r.waitState("work", id, "moved_on", "")
	}
}

func TestTheStateOfACardSurvivesARestartOfTheServer(t *testing.T) {
	r := newNeedRig(t)
	a := needing("s-work", "work", "stop", "t-1")
	r.setShips(a)
	r.start()
	c := r.cardFor("work", "s-work")
	r.setShips(resumed(a))
	r.waitState("work", c.ID, "settled_terminal", "")

	r.restart()
	r.setShips(a)
	if got := r.waitState("work", c.ID, "settled_terminal", ""); got.Card.ItemKind != "stop" {
		t.Errorf("card after a restart = %+v, want it still settled", got.Card)
	}
}

func TestTappingAllowSendsTheChoiceBoundToTheStopAndTheCardReadsAnsweredInTheChat(t *testing.T) {
	r := newNeedRig(t)
	a := needing("s-work", "work", "permission:Bash", "p-1")
	r.setShips(a)
	r.start()
	c := r.cardFor("work", "s-work")

	// The ship leaves its prompt the instant the keys land, and the next pass may see that before the route returns.
	stop := make(chan struct{})
	var seen sync.Map
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			for _, e := range r.servedQuietly("work") {
				if e.ID == c.ID {
					seen.Store(e.Card.State+"/"+e.Card.AnsweredIn, true)
				}
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	rec := r.post("work", c.ID, map[string]any{"choice": "allow"})
	if rec.Code != http.StatusOK {
		close(stop)
		wg.Wait()
		t.Fatalf("POST allow = %d %s, want 200", rec.Code, rec.Body)
	}
	r.setShips(resumed(a))
	r.waitState("work", c.ID, "answered", "chat")
	close(stop)
	wg.Wait()
	seen.Range(func(k, _ any) bool {
		if k != "open/" && k != "answered/chat" {
			t.Errorf("the card was served as %v while it was answered through the chat, want only open and answered/chat", k)
		}
		return true
	})
	want := []stopCall{{"s-work", ships.StopAnswer{StopID: "p-1", Reason: "permission:Bash", Choice: "allow"}}}
	if got := r.stopCalls(); !slices.Equal(got, want) {
		t.Errorf("stop answers = %+v, want %+v: the choice bound to the stop id and the reason the card was posted for", got, want)
	}
}

func TestDenyingThroughTheCardAnswersItInTheChatAndTheNextPromptGetsItsOwnCard(t *testing.T) {
	r := newNeedRig(t)
	r.setShips(needing("s-work", "work", "permission:Bash", "p-1"))
	r.start()
	c := r.cardFor("work", "s-work")

	if rec := r.post("work", c.ID, map[string]any{"choice": "deny"}); rec.Code != http.StatusOK {
		t.Fatalf("POST deny = %d %s, want 200", rec.Code, rec.Body)
	}
	// Deny leaves the pane at a finished turn with a new stop.
	r.setShips(needing("s-work", "work", "stop", "t-1"))
	r.waitState("work", c.ID, "answered", "chat")
	waitFor(t, "the card for the turn Deny leaves", func() bool { return len(r.cardsFor("work", "s-work")) == 2 })
	if got := r.stopCalls(); len(got) != 1 || got[0].Answer.Choice != "deny" {
		t.Errorf("stop answers = %+v, want one deny", got)
	}
}

func TestSendingTextOnAStopCardTypesItBoundToTheStop(t *testing.T) {
	r := newNeedRig(t)
	r.setShips(needing("s-work", "work", "stop", "t-1"))
	r.start()
	c := r.cardFor("work", "s-work")

	if rec := r.post("work", c.ID, map[string]any{"text": "now run the tests"}); rec.Code != http.StatusOK {
		t.Fatalf("POST text = %d %s, want 200", rec.Code, rec.Body)
	}
	want := []stopCall{{"s-work", ships.StopAnswer{StopID: "t-1", Reason: "stop", Text: "now run the tests"}}}
	if got := r.stopCalls(); !slices.Equal(got, want) {
		t.Errorf("stop answers = %+v, want %+v", got, want)
	}
}

func TestATapOnAStopTheShipHasLeftIsRefusedWithTheItemIsAnsweredMessage(t *testing.T) {
	for name, err := range map[string]error{"the pane moved on": ships.ErrStopMoved, "the ship is gone": ships.ErrNotFound} {
		t.Run(name, func(t *testing.T) {
			r := newNeedRig(t)
			r.setShips(needing("s-work", "work", "permission:Bash", "p-1"))
			r.start()
			c := r.cardFor("work", "s-work")
			r.failStops(err)

			rec := r.post("work", c.ID, map[string]any{"choice": "allow"})
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "item p-1 is already answered") {
				t.Errorf("POST on a stale stop = %d %s, want 409 item p-1 is already answered", rec.Code, rec.Body)
			}
		})
	}
}

func TestAnAnswerTheStopCannotTakeIs400WithTheShipsReason(t *testing.T) {
	r := newNeedRig(t)
	r.setShips(needing("s-work", "work", "stop", "t-1"))
	r.start()
	c := r.cardFor("work", "s-work")
	r.failStops(&ships.StopRefused{Msg: "text cannot be typed into the pane"})

	rec := r.post("work", c.ID, map[string]any{"text": "bad"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "text cannot be typed into the pane") {
		t.Errorf("POST refused by the ship = %d %s, want 400 with the ship's message", rec.Code, rec.Body)
	}
	r.failStops(errors.New("tmux is gone"))
	if rec := r.post("work", c.ID, map[string]any{"text": "again"}); rec.Code != http.StatusInternalServerError {
		t.Errorf("POST failing inside the ship = %d %s, want 500", rec.Code, rec.Body)
	}
}

func TestATapOnASettledCardIsRefusedAndSendsNothing(t *testing.T) {
	r := newNeedRig(t)
	a := needing("s-work", "work", "permission:Bash", "p-1")
	r.setShips(a)
	r.start()
	c := r.cardFor("work", "s-work")
	r.setShips(resumed(a))
	r.waitState("work", c.ID, "settled_terminal", "")

	rec := r.post("work", c.ID, map[string]any{"choice": "allow"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "item p-1 is already answered") {
		t.Errorf("POST on a settled card = %d %s, want 409 item p-1 is already answered", rec.Code, rec.Body)
	}
	if got := r.stopCalls(); len(got) != 0 {
		t.Errorf("the ship was asked %+v for a card already settled, want nothing", got)
	}
}

func TestABodyThatDoesNotFitTheCardIsRefusedAndSendsNothing(t *testing.T) {
	r := newNeedRig(t)
	r.setShips(needing("s-perm", "work", "permission:Bash", "p-1"), needing("s-stop", "work", "stop", "t-1"),
		needing("s-menu", "work", "permission:ExitPlanMode", "m-1"), needing("s-new", "work", "not_started", "n-1"))
	r.start()
	perm, stop, menu, fresh := r.cardFor("work", "s-perm"), r.cardFor("work", "s-stop"), r.cardFor("work", "s-menu"), r.cardFor("work", "s-new")

	for name, c := range map[string]struct {
		card string
		body map[string]any
	}{
		"text on a permission card":       {perm.ID, map[string]any{"text": "hi"}},
		"an unknown choice":               {perm.ID, map[string]any{"choice": "maybe"}},
		"a choice on a stop card":         {stop.ID, map[string]any{"choice": "allow"}},
		"an empty text on a stop card":    {stop.ID, map[string]any{"text": " "}},
		"a choice on a card with no keys": {menu.ID, map[string]any{"choice": "allow"}},
		"anything on a not-started card":  {fresh.ID, map[string]any{"text": "hi"}},
	} {
		if rec := r.post("work", c.card, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, rec.Code, rec.Body)
		}
	}
	if got := r.stopCalls(); len(got) != 0 {
		t.Errorf("the ship was asked %+v for bodies that do not fit their card, want nothing", got)
	}
}
