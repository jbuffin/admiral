package chat

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestARestartSettlesNoCardBeforeTheInboxHasScanned(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.start()
	card := r.waitLogged("work", 1)[0]
	r.stop()

	r.begin() // the poster and the chat are up; the Inbox has not scanned yet
	holdCards(t, 200*time.Millisecond, func() string {
		for _, c := range r.served("work") {
			if c.ID == card.ID && c.Card.State != "open" {
				return "before the first scan the card served as " + c.Card.State
			}
		}
		return ""
	})
	r.in.Refresh()
	r.waitState("work", card.ID, "open", "")
	if rec := r.post("work", card.ID, tapBeta()); rec.Code != 200 {
		t.Fatalf("POST after the restart = %d %s", rec.Code, rec.Body)
	}
	r.waitState("work", card.ID, "answered", "chat")
}

func TestACardRefusesAnItemWhoseQuestionsChangedAfterItWasPosted(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.start()
	card := r.waitLogged("work", 1)[0]

	changed := needQuestion("q-1", "2026-11-20T10:00:00Z")
	changed["question"].(map[string]any)["questions"].([]any)[0].(map[string]any)["text"] = "Something else entirely"
	r.write("s-work", "q-1.json", changed)
	r.in.Refresh()

	if rec := r.post("work", card.ID, tapBeta()); rec.Code != 409 {
		t.Fatalf("POST for an item whose text changed = %d %s, want 409", rec.Code, rec.Body)
	}
	if _, ok := r.answerFile("s-work", "q-1"); ok {
		t.Error("an answer file was written for questions the engineer never saw")
	}
}

func TestATapOnAnItemThatIsGoneAnswers409(t *testing.T) {
	r := newNeedRig(t)
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.start()
	card := r.waitLogged("work", 1)[0]
	r.stop() // no poster pass settles the card before the tap

	if err := os.RemoveAll(r.shipDir("s-work")); err != nil {
		t.Fatal(err)
	}
	if rec := r.post("work", card.ID, tapBeta()); rec.Code != 409 {
		t.Fatalf("POST for a vanished item = %d %s, want 409", rec.Code, rec.Body)
	}
}

func TestAnUnparseableCardRecordFileIsKeptAside(t *testing.T) {
	r := newNeedRig(t)
	path := filepath.Join(r.home, "need-cards.json")
	if err := os.WriteFile(path, []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.ask("s-work", "q-1", "2026-11-20T10:00:00Z")
	r.start()
	r.waitLogged("work", 1)
	waitFor(t, "the damaged file moved aside", func() bool {
		b, err := os.ReadFile(path + ".bad")
		return err == nil && string(b) == "{torn"
	})
}
