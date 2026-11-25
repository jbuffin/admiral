package crewapi_test

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTheUpdateStateFollowsTheListingOutcome(t *testing.T) {
	cases := map[string]struct {
		can  func(g *ghFake)
		want string
	}{
		"same blob": {func(g *ghFake) { g.upstreamListing("hubot/crew", map[string]string{"d": blobOf("d-old")}) }, "none"},
		"new blob":  {func(g *ghFake) { g.upstreamListing("hubot/crew", map[string]string{"d": blobOf("d-new")}) }, "available"},
		"file gone": {func(g *ghFake) { g.upstreamListing("hubot/crew", map[string]string{"other": blobOf("o")}) }, "removed"},
		"readable repo with no crew folder": {func(g *ghFake) {
			g.ok("GET", "repos/hubot/crew", repoAs("hubot/crew", "main"))
			g.answer("GET", "repos/hubot/crew/contents/crew", 404, nil, ghNotFound)
		}, "unknown"},
		"repo 404": {func(g *ghFake) { g.noAccessTo("hubot/crew") }, "no_access"},
		"other error": {func(g *ghFake) {
			g.answer("GET", "repos/hubot/crew/contents/crew", 500, nil, "gh: Server Error (HTTP 500)")
		}, "unknown"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
			p.clockIt(t)
			p.attach(att{name: "d"})
			c.can(p.gh)
			status, states := p.updates()
			wantStatus(t, "updates", status, 200, nil)
			if states["d"] != c.want {
				t.Errorf("states = %v, want d %s", states, c.want)
			}
			if got := p.updateOf("d"); got != c.want {
				t.Errorf("GET crew shows %q, want %s", got, c.want)
			}
		})
	}
}

func TestAPrivateRepoUpdatesCheckPreviewAndApplyLikeAPublicOne(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	u.gh.unseed()
	u.up.private = true
	u.gh.seed(u.up)
	u.gh.upstreamListing("hubot/crew", map[string]string{"scout": u.up.blob()})
	_, states := u.updates()
	if states["scout"] != "available" {
		t.Errorf("states = %v, want scout available", states)
	}
	u.clk.advance(time.Hour)
	pv := u.previewOK("scout")
	status, out := u.apply("scout", pv, nil)
	wantStatus(t, "apply", status, 200, out)
	if got := u.memberBytes("scout"); got != newScout {
		t.Errorf("member = %q, want the upstream bytes", got)
	}
	if got := slurp(u.sidecarPath("scout")); got == "" || strings.Contains(got, `"private"`) {
		t.Errorf("sidecar = %q, want one that records no visibility", got)
	}
}

func TestANoAccessResultIsNotReusedAndTheNextOpenChecksAgain(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	clk := p.clockIt(t)
	p.attach(att{name: "d"})
	p.gh.noAccessTo("hubot/crew")
	_, first := p.updates()
	if first["d"] != "no_access" {
		t.Fatalf("states = %v, want d no_access", first)
	}
	p.gh.unseed()
	p.gh.upstreamListing("hubot/crew", map[string]string{"d": blobOf("d-old")})
	clk.advance(time.Minute) // past the listing's 30 seconds, inside the five minutes a good result is reused
	_, second := p.updates()
	if second["d"] != "none" {
		t.Errorf("states = %v, want d none after access was granted", second)
	}
}

func TestOneUnreadableRepoDoesNotStopTheOthersBeingChecked(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	p.attach(att{name: "a", repo: "hubot/crew"})
	p.attach(att{name: "b", repo: "secret/crew"})
	p.attach(att{name: "c", repo: "hubot/crew"})
	p.gh.noAccessTo("secret/crew")
	p.gh.upstreamListing("hubot/crew", map[string]string{"a": blobOf("a-old"), "c": blobOf("c-new")})
	status, states := p.updates()
	wantStatus(t, "updates", status, 200, nil)
	if want := map[string]string{"a": "none", "b": "no_access", "c": "available"}; !reflect.DeepEqual(states, want) {
		t.Errorf("states = %v, want %v", states, want)
	}
}

func TestBrowseRowsShowTheCheckersLastNoAccessResult(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	p.attach(att{name: "scout-hubot", upstreamName: "scout"})
	p.gh.noAccessTo("hubot/crew")
	p.updates()
	p.gh.unseed()
	listUpstream(p, "scout")
	if got := browseRows(t, p)["scout"]["update"]; got != "no_access" {
		t.Errorf("the imported row has update %v, want the checker's last result, no_access", got)
	}
}

func TestANoAccessListingIsHeldThirtySecondsNotFiveMinutes(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	clk := p.clockIt(t)
	p.attach(att{name: "d"})
	p.gh.noAccessTo("hubot/crew")
	p.updates()
	clk.advance(29 * time.Second)
	p.updates()
	if got := len(p.gh.listings()); got != 1 {
		t.Fatalf("%d listing calls inside 30 seconds, want 1", got)
	}
	clk.advance(2 * time.Second)
	p.updates()
	if got := len(p.gh.listings()); got != 2 {
		t.Errorf("%d listing calls after 31 seconds, want 2", got)
	}
}

func TestTheSharingViewServesTheNoAccessTheUpdateCheckCached(t *testing.T) {
	p := newImp(t, impOpts{})
	p.clockIt(t)
	p.attach(att{name: "d", repo: shareRepo})
	p.gh.noAccessTo(shareRepo)
	_, states := p.updates()
	if states["d"] != "no_access" {
		t.Fatalf("states = %v, want d no_access", states)
	}
	status, out := p.getJSON("/sharing")
	wantStatus(t, "sharing", status, 200, out)
	if got := str(out, "published_error"); got != noAccessText(shareRepo) {
		t.Errorf("published_error = %q, want the no-access text", got)
	}
}
