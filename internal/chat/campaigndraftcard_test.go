package chat

import (
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/chatlog"
)

func TestACreatedCampaignDraftPostsACardNamingTheFiledCampaignAndStartsNoTurn(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	if _, err := r.history().Append("work", chatlog.Entry{ID: "m1", Kind: chatlog.KindMessage, Role: "user", Text: "draft a campaign for the map"}); err != nil {
		t.Fatal(err)
	}
	card := CampaignDraftCard{ID: "campaign-1", Title: "Speed up the fleet map", URL: "https://github.com/acme/widgets/issues/99", Created: true}
	if err := r.PostCampaignDraftCard("work", card); err != nil {
		t.Fatalf("PostCampaignDraftCard = %v", err)
	}
	r.Tick()
	got, err := r.history().Recent("work", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != "campaign-1" || got[1].Kind != chatlog.KindCampaignDraftCard {
		t.Fatalf("history = %+v, want the message and one campaign_draft_card with ID campaign-1 and no AdmiralBot turn after it", got)
	}
	for _, want := range []string{"Speed up the fleet map", "https://github.com/acme/widgets/issues/99"} {
		if !strings.Contains(got[1].Text, want) {
			t.Errorf("card text = %q, want it to hold %q", got[1].Text, want)
		}
	}
}

func TestADismissedCampaignDraftPostsACardSayingItWasThrownAwayAndTwiceAddsOne(t *testing.T) {
	t.Parallel()
	r := newBriefRig(t)
	for range 2 {
		if err := r.PostCampaignDraftCard("work", CampaignDraftCard{ID: "campaign-2", Title: "Speed up the fleet map"}); err != nil {
			t.Fatalf("PostCampaignDraftCard = %v", err)
		}
	}
	got, _ := r.history().Recent("work", 50)
	if len(got) != 1 || got[0].Kind != chatlog.KindCampaignDraftCard {
		t.Fatalf("history = %+v, want one campaign_draft_card after two posts of one ID", got)
	}
	if !strings.Contains(strings.ToLower(got[0].Text), "thrown away") || !strings.Contains(got[0].Text, "Speed up the fleet map") {
		t.Errorf("card text = %q, want it to say the draft was thrown away and name it", got[0].Text)
	}
}
