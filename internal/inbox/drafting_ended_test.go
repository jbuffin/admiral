package inbox_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
)

func draftingEnded() inbox.DraftingEnded {
	return inbox.DraftingEnded{Ship: "bramble", Name: "Bramble", Fleet: "work", Harness: "claude",
		Transcript: "1700000000000000000 launch /cfg/projects/p/s.jsonl", StartCmd: "claude --session-id s", ConfigDir: "/cfg"}
}

func getConversation(mux http.Handler, id string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/inbox/_admiral/"+id+"/conversation", nil))
	return rec
}

func TestAnEndedDraftingItemServesItsShipsConversationUntilTheItemIsGone(t *testing.T) {
	s, home, mux := actorInbox(t)
	var asked []inbox.DraftingEnded
	s.DraftingConversation = func(d inbox.DraftingEnded, query string) (int, []byte) {
		asked = append(asked, d)
		return http.StatusOK, []byte(`{"state":"ok","items":[]}`)
	}
	id, err := inbox.WriteDraftingEnded(home, draftingEnded())
	if err != nil {
		t.Fatalf("WriteDraftingEnded = %v", err)
	}

	rec := getConversation(mux, id)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"ok"`) {
		t.Fatalf("conversation = %d %s, want 200 with the reader's page", rec.Code, rec.Body)
	}
	if len(asked) != 1 || asked[0].Transcript != draftingEnded().Transcript || asked[0].StartCmd != draftingEnded().StartCmd {
		t.Errorf("the reader was given %+v, want the binding recorded when the ship stopped", asked)
	}

	if rec := asActor(mux, "", http.MethodPost, "/api/inbox/_admiral/"+id+"/remove", ""); rec.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	if rec := getConversation(mux, id); rec.Code != http.StatusNotFound {
		t.Errorf("conversation of a removed item = %d %s, want 404", rec.Code, rec.Body)
	}
	if rec := getConversation(mux, "no-such-item"); rec.Code != http.StatusNotFound {
		t.Errorf("conversation of an unknown item = %d, want 404", rec.Code)
	}
}

func TestAnEndedDraftingItemIsListedWithItsShipAndFleet(t *testing.T) {
	s, home, mux := actorInbox(t)
	if _, err := inbox.WriteDraftingEnded(home, draftingEnded()); err != nil {
		t.Fatalf("WriteDraftingEnded = %v", err)
	}
	s.Refresh()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "ended without filing a campaign") || !strings.Contains(body, "Bramble") {
		t.Errorf("inbox = %d %s, want the item titled for Bramble", rec.Code, body)
	}
	if strings.Contains(body, "start_cmd") || strings.Contains(body, "/cfg/projects") {
		t.Errorf("inbox = %s, want the conversation binding kept from clients", body)
	}
}
