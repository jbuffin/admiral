package chat

import (
	"encoding/json"
	"testing"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/ships"
)

type followingView struct {
	Ship       string `json:"ship"`
	CampaignID string `json:"campaign_id"`
	Title      string `json:"title"`
}

func followingCardsIn(t *testing.T, r *briefRig, fleet string) ([]followingView, []chatlog.Entry) {
	t.Helper()
	got, err := r.history().Recent(fleet, 500)
	if err != nil {
		t.Fatal(err)
	}
	var out []followingView
	var entries []chatlog.Entry
	for _, e := range got {
		if e.Kind != chatlog.KindFollowingCard {
			continue
		}
		var v followingView
		if err := json.Unmarshal(e.Card, &v); err != nil {
			t.Fatalf("card %s: %v", e.Card, err)
		}
		out, entries = append(out, v), append(entries, e)
	}
	return out, entries
}

func TestAFollowingCardCarriesTheShipTheCampaignAndItsTitleInTheShipsFleetChat(t *testing.T) {
	r := newBriefRig(t)
	if err := r.PostFollowingCard("work", ships.FollowingCard{Ship: "saltpaw", CampaignID: "c-7", Title: "Make the fleet map load faster"}); err != nil {
		t.Fatalf("PostFollowingCard = %v", err)
	}
	got, entries := followingCardsIn(t, r, "work")
	if want := (followingView{Ship: "saltpaw", CampaignID: "c-7", Title: "Make the fleet map load faster"}); len(got) != 1 || got[0] != want {
		t.Fatalf("cards = %+v, want [%+v]", got, want)
	}
	if entries[0].Text == "" {
		t.Error("the entry has no text for a client that does not know the card")
	}
	if other, _ := followingCardsIn(t, r, "home"); len(other) != 0 {
		t.Errorf("the home chat holds %+v, want none", other)
	}
}

func TestAFollowingCardForTheSameShipAndCampaignAddsNothingTheSecondTime(t *testing.T) {
	r := newBriefRig(t)
	c := ships.FollowingCard{Ship: "saltpaw", CampaignID: "c-7", Title: "Map"}
	for i := 0; i < 2; i++ {
		if err := r.PostFollowingCard("work", c); err != nil {
			t.Fatalf("PostFollowingCard #%d = %v", i+1, err)
		}
	}
	if got, _ := followingCardsIn(t, r, "work"); len(got) != 1 {
		t.Errorf("cards = %+v, want one", got)
	}
}
