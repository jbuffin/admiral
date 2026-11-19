package follow

import "testing"

// FollowDrafted is the func main hands the ships service: it follows the campaign a drafting ship filed, in the ship's own fleet only.

func TestFollowDraftedFollowsAFreshCampaignInTheShipsFleet(t *testing.T) {
	r := newRig(t, fixtureTree())
	if err := r.svc.FollowDrafted("work", rootURL); err != nil {
		t.Fatalf("FollowDrafted = %v, want nil", err)
	}
	if have, ok := r.svc.Store.Lookup(rootURL); !ok || have.Ended() || have.Fleet != "work" {
		t.Errorf("campaign after FollowDrafted = %+v, %v; want followed in work", have, ok)
	}
}

func TestFollowDraftedRefusesACampaignOfAnotherFleetAndFollowsNothing(t *testing.T) {
	r := newRig(t, fixtureTree())
	err := r.svc.FollowDrafted("home", rootURL)
	if err == nil || err.Error() != "That campaign belongs to fleet work, not home" {
		t.Fatalf("FollowDrafted = %v, want the fleet refusal", err)
	}
	if _, ok := r.svc.Store.Lookup(rootURL); ok {
		t.Error("the campaign was followed despite the refusal")
	}
}

func TestFollowDraftedRefusesACampaignNoFleetOwns(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.svc.Route = func(string, string) string { return "" }
	err := r.svc.FollowDrafted("work", rootURL)
	if err == nil || err.Error() != "No fleet owns acme/widgets, so Admiral can't read it" {
		t.Fatalf("FollowDrafted = %v, want the no-fleet refusal", err)
	}
	if _, ok := r.svc.Store.Lookup(rootURL); ok {
		t.Error("the campaign was followed despite the refusal")
	}
}

func TestFollowDraftedFollowsAnUnfollowedCampaignAgain(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.unfollow(c.ID)
	if err := r.svc.FollowDrafted("work", rootURL); err != nil {
		t.Fatalf("FollowDrafted = %v, want nil", err)
	}
	if got := r.get(c.ID); got.Ended || got.EndReason != "" {
		t.Errorf("campaign after FollowDrafted = %+v; want followed again, not ended", got)
	}
}
