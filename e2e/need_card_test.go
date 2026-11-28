package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type needCardMsg struct {
	ID   string
	Kind string
	Card struct {
		Ship       string
		Item       string
		ItemKind   string `json:"item_kind"`
		State      string
		AnsweredIn string `json:"answered_in"`
		Tool       string
		Answerable bool
	}
}

func (w *world) needCards(fleet string) []needCardMsg {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/chat/"+fleet+"/session", nil)
	var snap struct{ Messages []needCardMsg }
	if code != http.StatusOK || json.Unmarshal([]byte(body), &snap) != nil {
		w.t.Fatalf("GET chat session of %s = %d %s, want 200", fleet, code, body)
	}
	var out []needCardMsg
	for _, m := range snap.Messages {
		if m.Kind == "need_card" {
			out = append(out, m)
		}
	}
	return out
}

func (w *world) waitNeedCard(fleet, state, in string) needCardMsg {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if cs := w.needCards(fleet); len(cs) == 1 && cs[0].Card.State == state && cs[0].Card.AnsweredIn == in {
			return cs[0]
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 10s the %s chat never held one need card %s %s (it has %+v)", fleet, state, in, w.needCards(fleet))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A ship's question posts a card in its fleet's chat; answering through the card writes the answer the ship reads and settles the card.
func TestANeedCardAnswersAShipsQuestionFromTheChat(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.resumeConfig(map[string][]string{"f": {"record", "hook working", "wait go", "event Stop {}"}})
	w.start()
	const text = "/commodore resume run-1"
	ship, _, dir := w.resumeShip("card1", text, []string{"q-1"}, "q-1")
	w.waitFile(ship.Session, "recording")

	card := w.waitNeedCard("f", "open", "")
	if card.Card.Ship != ship.Session || card.Card.Item != "q-1" || card.Card.ItemKind != "question" {
		t.Fatalf("card = %+v, want ship %s, item q-1, item_kind question", card.Card, ship.Session)
	}

	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/f/need-cards/"+card.ID+"/answer", answerBody())
	if !ok2xx(code) {
		t.Fatalf("POST need card answer = %d %s, want 2xx", code, body)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "q-1.answer.json"))
	if err != nil {
		t.Fatalf("the answer file was not written: %v", err)
	}
	var ans struct {
		ID      string
		Kind    string
		Answers map[string]struct{ Choice json.RawMessage }
	}
	if json.Unmarshal(raw, &ans) != nil || ans.ID != "q-1" || string(ans.Answers["1"].Choice) != `"Alpha"` || string(ans.Answers["2"].Choice) != `["X"]` {
		t.Errorf("answer file = %s, want q-1 answered Alpha and [X]", raw)
	}
	w.waitNeedCard("f", "answered", "chat")

	w.touch(ship.Session, "go") // the turn ends: Stop
	w.waitStdin(t, ship.Session, text+"\r")
}

// A permission prompt posts a card; the prompt is cleared in the terminal, the card reads Settled in the terminal, and a late tap is refused with nothing sent to the pane.
func TestAPermissionCardSettlesWhenThePromptIsClearedInTheTerminalAndALateTapSendsNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.resumeConfig(map[string][]string{"f": {"record", "hook working", "wait go0", "event PermissionRequest " + permAsk, "wait clear", "event PostToolUse " + permRan, "wait end"}})
	w.start()
	ship := w.launchIn("f", "claude", "stopcard")
	w.waitFile(ship.Session, "recording")
	w.waitPaneWorking(shipPaneOf(w, ship.Session))
	w.touch(ship.Session, "go0")

	card := w.waitNeedCard("f", "open", "")
	if c := card.Card; c.Ship != ship.Session || c.ItemKind != "permission" || c.Tool != "Bash" || !c.Answerable || c.Item == "" {
		t.Fatalf("card = %+v, want an answerable permission card for Bash on %s with a stop id", c, ship.Session)
	}

	w.touch(ship.Session, "clear") // the engineer answers in the terminal: the ship runs the tool and works again
	settled := w.waitNeedCard("f", "settled_terminal", "")
	if settled.ID != card.ID {
		t.Errorf("settled card %s, want the one posted, %s", settled.ID, card.ID)
	}

	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/chat/f/need-cards/"+card.ID+"/answer", `{"choice":"allow"}`)
	if code != http.StatusConflict || !strings.Contains(body, "item "+card.Card.Item+" is already answered") {
		t.Errorf("a late tap = %d %s, want 409 item %s is already answered", code, body, card.Card.Item)
	}
	if got := w.stdinOf(ship.Session); got != "" {
		t.Errorf("the pane received %s after the late tap, want nothing", quote(got))
	}
}
