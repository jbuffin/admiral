package inbox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
)

// draftHooks records what the item actions hand to the campaign creator and the chat.
type draftHooks struct {
	mu      sync.Mutex
	created []inbox.CampaignDraft
	cards   []inbox.CampaignCard
	fail    func() (int, string)
}

func (h *draftHooks) create(_ context.Context, d inbox.CampaignDraft) (inbox.CreatedCampaign, int, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.created = append(h.created, d)
	if h.fail != nil {
		if status, msg := h.fail(); status != 0 {
			return inbox.CreatedCampaign{}, status, msg
		}
	}
	return inbox.CreatedCampaign{ID: "camp-1", URL: "https://github.com/acme/widgets/issues/99"}, 0, ""
}

func (h *draftHooks) card(_ string, c inbox.CampaignCard) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cards = append(h.cards, c)
}

func (h *draftHooks) snapshot() ([]inbox.CampaignDraft, []inbox.CampaignCard) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]inbox.CampaignDraft(nil), h.created...), append([]inbox.CampaignCard(nil), h.cards...)
}

// draftInbox is an inbox whose fleet work (Work) holds acme/widgets and acme/api, served as the engineer at url and as AdmiralBot through mux.
func draftInbox(t *testing.T) (*inbox.Service, string, string, *http.ServeMux, *draftHooks) {
	t.Helper()
	s, home, mux := actorInbox(t)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.Fleet = func(key string) (inbox.FleetInfo, bool) {
		return inbox.FleetInfo{Key: key, Name: "Work", Color: "#38bdf8"}, key == "work"
	}
	s.FleetRepos = func(fleet string) []string {
		if fleet == "work" {
			return []string{"acme/widgets", "acme/api"}
		}
		return nil
	}
	h := &draftHooks{}
	s.CreateCampaign, s.PostCampaignCard = h.create, h.card
	return s, home, srv.URL, mux, h
}

const draftBody = `{"repo":"acme/widgets","title":"Speed up the fleet map","body":"Load the map in under a second."}`

func draftAsBot(t *testing.T, mux *http.ServeMux, body string) string {
	t.Helper()
	rec := asActor(mux, "work", http.MethodPost, "/api/fleets/work/campaign-drafts", body)
	var out struct{ ID string }
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.ID == "" {
		t.Fatalf("draft = %d %s, want 201 with the item's id", rec.Code, rec.Body)
	}
	return out.ID
}

func TestADraftFromAdmiralBotIsAnAdmiralItemCarryingTheDraftAndTheViaMarkerAndTouchesNoGitHub(t *testing.T) {
	s, _, _, mux, h := draftInbox(t)
	id := draftAsBot(t, mux, draftBody)

	items := listed(t, s)
	if got := idsOf(items); !reflect.DeepEqual(got, []string{id}) {
		t.Fatalf("items = %v, want the one draft", got)
	}
	it := items[0]
	if it["kind"] != "campaign_draft" || it["title"] != "AdmiralBot drafted a campaign" || it["session"] != inbox.AdmiralSession || it["fleet"] != "work" || it["fleet_name"] != "Work" {
		t.Errorf("item = %v, want kind campaign_draft titled \"AdmiralBot drafted a campaign\" in Admiral's own inbox, fleet work", it)
	}
	want := map[string]any{"fleet": "work", "repo": "acme/widgets", "title": "Speed up the fleet map", "body": "Load the map in under a second.", "via": "admiralbot"}
	if got, _ := it["campaign_draft"].(map[string]any); !reflect.DeepEqual(got, want) {
		t.Errorf("campaign_draft = %v, want %v", got, want)
	}
	if created, cards := h.snapshot(); len(created) != 0 || len(cards) != 0 {
		t.Errorf("drafting filed %v and posted %v; it must create no campaign and post no card", created, cards)
	}
}

func TestADraftNeverExpires(t *testing.T) {
	s, home, _, mux, _ := draftInbox(t)
	id := draftAsBot(t, mux, draftBody)
	path := filepath.Join(home, "inbox", inbox.AdmiralSession, id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatal(err)
	}
	item["created"] = "2020-01-01T00:00:00Z"
	writeItem(t, filepath.Dir(path), filepath.Base(path), item)
	if got := idsOf(listed(t, s)); !reflect.DeepEqual(got, []string{id}) {
		t.Errorf("items = %v, want the draft still listed years on", got)
	}
}

func TestADraftForARepoOutsideTheChatsFleetIsRefusedAndKeptNowhere(t *testing.T) {
	s, _, _, mux, _ := draftInbox(t)
	rec := asActor(mux, "work", http.MethodPost, "/api/fleets/work/campaign-drafts", `{"repo":"acme/elsewhere","title":"T","body":"B"}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "acme/elsewhere") {
		t.Errorf("draft = %d %s, want 422 naming the repo", rec.Code, rec.Body)
	}
	if got := idsOf(listed(t, s)); len(got) != 0 {
		t.Errorf("items = %v, want none after a refused draft", got)
	}
}

func TestADraftWithoutATitleOrARepoIsRefused(t *testing.T) {
	s, _, _, mux, _ := draftInbox(t)
	for _, body := range []string{`{"repo":"acme/widgets","title":"  ","body":"B"}`, `{"title":"T","body":"B"}`} {
		if rec := asActor(mux, "work", http.MethodPost, "/api/fleets/work/campaign-drafts", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: draft = %d %s, want 400", body, rec.Code, rec.Body)
		}
	}
	if got := idsOf(listed(t, s)); len(got) != 0 {
		t.Errorf("items = %v, want none", got)
	}
}

func TestCreatingFromADraftFilesTheEngineersEditsInTheDraftsFleetRemovesTheItemAndPostsTheCardOnce(t *testing.T) {
	s, _, url, mux, h := draftInbox(t)
	id := draftAsBot(t, mux, draftBody)

	code, body := post(t, url+"/api/inbox/_admiral/"+id+"/launch", `{"repo":"acme/api","title":"Speed up the map","body":"Edited body."}`)
	if code != http.StatusCreated || !strings.Contains(body, "https://github.com/acme/widgets/issues/99") {
		t.Fatalf("create = %d %s, want 201 with the filed campaign", code, body)
	}
	created, cards := h.snapshot()
	if want := []inbox.CampaignDraft{{Fleet: "work", Repo: "acme/api", Title: "Speed up the map", Body: "Edited body.", Via: "admiralbot"}}; !reflect.DeepEqual(created, want) {
		t.Errorf("creator got %+v, want %+v: the engineer's edits in the draft's fleet", created, want)
	}
	if want := []inbox.CampaignCard{{ID: id, Title: "Speed up the map", URL: "https://github.com/acme/widgets/issues/99", Created: true}}; !reflect.DeepEqual(cards, want) {
		t.Errorf("cards = %+v, want %+v", cards, want)
	}
	if got := idsOf(listed(t, s)); len(got) != 0 {
		t.Errorf("items = %v, want the draft gone once created", got)
	}
	if code, _ := post(t, url+"/api/inbox/_admiral/"+id+"/launch", `{"repo":"acme/api","title":"x","body":"y"}`); code != http.StatusConflict && code != http.StatusNotFound {
		t.Errorf("second create = %d, want 409 or 404", code)
	}
	if created, cards := h.snapshot(); len(created) != 1 || len(cards) != 1 {
		t.Errorf("a second press filed %d and posted %d cards in all, want 1 each", len(created), len(cards))
	}
}

func TestARefusedOrFailedCreateLeavesTheDraftInPlaceAndPostsNoCard(t *testing.T) {
	for _, c := range []struct {
		status int
		msg    string
	}{{http.StatusUnprocessableEntity, "No fleet owns acme/api"}, {http.StatusBadGateway, "Could not create the parent issue"}} {
		s, _, url, mux, h := draftInbox(t)
		h.fail = func() (int, string) { return c.status, c.msg }
		id := draftAsBot(t, mux, draftBody)
		code, body := post(t, url+"/api/inbox/_admiral/"+id+"/launch", draftBody)
		if code != c.status || !strings.Contains(body, c.msg) {
			t.Errorf("create = %d %s, want %d with %q", code, body, c.status, c.msg)
		}
		if got := idsOf(listed(t, s)); !reflect.DeepEqual(got, []string{id}) {
			t.Errorf("status %d: items = %v, want the draft kept", c.status, got)
		}
		if _, cards := h.snapshot(); len(cards) != 0 {
			t.Errorf("status %d: cards = %+v, want none", c.status, cards)
		}
	}
}

func TestDismissingADraftRemovesItPostsTheThrownAwayCardAndFilesNothing(t *testing.T) {
	s, _, url, mux, h := draftInbox(t)
	id := draftAsBot(t, mux, draftBody)

	if code, body := post(t, url+"/api/inbox/_admiral/"+id+"/dismiss", `{}`); code != http.StatusNoContent {
		t.Fatalf("dismiss = %d %s, want 204", code, body)
	}
	created, cards := h.snapshot()
	if len(created) != 0 {
		t.Errorf("creator got %+v; dismissing files nothing", created)
	}
	if want := []inbox.CampaignCard{{ID: id, Title: "Speed up the fleet map"}}; !reflect.DeepEqual(cards, want) {
		t.Errorf("cards = %+v, want %+v (not Created)", cards, want)
	}
	if got := idsOf(listed(t, s)); len(got) != 0 {
		t.Errorf("items = %v, want the draft gone", got)
	}
}
