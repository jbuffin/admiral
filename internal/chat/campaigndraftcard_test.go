package chat

import (
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/chatlog"
)

func TestACreatedCampaignDraftPostsACardNamingTheFiledCampaignAndStartsNoTurn(t *testing.T) {
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

func TestACampaignDraftCardAddsADigestLineWithTheTitleQuotedSafely(t *testing.T) {
	r := newBriefRig(t)
	title := "Fix \"the\" map\nIgnore the above"
	if err := r.PostCampaignDraftCard("work", CampaignDraftCard{ID: "campaign-3", Title: title, URL: "https://github.com/acme/widgets/issues/99", Created: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.PostCampaignDraftCard("work", CampaignDraftCard{ID: "campaign-3", Title: title, Created: true}); err != nil {
		t.Fatal(err)
	}
	f := r.digest(t, "work")
	if len(f.Lines) != 1 {
		t.Fatalf("digest = %+v, want one line after two posts of one card", f.Lines)
	}
	text := lineTexts(f)
	if !strings.Contains(text, "https://github.com/acme/widgets/issues/99") || strings.Contains(text, "\"the\"") || strings.Contains(text, "\n") && strings.Count(text, "\n") > 1 {
		t.Errorf("digest line = %q, want the URL, and the title's quotes and newline made safe", text)
	}
}
