package chat

import (
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/chatlog"
)

func launchCard() LaunchCard {
	return LaunchCard{ID: "trig-1", Routine: "r1", RoutineName: "Review watch", Tier: "T1", Sender: "alice-the-reviewer",
		Link: "https://github.com/acme/api/pull/7", Text: "Alice asks for a saffron review", MuteBy: []string{"sender"},
		Ship: "calm-otter", Level: "A3", Restricted: "draft_fix"}
}

func TestALaunchCardIsAppendedToTheFleetChatAsALaunchCardWithTheShipAndLevel(t *testing.T) {
	r := newBriefRig(t)
	if err := r.PostLaunchCard("work", launchCard()); err != nil {
		t.Fatalf("PostLaunchCard = %v", err)
	}
	got, err := r.history().Recent("work", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "trig-1" || got[0].Kind != chatlog.KindLaunchCard || !strings.Contains(got[0].Text, "saffron review") {
		t.Fatalf("history = %+v, want the one launch_card with ID trig-1 and the summary as text", got)
	}
	for _, want := range []string{"calm-otter", "A3", "draft_fix", "Review watch", "T1", "alice-the-reviewer", "github.com/acme/api/pull/7", "r1"} {
		if !strings.Contains(string(got[0].Card), want) {
			t.Errorf("card %s lacks %q", got[0].Card, want)
		}
	}
}

func TestPostingALaunchCardTwiceAddsOneEntry(t *testing.T) {
	r := newBriefRig(t)
	for range 2 {
		if err := r.PostLaunchCard("work", launchCard()); err != nil {
			t.Fatalf("PostLaunchCard = %v", err)
		}
	}
	if got, _ := r.history().Recent("work", 50); len(got) != 1 {
		t.Errorf("%d entries after two posts of one ID, want 1", len(got))
	}
}

func TestALaunchCardNeverReachesTheDigestOrTheBrief(t *testing.T) {
	r := newBriefRig(t)
	if _, err := r.history().Append("work", chatlog.Entry{ID: "m1", Kind: chatlog.KindMessage, Role: "user", Text: "where are the lanterns"}); err != nil {
		t.Fatal(err)
	}
	if err := r.PostLaunchCard("work", launchCard()); err != nil {
		t.Fatalf("PostLaunchCard = %v", err)
	}
	r.Tick()
	if got, _ := r.history().Recent("work", 50); len(got) != 2 {
		t.Fatalf("history = %+v, want the message and the card: a card starts no turn", got)
	}
	digest := lineTexts(r.digest(t, "work"))
	brief := r.brief("work")
	if !strings.Contains(brief, "where are the lanterns") {
		t.Fatalf("the brief holds no recent message, so the check below proves nothing:\n%s", brief)
	}
	for _, s := range []string{"saffron", "alice-the-reviewer", "github.com/acme", "calm-otter", "draft_fix"} {
		if strings.Contains(digest, s) {
			t.Errorf("digest %q holds %q, which AdmiralBot would read", digest, s)
		}
		if strings.Contains(brief, s) {
			t.Errorf("brief holds %q, which AdmiralBot would read:\n%s", s, brief)
		}
	}
}
