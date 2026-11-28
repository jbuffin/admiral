package follow

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/github"
)

// followAgain is the engineer's POST /api/campaigns for a reference; it returns the status and the campaign.
func (r *rig) followAgain(ref string) (int, campaignJSON, string) {
	r.t.Helper()
	code, body := r.do("POST", "/api/campaigns", map[string]string{"reference": ref})
	var c campaignJSON
	_ = json.Unmarshal(body, &c)
	if len(body) > 160 {
		body = body[:160]
	}
	return code, c, string(body)
}

func (r *rig) unfollow(id string) {
	r.t.Helper()
	if code, body := r.do("POST", "/api/campaigns/"+id+"/unfollow", nil); code != http.StatusOK {
		r.t.Fatalf("unfollow = %d %s", code, body)
	}
}

func TestFollowingAnUnfollowedCampaignAgainClearsItsEndAndResumesRereads(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.unfollow(c.ID)

	reads := r.gh.rootReads()
	code, again, body := r.followAgain(rootURL)
	if code != http.StatusOK || again.ID != c.ID || again.Ended || again.EndReason != "" || again.EndedAt != "" {
		t.Fatalf("follow again = %d %s; want 200, campaign %s, not ended", code, body, c.ID)
	}
	if r.gh.rootReads() <= reads {
		t.Error("follow again read no tree; want the tree read before the end is cleared")
	}
	if got := r.get(c.ID); got.Ended || got.EndReason != "" || got.Root == nil {
		t.Errorf("campaign after follow again = %+v; want followed with its tree", got)
	}
	_, active := r.do("GET", "/api/campaigns?ended=false", nil)
	_, ended := r.do("GET", "/api/campaigns?ended=true", nil)
	if !strings.Contains(string(active), c.ID) || strings.Contains(string(ended), c.ID) {
		t.Errorf("ended=false lists %s, ended=true lists %s; want the campaign only under active", active, ended)
	}

	r.clock.Advance(10 * time.Minute)
	reads = r.gh.rootReads()
	r.svc.Tick()
	eventually(t, "a followed-again campaign was reread", func() bool { return r.gh.rootReads() > reads })
}

func TestFollowingAnUnfollowedCampaignAgainEndsItAsEndedWhenItsTreeClosedMeanwhile(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.unfollow(c.ID)
	r.gh.set(closedTree())

	code, again, body := r.followAgain(rootURL)
	if code != http.StatusOK || again.ID != c.ID || !again.Ended || again.EndReason != "ended" {
		t.Errorf("follow again of a closed tree = %d %s; want 200, ended with reason ended", code, body)
	}
}

func TestFollowingACampaignWhoseTreeClosedLeavesItEnded(t *testing.T) {
	r := newRig(t, closedTree())
	c := r.follow(rootURL)
	if !c.Ended || c.EndReason != "ended" {
		t.Fatalf("followed closed tree = %+v; want ended with reason ended", c)
	}
	reads := r.gh.rootReads()
	code, again, body := r.followAgain(rootURL)
	if code != http.StatusOK || again.ID != c.ID || !again.Ended || again.EndReason != "ended" {
		t.Errorf("follow again of a tree-closed campaign = %d %s; want 200, still ended with reason ended", code, body)
	}
	if got := r.get(c.ID); !got.Ended || got.EndReason != "ended" {
		t.Errorf("campaign = %+v; want still ended", got)
	}
	r.clock.Advance(10 * time.Minute)
	r.svc.Tick()
	r.holds("a tree-closed campaign was reread", func() bool { return r.gh.rootReads() > reads })
}

func TestTheCampaignViewCarriesWhyItEnded(t *testing.T) {
	r := newRig(t, fixtureTree())
	open := r.follow(rootURL)
	if open.EndReason != "" {
		t.Errorf("a followed campaign's end_reason = %q; want empty", open.EndReason)
	}
	_, raw := r.do("GET", "/api/campaigns/"+open.ID, nil)
	if strings.Contains(string(raw), `"end_reason":"`) {
		t.Errorf("a followed campaign carries an end reason: %s", raw)
	}

	r.unfollow(open.ID)
	if got := r.get(open.ID); got.EndReason != "unfollowed" {
		t.Errorf("GET one: end_reason = %q; want unfollowed", got.EndReason)
	}
	r.gh.set(iss{num: 30, closed: true, kids: []iss{{num: 31, closed: true}}})
	shut := r.follow("acme/widgets#30")
	if shut.EndReason != "ended" {
		t.Errorf("follow of a closed tree: end_reason = %q; want ended", shut.EndReason)
	}

	_, body := r.do("GET", "/api/campaigns", nil)
	var list struct {
		Campaigns []campaignJSON `json:"campaigns"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range list.Campaigns {
		got[c.ID] = c.EndReason
	}
	if got[open.ID] != "unfollowed" || got[shut.ID] != "ended" {
		t.Errorf("GET list end reasons = %v; want %s unfollowed and %s ended", got, open.ID, shut.ID)
	}
}

func TestALegacyEndedRecordWithOpenTreeReadsAsUnfollowedAndCanBeFollowedAgain(t *testing.T) {
	r := newRig(t, fixtureTree())
	now := r.clock.Now()
	legacy, _, err := r.svc.Store.Follow(Followed{URL: rootURL, Fleet: "work", FollowedAt: now, EndedAt: now,
		Tree: &github.TreeNode{Number: 20, Children: []*github.TreeNode{{Number: 21}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.get(legacy.ID); !got.Ended || got.EndReason != "unfollowed" {
		t.Fatalf("legacy record = %+v; want ended with reason unfollowed", got)
	}
	code, again, body := r.followAgain(rootURL)
	if code != http.StatusOK || again.ID != legacy.ID || again.Ended {
		t.Errorf("follow again of a legacy record = %d %s; want 200, followed", code, body)
	}
}

func TestFollowingAgainWhenTheTreeReadFailsAnswers422AndStaysEnded(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.unfollow(c.ID)
	r.gh.failFile("acme_widgets_20", "gh: HTTP 502 bad gateway")

	code, _, body := r.followAgain(rootURL)
	if code != http.StatusUnprocessableEntity {
		t.Errorf("follow again with a failing read = %d %s; want 422", code, body)
	}
	if got := r.get(c.ID); !got.Ended || got.EndReason != "unfollowed" {
		t.Errorf("campaign after the failed re-follow = %+v; want still ended as unfollowed", got)
	}
}

func TestServiceFollowOfAnUnfollowedCampaignLeavesItEnded(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.unfollow(c.ID)

	got, created, lerr := r.svc.Follow(context.Background(), rootURL)
	if lerr != nil || created || got.ID != c.ID || !got.Ended() {
		t.Errorf("Service.Follow of an unfollowed campaign = %+v, created %v, %v; want the same campaign, still ended", got, created, lerr)
	}
	if now := r.get(c.ID); !now.Ended {
		t.Errorf("campaign = %+v; want still ended", now)
	}
}
