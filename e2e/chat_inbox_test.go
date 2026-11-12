package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// chatInboxItems reads GET /api/inbox and keeps the items of AdmiralBot's chats.
func chatInboxItems(t *testing.T, w *world) []inboxItem {
	t.Helper()
	var out []inboxItem
	for _, it := range w.in.inbox(t) {
		if strings.HasPrefix(it.Session, "_chat-") {
			out = append(out, it)
		}
	}
	return out
}

func waitChatInbox(t *testing.T, w *world, what string, pred func([]inboxItem) bool) []inboxItem {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		items := chatInboxItems(t, w)
		if pred(items) {
			return items
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 10s GET /api/inbox never showed %s; its chat items: %+v", what, items)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func inboxHas(id string) func([]inboxItem) bool {
	return func(items []inboxItem) bool {
		for _, it := range items {
			if it.ID == id {
				return true
			}
		}
		return false
	}
}

func not(p func([]inboxItem) bool) func([]inboxItem) bool {
	return func(items []inboxItem) bool { return !p(items) }
}

// waitInboxWS reads `inbox` messages until one lists the item (want true) or no longer lists it.
func waitInboxWS(t *testing.T, c *websocket.Conn, id string, want bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within 10s the websocket never delivered an `inbox` message with item %s present=%v: %v", id, want, err)
		}
		var m struct {
			Type  string      `json:"type"`
			Items []inboxItem `json:"items"`
		}
		if json.Unmarshal(data, &m) != nil || m.Type != "inbox" {
			continue
		}
		if inboxHas(id)(m.Items) == want {
			return
		}
	}
}

// A fleet's waiting chat question is an Inbox item for that fleet, answered through the
// Inbox route into that fleet's chat only.
func TestChatInbox_ListsAndAnswersAFleetsQuestion(t *testing.T) {
	t.Parallel()
	w, chats := newFleetsWorld(t, chatCue,
		fleetSpec{key: "alpha", model: "opus"},
		fleetSpec{key: "beta", model: "opus"},
		fleetSpec{key: "nodock", codexOnly: true})
	a, b := chats["alpha"], chats["beta"]
	// Two ordinary items around the chat questions in time, from a session that is no ship.
	seedItem(t, filepath.Join(w.home, "inbox", "shipx"), "0-early", questionItem("0-early", "2020-01-01T00:00:00Z"))
	seedItem(t, filepath.Join(w.home, "inbox", "shipx"), "z-late", questionItem("z-late", "2099-01-01T00:00:00Z"))
	w.start()
	ws := dialWS(t, w.in.instance)
	a.start()
	b.start()

	pa := a.w.botStart(a.w.botEnvFor("alpha", "ADMIRAL_PORT="+a.w.in.Port), askStdin("Alpha asks"), "chat", "ask")
	sa := waitFleetQuestions(t, a, pa, 1)
	ida := qStr(sa.Questions[0], "id")
	pb := b.w.botStart(b.w.botEnvFor("beta", "ADMIRAL_PORT="+b.w.in.Port), askStdin("Beta asks"), "chat", "ask")
	sb := waitFleetQuestions(t, b, pb, 1)
	idb := qStr(sb.Questions[0], "id")

	t.Run("each question is an item of its fleet, named AdmiralBot, oldest first with the rest", func(t *testing.T) {
		w := w.at(t)
		waitChatInbox(t, w, "both chat questions", func(items []inboxItem) bool { return inboxHas(ida)(items) && inboxHas(idb)(items) })
		items := w.in.inbox(t)
		var order []string
		for _, it := range items {
			order = append(order, it.ID)
			switch it.ID {
			case ida, idb:
				fleet := map[string]string{ida: "alpha", idb: "beta"}[it.ID]
				if it.Kind != "question" || it.Session != chatSessionFor(fleet) || it.Fleet != fleet || it.FleetName != fleet || it.Name != "AdmiralBot" || it.Title == "" {
					t.Errorf("item %s = %+v, want a question of session %s, fleet %s, named AdmiralBot", it.ID, it, chatSessionFor(fleet), fleet)
				}
			}
		}
		if len(order) != 4 || order[0] != "0-early" || order[3] != "z-late" || order[1] != ida || order[2] != idb {
			t.Errorf("GET /api/inbox lists %v, want 0-early, %s, %s, z-late: oldest first", order, ida, idb)
		}
		for _, s := range w.in.ships(t) {
			if strings.HasPrefix(s.Session, "_chat") {
				t.Errorf("the ships list holds the chat session %s", s.Session)
			}
		}
		waitInboxWS(t, ws, ida, true)
	})

	t.Run("an answer for the wrong fleet's session is refused and leaves the question waiting", func(t *testing.T) {
		w := w.at(t)
		code, body := w.in.answer(t, chatSessionFor("beta"), ida, botAnswer("Freeform"))
		if code != http.StatusNotFound || errMessage(body) == "" {
			t.Errorf("answering alpha's question as beta's = %d %s, want 404 with a reason", code, body)
		}
		if code, body := w.in.answer(t, "_chat-nowhere", ida, botAnswer("Freeform")); code != http.StatusNotFound {
			t.Errorf("answering for a fleet that does not exist = %d %s, want 404", code, body)
		}
		if pa.exited() {
			t.Fatalf("alpha's chat ask ended after answers that were refused")
		}
		if _, err := os.Stat(filepath.Join(a.questionsDir(), ida+".answer.json")); err == nil {
			t.Errorf("an answer file was written for a refused answer")
		}
		if !inboxHas(ida)(chatInboxItems(t, w)) {
			t.Errorf("alpha's question left the inbox after a refused answer")
		}
	})

	t.Run("an answer for a fleet whose chat is off is refused with the reason, as the chat's own route refuses it", func(t *testing.T) {
		w := w.at(t)
		dir := filepath.Join(w.home, "chat", "nodock", "questions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		left := `{"version":1,"id":"left","kind":"question","created":"2020-01-01T00:00:00Z","title":"Left over","question":{"questions":[{"id":"1","text":"Left over","multi":false,"options":[{"label":"Freeform"}]}]}}`
		if err := os.WriteFile(filepath.Join(dir, "left.json"), []byte(left), 0o644); err != nil {
			t.Fatal(err)
		}
		code, body := w.in.answer(t, chatSessionFor("nodock"), "left", botAnswer("Freeform"))
		if code != http.StatusConflict || !strings.Contains(errMessage(body), "chat is off") {
			t.Errorf("answering a chat-off fleet's question = %d %s, want 409 naming that the chat is off", code, body)
		}
		if _, err := os.Stat(filepath.Join(dir, "left.answer.json")); err == nil {
			t.Errorf("an answer file was written for a fleet whose chat is off")
		}
		if inboxHas("left")(chatInboxItems(t, w)) {
			t.Errorf("a chat-off fleet's question is listed in the inbox")
		}
	})

	t.Run("answering through the inbox reaches that fleet's chat only and removes the item", func(t *testing.T) {
		w := w.at(t)
		if code, body := w.in.answer(t, chatSessionFor("alpha"), ida, botAnswer("Work queue")); code/100 != 2 {
			t.Fatalf("POST /api/inbox/%s/%s/answer = %d, want 2xx: %s", chatSessionFor("alpha"), ida, code, body)
		}
		w.expectExit(pa.wait(10*time.Second), 0, "alpha's chat ask after the inbox answer")
		file, err := os.ReadFile(filepath.Join(a.questionsDir(), ida+".answer.json"))
		if err != nil || !strings.Contains(string(file), "Work queue") {
			t.Errorf("alpha's answer file = %q, %v, want the choice Work queue", file, err)
		}
		waitChatInbox(t, w, "alpha's question gone and beta's kept", func(items []inboxItem) bool { return !inboxHas(ida)(items) && inboxHas(idb)(items) })
		waitInboxWS(t, ws, ida, false)
		waitQuestionsOf(t, a, "alpha's answered question", func(s botSession) bool {
			return len(s.Questions) == 1 && string(s.Questions[0]["answer"]) != "null"
		})
		if got := fetchBotFleet(t, b); len(got.Questions) != 1 || string(got.Questions[0]["answer"]) != "null" {
			t.Errorf("beta's question changed after alpha's answer: %+v", got.Questions)
		}
		if pb.exited() {
			t.Errorf("beta's chat ask ended after alpha's answer")
		}
		if code, body := w.in.answer(t, chatSessionFor("alpha"), ida, botAnswer("Freeform")); code != http.StatusConflict {
			t.Errorf("a second answer through the inbox = %d, want 409: %s", code, body)
		}
	})

	t.Run("New chat withdraws the question from the inbox and an answer for it is refused with a reason", func(t *testing.T) {
		w := w.at(t)
		b.at(t).newChat()
		w.expectExit(pb.wait(10*time.Second), 4, "beta's chat ask when New chat withdrew its question")
		waitChatInbox(t, w, "beta's question gone", func(items []inboxItem) bool { return !inboxHas(idb)(items) })
		code, body := w.in.answer(t, chatSessionFor("beta"), idb, botAnswer("Freeform"))
		if code != http.StatusNotFound || errMessage(body) == "" {
			t.Errorf("answering a withdrawn question = %d %s, want 404 with a reason", code, body)
		}
		if names := dirNames(b.questionsDir()); len(names) != 0 {
			t.Errorf("the refused answer left %v in beta's questions", names)
		}
	})
}

// The item's fleet comes from the question's directory, and the old UI's answer call works on it.
func TestChatInbox_FleetComesFromTheDirectory(t *testing.T) {
	t.Parallel()
	w, chats := newFleetsWorld(t, chatCue, fleetSpec{key: "alpha", model: "opus"})
	a := chats["alpha"]
	w.start()
	a.start()
	p := w.botStart(w.botEnvFor("alpha", "ADMIRAL_PORT="+w.in.Port), askStdin("Alpha asks"), "chat", "ask")
	id := qStr(waitFleetQuestions(t, a, p, 1).Questions[0], "id")

	items := waitChatInbox(t, w, "the question", inboxHas(id))
	// The fleet comes from the directory, never from the file `chat ask` wrote.
	raw, err := os.ReadFile(filepath.Join(a.questionsDir(), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]any
	if json.Unmarshal(raw, &onDisk) != nil {
		t.Fatalf("the question file is not JSON: %s", raw)
	}
	if items[0].Fleet != "alpha" || items[0].Session != "_chat-alpha" {
		t.Errorf("item = %+v, want fleet alpha and session _chat-alpha", items[0])
	}
	if _, has := onDisk["fleet"]; has {
		t.Errorf("chat ask wrote a fleet into the question file: %s", raw)
	}
	// The frozen UI answers an item at /api/inbox/<session>/<id>/answer with the item's own session.
	if code, body := w.in.answer(t, items[0].Session, items[0].ID, botAnswer("Freeform")); code/100 != 2 {
		t.Fatalf("answer = %d: %s", code, body)
	}
	w.expectExit(p.wait(10*time.Second), 0, "chat ask after the answer")
	waitChatInbox(t, w, "the answered question gone", not(inboxHas(id)))
}

func waitQuestionsOf(t *testing.T, f *chatDock, what string, pred func(botSession) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !pred(fetchBotFleet(t, f)) {
		if time.Now().After(deadline) {
			t.Fatalf("within 5s GET /api/chat/%s/session never showed %s", f.fleet, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
