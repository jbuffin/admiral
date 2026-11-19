package ships

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const mapCampaignTitle = "Make the fleet map load faster"

type cardCatcher struct {
	mu     sync.Mutex
	fleets []string
	cards  []FollowingCard
}

func (c *cardCatcher) post(fleet string, card FollowingCard) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fleets, c.cards = append(c.fleets, fleet), append(c.cards, card)
	return nil
}

func (c *cardCatcher) got() ([]string, []FollowingCard) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.fleets...), append([]FollowingCard(nil), c.cards...)
}

// newCardRig is a draft rig whose follow reads campaign c-7, titled by Admiral, from GitHub; saltpaw was launched from the chat when fromChat.
func newCardRig(t *testing.T, fromChat bool) (*draftRig, *cardCatcher) {
	t.Helper()
	r := newDraftRig(t)
	cc := &cardCatcher{}
	r.s.PostFollowingCard = cc.post
	r.id, r.title = "c-7", mapCampaignTitle
	if fromChat {
		o := r.options(t, "saltpaw")
		r.s.markDrafting("saltpaw", firstLaunched(o), o["fleet"], o["harness"], true)
	}
	return r, cc
}

func TestAChatLaunchedDraftingShipsFollowedRefPostsOneFollowingCardNamingTheCampaignAdmiralRead(t *testing.T) {
	r, cc := newCardRig(t, true)
	// What the ship wrote is only a URL; the title is Admiral's own read of the issue.
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")
	fleets, cards := cc.got()
	if len(cards) != 1 || fleets[0] != "work" {
		t.Fatalf("cards = %+v in fleets %v, want one in the ship's fleet work", cards, fleets)
	}
	first := firstLaunched(r.options(t, "saltpaw"))
	if want := (FollowingCard{Ship: "saltpaw", First: first, CampaignID: "c-7", Title: mapCampaignTitle}); first == "" || cards[0] != want {
		t.Errorf("card = %+v, want %+v", cards[0], want)
	}
}

func TestAFollowingCardPostsOncePerShipNotForALaterRefTheFollowIgnores(t *testing.T) {
	r, cc := newCardRig(t, true)
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")
	writeRef(t, r.refOf("saltpaw"), otherCampaign+"\n")
	r.endTurn(t, "saltpaw")
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")
	if _, cards := cc.got(); len(cards) != 1 {
		t.Errorf("cards = %+v, want the one for the first ref", cards)
	}
}

func TestAChatLaunchedDraftingShipThatFilesAtItsEndPostsTheFollowingCard(t *testing.T) {
	r, cc := newCardRig(t, true)
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.stopShip(t, "saltpaw")
	r.s.settleWG.Wait()
	if _, cards := cc.got(); len(cards) != 1 || cards[0].CampaignID != "c-7" {
		t.Errorf("cards = %+v, want one for c-7 from the end-of-ship read", cards)
	}
}

func TestOnlyADraftingShipAdmiralBotLaunchesFromTheChatPostsAFollowingCard(t *testing.T) {
	r, cc := newCardRig(t, false)
	skill := filepath.Join(r.checkout, ".claude", "skills", "wayfinder", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: wayfinder\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const draft = `,"drafting":true,"skill":{"kind":"skill","name":"wayfinder"}`
	fromBot := r.launchBody(t, true, draft)
	fromEngineer := r.launchBody(t, false, draft)
	for _, session := range []string{fromBot, fromEngineer} {
		writeRef(t, r.refOf(session), campaignIssue+"\n")
		r.endTurn(t, session)
	}
	if got := r.followed(); len(got) != 2 {
		t.Fatalf("follow calls = %v, want both ships' refs followed", got)
	}
	if _, cards := cc.got(); len(cards) != 1 || cards[0].Ship != fromBot {
		t.Errorf("cards = %+v, want one, for the ship AdmiralBot launched (%s) and none for the engineer's (%s)", cards, fromBot, fromEngineer)
	}
}

func TestAChatLaunchedDraftingShipWhoseRefTheFollowRefusesPostsNoCard(t *testing.T) {
	r, cc := newCardRig(t, true)
	r.err = errString("That campaign belongs to fleet home, not work")
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")
	if _, cards := cc.got(); len(cards) != 0 {
		t.Errorf("cards = %+v, want none: nothing was followed", cards)
	}
}
