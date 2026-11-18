package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/chatlog"
)

// shipCardView is a ship card as a client reads it.
type shipCardView struct {
	Ship      string `json:"ship"`
	Title     string `json:"title"`
	Fleet     string `json:"fleet"`
	Skill     string `json:"skill"`
	SkillKind string `json:"skill_kind"`
	Result    string `json:"result"`
	PRs       []struct {
		URL    string `json:"url"`
		Number int    `json:"number"`
	} `json:"prs"`
}

func shipCardsIn(t *testing.T, r *briefRig, fleet string) []shipCardView {
	t.Helper()
	got, err := r.history().Recent(fleet, 500)
	if err != nil {
		t.Fatal(err)
	}
	var out []shipCardView
	for _, e := range got {
		if e.Kind != chatlog.KindShipCard {
			continue
		}
		var v shipCardView
		if err := json.Unmarshal(e.Card, &v); err != nil {
			t.Fatalf("card %s: %v", e.Card, err)
		}
		out = append(out, v)
	}
	return out
}

func sampleShipCard() ShipCard {
	return ShipCard{Ship: "calm-otter", FirstLaunched: "2026-11-17T10:00:00Z", Skill: "tdd", SkillKind: "skill", Result: ShipMoored,
		PRs: []ShipCardPR{{URL: "https://github.com/acme/api/pull/7", Number: 7}, {URL: "https://github.com/acme/api/pull/8", Number: 8}}}
}

func TestAShipCardCarriesWhatTheClientNeedsToRenderAndOpenTheShip(t *testing.T) {
	r := newBriefRig(t)
	if err := r.PostShipCard("work", sampleShipCard()); err != nil {
		t.Fatalf("PostShipCard = %v", err)
	}
	got := shipCardsIn(t, r, "work")
	if len(got) != 1 {
		t.Fatalf("%d ship cards in the work chat, want 1", len(got))
	}
	c := got[0]
	if c.Ship != "calm-otter" || c.Fleet != "work" || c.Skill != "tdd" || c.SkillKind != "skill" || c.Result != "moored" {
		t.Errorf("card = %+v, want ship calm-otter, fleet work, skill tdd, kind skill, result moored", c)
	}
	if len(c.PRs) != 2 || c.PRs[0].URL != "https://github.com/acme/api/pull/7" || c.PRs[0].Number != 7 || c.PRs[1].Number != 8 {
		t.Errorf("prs = %+v, want the two PRs with their URLs and numbers in order", c.PRs)
	}
}

func TestAShipCardPostsInItsOwnFleetAndNoOther(t *testing.T) {
	r := newBriefRig(t)
	if err := r.PostShipCard("work", sampleShipCard()); err != nil {
		t.Fatalf("PostShipCard = %v", err)
	}
	if got := shipCardsIn(t, r, "home"); len(got) != 0 {
		t.Errorf("the home chat holds %+v, want none", got)
	}
}

func TestAShipCardIsPostedOncePerVoyageAndResult(t *testing.T) {
	r := newBriefRig(t)
	post := func(c ShipCard) {
		t.Helper()
		if err := r.PostShipCard("work", c); err != nil {
			t.Fatalf("PostShipCard = %v", err)
		}
	}
	first := sampleShipCard()
	post(first)
	post(first)
	if got := shipCardsIn(t, r, "work"); len(got) != 1 {
		t.Fatalf("%d cards after the same event twice, want 1", len(got))
	}
	next := first
	next.Result = ShipSailedOff
	post(next)
	reused := first
	reused.FirstLaunched = "2026-11-18T09:00:00Z"
	post(reused)
	if got := shipCardsIn(t, r, "work"); len(got) != 3 {
		t.Errorf("%d cards, want 3: a new result and a reused name with a new FirstLaunched each post their own", len(got))
	}
}

func TestAShipCardAcceptsALostResult(t *testing.T) {
	r := newBriefRig(t)
	c := sampleShipCard()
	c.Result = ShipLost
	if err := r.PostShipCard("work", c); err != nil {
		t.Fatalf("PostShipCard = %v", err)
	}
	if got := shipCardsIn(t, r, "work"); len(got) != 1 || got[0].Result != "lost" {
		t.Errorf("cards = %+v, want one with result lost", got)
	}
}

func TestAShipCardStartsNoTurnAndNeverReachesTheDigestOrTheBrief(t *testing.T) {
	r := newBriefRig(t)
	if _, err := r.history().Append("work", chatlog.Entry{ID: "m1", Kind: chatlog.KindMessage, Role: "user", Text: "where are the lanterns"}); err != nil {
		t.Fatal(err)
	}
	if err := r.PostShipCard("work", sampleShipCard()); err != nil {
		t.Fatalf("PostShipCard = %v", err)
	}
	r.Tick()
	digest := lineTexts(r.digest(t, "work"))
	brief := r.brief("work")
	if !strings.Contains(brief, "where are the lanterns") {
		t.Fatalf("the brief holds no recent message, so the check below proves nothing:\n%s", brief)
	}
	for _, s := range []string{"calm-otter", "pull/7"} {
		if strings.Contains(digest, s) {
			t.Errorf("digest %q holds %q, which AdmiralBot would read", digest, s)
		}
		if strings.Contains(brief, s) {
			t.Errorf("brief holds %q, which AdmiralBot would read:\n%s", s, brief)
		}
	}
}

// rawShipCard is the payload of the one ship card in the fleet's chat, as keys.
func rawShipCard(t *testing.T, r *briefRig, fleet string) map[string]any {
	t.Helper()
	got, err := r.history().Recent(fleet, 500)
	if err != nil || len(got) != 1 {
		t.Fatalf("chat = %v, %v, want one entry", got, err)
	}
	var m map[string]any
	if err := json.Unmarshal(got[0].Card, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAShipCardCarriesTheShipsTitleBesideItsName(t *testing.T) {
	r := newBriefRig(t)
	c := sampleShipCard()
	c.Name, c.Title = "Calm Otter", "Fix the login redirect"
	if err := r.PostShipCard("work", c); err != nil {
		t.Fatal(err)
	}
	if m := rawShipCard(t, r, "work"); m["name"] != "Calm Otter" || m["title"] != "Fix the login redirect" {
		t.Errorf("card = %v, want name Calm Otter and title Fix the login redirect", m)
	}
}

func TestAShipCardOfAShipWithNoTitleHasNoTitleKey(t *testing.T) {
	r := newBriefRig(t)
	c := sampleShipCard()
	c.Name = "Calm Otter"
	if err := r.PostShipCard("work", c); err != nil {
		t.Fatal(err)
	}
	if m := rawShipCard(t, r, "work"); m["name"] != "Calm Otter" {
		t.Fatalf("card = %v, want name Calm Otter", m)
	} else if _, has := m["title"]; has {
		t.Errorf("card = %v, want no title key", m)
	}
}
