package chat

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
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/ships"
)

// quietRig is a Service with fleets home and work that never wires the Inbox or a digest ack, so it compiles
// whether or not those fields exist.
type quietRig struct {
	*Service
	home string
	mux  *http.ServeMux

	mu    sync.Mutex
	ships []ships.Ship
	items []inbox.Item
}

func newQuietRig(t *testing.T) *quietRig {
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
	r := &quietRig{home: home, mux: http.NewServeMux()}
	r.Service = &Service{
		Home: home, Config: func() *config.Config { return cfg },
		Ships:     func() []ships.Ship { r.mu.Lock(); defer r.mu.Unlock(); return append([]ships.Ship(nil), r.ships...) },
		Campaigns: func() []follow.Followed { return nil },
	}
	// The Inbox feed is set by name only while the field exists, so the test stays valid after it goes.
	if f := reflect.ValueOf(r.Service).Elem().FieldByName("Inbox"); f.IsValid() {
		f.Set(reflect.ValueOf(func() []inbox.Item {
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]inbox.Item(nil), r.items...)
		}))
	}
	r.Routes(r.mux)
	return r
}

func (r *quietRig) set(f func()) { r.mu.Lock(); defer r.mu.Unlock(); f() }

// writesNoDigest fails when chat/<fleet>/digest.json exists for any fleet.
func (r *quietRig) writesNoDigest(t *testing.T, what string) {
	t.Helper()
	for _, fleet := range []string{"home", "work"} {
		if b, err := os.ReadFile(filepath.Join(r.home, "chat", fleet, "digest.json")); err == nil {
			t.Errorf("%s wrote chat/%s/digest.json = %s, want no digest file", what, fleet, b)
		}
	}
}

func TestACommitmentCardAddsItsEntryAndWritesNoDigestFile(t *testing.T) {
	r := newQuietRig(t)
	for range 2 {
		if err := r.PostCard("work", commitmentCard()); err != nil {
			t.Fatalf("PostCard = %v", err)
		}
	}
	got, _ := r.history().Recent("work", 50)
	if len(got) != 1 || got[0].Kind != chatlog.KindCommitment {
		t.Fatalf("history = %+v, want the one commitment card", got)
	}
	r.writesNoDigest(t, "a commitment card")
}

func TestACampaignDraftCardAddsItsEntryAndWritesNoDigestFile(t *testing.T) {
	r := newQuietRig(t)
	card := CampaignDraftCard{ID: "campaign-3", Title: "Speed up the fleet map", URL: "https://github.com/acme/widgets/issues/99", Created: true}
	for range 2 {
		if err := r.PostCampaignDraftCard("work", card); err != nil {
			t.Fatalf("PostCampaignDraftCard = %v", err)
		}
	}
	got, _ := r.history().Recent("work", 50)
	if len(got) != 1 || got[0].Kind != chatlog.KindCampaignDraftCard {
		t.Fatalf("history = %+v, want the one campaign_draft_card", got)
	}
	r.writesNoDigest(t, "a campaign draft card")
}

func TestChatTicksOverShipChangesInboxItemsAndAnsweredQuestionsWriteNoDigestFile(t *testing.T) {
	r := newQuietRig(t)
	r.set(func() {
		r.ships = []ships.Ship{{Session: "s1", Name: "bold-heron", Fleet: "home", Status: ships.StatusUnderway}}
		r.items = []inbox.Item{{ID: "old", Kind: "question", Session: "s0", Fleet: "home"}}
	})
	req := httptest.NewRequest("POST", "/api/chat/questions", strings.NewReader(`{"title":"Which branch?","questions":[{"id":"q1","text":"Which branch?","multi":false,"options":[{"label":"main"},{"label":"dev"}]}]}`)).WithContext(actor.WithFleet(context.Background(), "home"))
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST question = %d %s, want 201", rec.Code, rec.Body)
	}
	var q struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	r.Tick() // the baseline

	r.set(func() {
		r.ships = []ships.Ship{{Session: "s1", Name: "bold-heron", Fleet: "home", Status: ships.StatusMoored}}
		r.items = append(r.items, inbox.Item{ID: "new", Kind: "question", Session: "s1", Name: "bold-heron", Fleet: "home"})
	})
	r.Tick()
	path := filepath.Join(r.home, "chat", "home", "questions", inbox.AnswerName(q.ID))
	if err := os.WriteFile(path, []byte(`{"answered":"2026-11-12T10:00:00Z","answers":{"q1":{"selected":["main"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The question cache refreshes on its own clock; the answered question leaves the brief once it has.
	for end := time.Now().Add(5 * time.Second); strings.Contains(r.brief("home"), "Which branch?"); time.Sleep(50 * time.Millisecond) {
		r.Tick()
		if time.Now().After(end) {
			t.Fatal("within 5s the answered question never left the brief, so the ticks did not see the answer")
		}
	}
	r.Tick()
	r.writesNoDigest(t, "ticks over a ship change, an Inbox item and an answered question")
}

func (r *quietRig) brief(fleet string) string {
	b, _ := os.ReadFile(filepath.Join(r.home, "chat", fleet, "brief.md"))
	return string(b)
}
