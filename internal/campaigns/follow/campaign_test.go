package follow

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
)

func closedTree() iss {
	return iss{num: 20, closed: true, labels: []string{"wayfinder:map"}, kids: []iss{{num: 21, closed: true}, {num: 22, closed: true}}}
}

func TestFollowFollowsAReferenceOnceAndReturnsTheSameCampaignTheSecondTime(t *testing.T) {
	r := newRig(t, fixtureTree())
	first, created, lerr := r.svc.Follow(context.Background(), "acme/widgets#20")
	if lerr != nil || !created || first.ID == "" || first.URL != rootURL || first.Fleet != "work" {
		t.Fatalf("Follow = %+v, created=%v, %v; want a new campaign on fleet work at %s", first, created, lerr, rootURL)
	}
	again, created, lerr := r.svc.Follow(context.Background(), rootURL)
	if lerr != nil || created || again.ID != first.ID {
		t.Errorf("second Follow = %+v, created=%v, %v; want the same campaign, not created", again, created, lerr)
	}
}

func TestFollowRefusesWhatTheRouteRefuses(t *testing.T) {
	r := newRig(t, fixtureTree())
	if _, _, lerr := r.svc.Follow(context.Background(), "not a reference"); lerr == nil || lerr.Status != http.StatusBadRequest {
		t.Errorf("Follow of a bad reference = %v; want a 400", lerr)
	}
	r.svc.Route = func(string, string) string { return "" }
	if _, _, lerr := r.svc.Follow(context.Background(), "acme/elsewhere#1"); lerr == nil || lerr.Status != http.StatusUnprocessableEntity {
		t.Errorf("Follow of a repo no fleet owns = %v; want a 422", lerr)
	}
}

func TestSnapshotIsTheLatestTreeBuiltWithEachChildsLaunchOrder(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.do("PUT", "/api/campaigns/"+c.ID+"/work-repo", map[string]string{"repo": "https://github.com/acme/work"})
	got := r.svc.Snapshot(c.ID)
	if !got.Found || !got.Followed || got.ID != c.ID || got.Campaign.Root == nil {
		t.Fatalf("Snapshot = found %v followed %v id %q root %v; want a followed campaign with its tree", got.Found, got.Followed, got.ID, got.Campaign.Root)
	}
	ready := map[string]bool{}
	for _, n := range got.Campaign.Ready {
		ready[n.URL] = true
	}
	if !ready[issURL(21)] || ready[issURL(22)] || ready[issURL(23)] {
		t.Errorf("ready = %v; want 21 only of 21, 22 and 23", ready)
	}
	if o := got.Orders[issURL(21)]; o.Text != issURL(21) || o.CloneRepo != "github.com/acme/work" {
		t.Errorf("order of 21 = %+v; want its URL as text and the work repo", o)
	}
	if o := got.Orders["https://github.com/acme/other/issues/28"]; o.Text != "Issue 28 https://github.com/acme/other/issues/28" || o.CloneRepo != "github.com/acme/other" {
		t.Errorf("order of the cross-repo child 28 = %+v; want its own repo, not the work repo", o)
	}
}

func TestSnapshotOfAnUnknownCampaignIsNotFoundAndOfAnUnfollowedOneIsNotFollowed(t *testing.T) {
	r := newRig(t, fixtureTree())
	if got := r.svc.Snapshot("nope"); got.Found {
		t.Errorf("Snapshot of an unknown id = %+v; want Found false", got)
	}
	c := r.follow(rootURL)
	if code, b := r.do("POST", "/api/campaigns/"+c.ID+"/unfollow", nil); code != http.StatusOK {
		t.Fatalf("unfollow = %d %s", code, b)
	}
	if got := r.svc.Snapshot(c.ID); !got.Found || got.Followed || got.Campaign.Root == nil {
		t.Errorf("Snapshot after unfollow = found %v followed %v root %v; want found, not followed, tree kept", got.Found, got.Followed, got.Campaign.Root)
	}
}

func TestSnapshotKeepsTheLastTreeWhenARereadFails(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	if err := writeFail(r.gh, "acme_widgets_20", "boom"); err != nil {
		t.Fatal(err)
	}
	_ = r.svc.read(context.Background(), c.ID)
	if got := r.svc.Snapshot(c.ID); !got.Followed || got.Campaign.Root == nil || len(got.Campaign.Ready) == 0 {
		t.Errorf("Snapshot after a failed read = followed %v root %v ready %d; want the last good tree", got.Followed, got.Campaign.Root, len(got.Campaign.Ready))
	}
}

func TestSnapshotOfACampaignWhoseTreeClosedIsNotFollowedAndShowsItAllClosed(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.gh.set(closedTree())
	if err := r.svc.read(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	got := r.svc.Snapshot(c.ID)
	if !got.Found || got.Followed || got.Campaign.Root == nil || !got.Campaign.Root.Closed {
		t.Errorf("Snapshot = found %v followed %v; want an ended follow with the closed tree", got.Found, got.Followed)
	}
}

func TestOnReadRunsAfterAReadStoresATreeAfterItEndsTheCampaignAndAfterAnUnfollow(t *testing.T) {
	r := newRig(t, fixtureTree())
	var n atomic.Int32
	r.svc.OnRead = func() { n.Add(1) }
	c := r.follow(rootURL)
	base := n.Load()
	if err := r.svc.read(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if n.Load() <= base {
		t.Error("OnRead did not run after a read stored a tree")
	}
	base = n.Load()
	r.gh.set(closedTree())
	if err := r.svc.read(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if n.Load() <= base {
		t.Error("OnRead did not run after a read ended the campaign")
	}
	c2 := newRig(t, fixtureTree())
	c2.svc.OnRead = func() { n.Add(1) }
	f := c2.follow(rootURL)
	base = n.Load()
	c2.do("POST", "/api/campaigns/"+f.ID+"/unfollow", nil)
	if n.Load() <= base {
		t.Error("OnRead did not run after an unfollow")
	}
}
