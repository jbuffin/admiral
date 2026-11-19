package ships

import (
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

// cardRig is a draft rig whose follow reads campaign c-7, titled by Admiral, from GitHub; saltpaw was launched from the chat when fromChat.
func newCardRig(t *testing.T, fromChat bool) (*draftRig, *cardCatcher) {
	t.Helper()
	r := newDraftRig(t)
	cc := &cardCatcher{}
	r.s.PostFollowingCard = cc.post
	r.s.FollowedCampaign = func(fleet, ref string) (string, string, bool) { return "c-7", mapCampaignTitle, true }
	if fromChat {
		o := r.options(t, "saltpaw")
		r.s.markDraftingFromChat("saltpaw", firstLaunched(o), o["fleet"], o["harness"])
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
	if want := (FollowingCard{Ship: "saltpaw", CampaignID: "c-7", Title: mapCampaignTitle}); cards[0] != want {
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

func TestADraftingShipNotLaunchedFromTheChatFollowsItsRefAndPostsNoCard(t *testing.T) {
	r, cc := newCardRig(t, false)
	writeRef(t, r.refOf("saltpaw"), campaignIssue+"\n")
	r.endTurn(t, "saltpaw")
	if got := r.followed(); len(got) != 1 {
		t.Fatalf("follow calls = %v, want the ref followed as before", got)
	}
	if _, cards := cc.got(); len(cards) != 0 {
		t.Errorf("cards = %+v, want none for a ship launched from the Add campaign modal", cards)
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
