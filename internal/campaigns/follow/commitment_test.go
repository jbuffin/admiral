package follow

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type commitmentView struct {
	ID              string `json:"id"`
	TS              string `json:"ts"`
	Launched        int    `json:"launched"`
	WaitingChildren int    `json:"waiting_children"`
}

func (r *rig) rawGet(id string) map[string]json.RawMessage {
	r.t.Helper()
	code, body := r.do("GET", "/api/campaigns/"+id, nil)
	var m map[string]json.RawMessage
	if code != http.StatusOK || json.Unmarshal(body, &m) != nil {
		r.t.Fatalf("GET /api/campaigns/%s = %d %s", id, code, body)
	}
	return m
}

func TestTheCampaignCarriesItsOpenWorkCommitment(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	ts := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	var asked [][2]string
	r.svc.Commitment = func(fleet, id string) (CampaignCommitment, bool) {
		asked = append(asked, [2]string{fleet, id})
		return CampaignCommitment{ID: "cm-1", TS: ts, Launched: 2, WaitingChildren: 1}, true
	}
	raw, ok := r.rawGet(c.ID)["commitment"]
	if !ok {
		t.Fatal("GET campaign has no commitment key; want the open work commitment")
	}
	var got commitmentView
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "cm-1" || got.TS != "2026-01-01T11:00:00Z" || got.Launched != 2 || got.WaitingChildren != 1 {
		t.Errorf("commitment = %+v, want cm-1 at 2026-01-01T11:00:00Z with 2 launched and 1 waiting", got)
	}
	for _, a := range asked {
		if a[0] != "work" || a[1] != c.ID {
			t.Errorf("the ledger was asked for %v; want only the campaign's own fleet work and id %s", a, c.ID)
		}
	}
}

func TestTheCampaignHasNoCommitmentKeyWhenOnlyFollowed(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	if _, ok := r.rawGet(c.ID)["commitment"]; ok {
		t.Error("GET campaign carries a commitment with no hook set")
	}
	r.svc.Commitment = func(fleet, id string) (CampaignCommitment, bool) { return CampaignCommitment{}, false }
	if _, ok := r.rawGet(c.ID)["commitment"]; ok {
		t.Error("GET campaign carries a commitment when the ledger holds none")
	}
}

func TestEveryCampaignResponseCarriesTheCommitment(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.svc.Commitment = func(fleet, id string) (CampaignCommitment, bool) {
		return CampaignCommitment{ID: "cm-1", TS: r.clock.Now()}, true
	}
	code, body := r.do("GET", "/api/campaigns", nil)
	var list struct {
		Campaigns []map[string]json.RawMessage `json:"campaigns"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &list) != nil || len(list.Campaigns) != 1 {
		t.Fatalf("GET /api/campaigns = %d %s", code, body)
	}
	if _, ok := list.Campaigns[0]["commitment"]; !ok {
		t.Error("the list entry has no commitment key")
	}
	code, body = r.do("POST", "/api/campaigns/"+c.ID+"/unfollow", nil)
	var m map[string]json.RawMessage
	if code != http.StatusOK || json.Unmarshal(body, &m) != nil {
		t.Fatalf("unfollow = %d %s", code, body)
	}
	if _, ok := m["commitment"]; !ok {
		t.Error("the unfollow response has no commitment key")
	}
}
