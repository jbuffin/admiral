package follow

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
)

func TestAnActorsCampaignListHoldsOnlyItsFleetsCampaigns(t *testing.T) {
	r := newRig(t, fixtureTree())
	for fleet, url := range map[string]string{"work": rootURL, "home": issURL(21)} {
		if _, _, err := r.svc.Store.Follow(Followed{URL: url, Fleet: fleet, FollowedAt: time.Unix(1000, 0).UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		fleet string
		want  int
	}{{"work", 1}, {"home", 1}, {"", 2}} {
		req := httptest.NewRequest("GET", "/api/campaigns", nil)
		if c.fleet != "" {
			req = req.WithContext(actor.WithFleet(context.Background(), c.fleet))
		}
		rec := httptest.NewRecorder()
		r.mux.ServeHTTP(rec, req)
		var out struct{ Campaigns []campaignJSON }
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("fleet %q: %v: %s", c.fleet, err, rec.Body)
		}
		if len(out.Campaigns) != c.want {
			t.Errorf("fleet %q lists %d campaigns, want %d", c.fleet, len(out.Campaigns), c.want)
		}
		for _, g := range out.Campaigns {
			if c.fleet != "" && g.Fleet != c.fleet {
				t.Errorf("fleet %q lists a campaign of fleet %q", c.fleet, g.Fleet)
			}
		}
	}
}
