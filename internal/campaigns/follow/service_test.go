package follow

import (
	"encoding/json"
	"github.com/jbuffin/admiral/internal/github"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ships"
)

func TestFollowRoutesToAFleetReadsTheTreeAndLaunchesNothing(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	if c.ID == "" || c.URL != rootURL || c.Fleet != "work" || c.Ended {
		t.Errorf("followed campaign = %+v, want id, the parent's URL, fleet work, not ended", c)
	}
	if len(r.routed) != 1 || r.routed[0] != "github.com acme/widgets" {
		t.Errorf("Route calls = %v, want one for github.com acme/widgets", r.routed)
	}
	if c.Root == nil || c.Root.Number != 20 || !c.Root.Parent {
		t.Fatalf("root = %+v, want the parent read", c.Root)
	}
	if c.Progress.Closed != 1 || c.Progress.Total != 7 {
		t.Errorf("progress = %+v, want 1 of 7 launchable children closed", c.Progress)
	}
	for num, want := range map[int]string{21: "ready", 22: "blocked", 23: "done", 25: "claimed-no-ship"} {
		if got := c.Root.find(num); got == nil || got.Status != want {
			t.Errorf("child %d = %+v, want status %s", num, got, want)
		}
	}
	if n := c.Root.find(26); n == nil || !n.Parent || n.Status != "" || n.find(27) == nil {
		t.Errorf("nested parent 26 = %+v, want a parent with no status holding 27", n)
	}
	if c.Root.find(21).Mode != "without-you" || c.Root.find(21).Skill.Kind != "commodore" {
		t.Errorf("child 21 mode/skill = %+v, want without-you + commodore from ready-for-agent", c.Root.find(21))
	}
	if got := r.launches(); len(got) != 0 {
		t.Errorf("following launched %v; following only reads", got)
	}
}

func TestFollowingTheSameCampaignTwiceReturnsTheFirst(t *testing.T) {
	r := newRig(t, fixtureTree())
	first := r.follow(rootURL)
	code, body := r.do("POST", "/api/campaigns", map[string]string{"reference": "acme/widgets#20"})
	var again campaignJSON
	_ = json.Unmarshal(body, &again)
	if code != http.StatusOK || again.ID != first.ID {
		t.Errorf("second follow = %d %s, want 200 with campaign %s", code, body, first.ID)
	}
	_, list := r.do("GET", "/api/campaigns", nil)
	if n := strings.Count(string(list), `"`+first.ID+`"`); n != 1 {
		t.Errorf("campaign list names %s %d times, want once: %s", first.ID, n, list)
	}
}

func TestFollowRefusesAReferenceWhoseHostTheFleetDoesNotHave(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.hasHost = func(h string) bool { return h == "github.com" }
	for _, ref := range []string{"https://ghe.example.com/acme/widgets/issues/20"} {
		code, body := r.do("POST", "/api/campaigns", map[string]string{"reference": ref})
		if code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
			t.Errorf("follow %q = %d %s, want a 400 or 422 refusal", ref, code, body)
		}
	}
	if n := r.gh.rootReads(); n != 0 {
		t.Errorf("a refused follow still read the tree %d times", n)
	}
	if _, list := r.do("GET", "/api/campaigns", nil); strings.Contains(string(list), "acme/widgets") {
		t.Errorf("a refused follow was stored: %s", list)
	}
}

func TestFollowComparesTheHostIgnoringCase(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow("https://GitHub.com/acme/widgets/issues/20")
	if c.Root == nil || c.Root.Number != 20 {
		t.Errorf("a differently cased host was not accepted: %+v", c)
	}
}

func TestShortReferenceMeansGitHubDotCom(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.hasHost = func(h string) bool { return false }
	code, body := r.do("POST", "/api/campaigns", map[string]string{"reference": "acme/widgets#20"})
	if code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
		t.Errorf("owner/repo#n with no github.com source = %d %s, want a 400 or 422 refusal", code, body)
	}
	r.hasHost = func(h string) bool { return h == "github.com" }
	r.follow("acme/widgets#20")
	if len(r.hostSeen) == 0 || r.hostSeen[len(r.hostSeen)-1] != "github.com" {
		t.Errorf("hosts asked of Clients = %v, want github.com for owner/repo#n", r.hostSeen)
	}
}

func TestUnfollowEndsTheCampaignAndKeepsItReadable(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	if code, body := r.do("POST", "/api/campaigns/"+c.ID+"/unfollow", nil); code != http.StatusOK {
		t.Fatalf("unfollow = %d %s", code, body)
	}
	if got := r.get(c.ID); !got.Ended || got.Root == nil {
		t.Errorf("after unfollow: %+v, want ended and still readable", got)
	}
	_, active := r.do("GET", "/api/campaigns?ended=false", nil)
	_, ended := r.do("GET", "/api/campaigns?ended=true", nil)
	if strings.Contains(string(active), c.ID) || !strings.Contains(string(ended), c.ID) {
		t.Errorf("ended=false lists %s, ended=true lists %s; want the campaign only under ended", active, ended)
	}
	r.clock.Advance(10 * time.Minute)
	reads := r.gh.rootReads()
	r.svc.Tick()
	holds(t, "an ended campaign was reread", func() bool { return r.gh.rootReads() > reads })
}

func TestOverrideAndWorkRepoRoutesChangeWhatTheCampaignShows(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	body := map[string]any{"child_url": issURL(21), "mode": "with-you", "skill": map[string]string{"kind": "none"}}
	if code, b := r.do("PUT", "/api/campaigns/"+c.ID+"/override", body); code != http.StatusOK {
		t.Fatalf("override = %d %s", code, b)
	}
	if code, b := r.do("PUT", "/api/campaigns/"+c.ID+"/work-repo", map[string]string{"repo": "https://github.com/acme/work"}); code != http.StatusOK {
		t.Fatalf("work-repo = %d %s", code, b)
	}
	got := r.get(c.ID)
	if n := got.Root.find(21); n.Mode != "with-you" || n.Skill.Kind != "none" {
		t.Errorf("child 21 after override = %+v, want with-you + none", n)
	}
	if got.WorkRepo != "https://github.com/acme/work" {
		t.Errorf("work repo = %q, want the one set", got.WorkRepo)
	}
}

func TestOrderAnswersACampaignChildAndPassesOthersOn(t *testing.T) {
	r := newRig(t, fixtureTree())
	next := &recordingOrders{}
	r.svc.Next = next
	c := r.follow(rootURL)
	r.do("PUT", "/api/campaigns/"+c.ID+"/work-repo", map[string]string{"repo": "https://github.com/acme/work"})

	cases := []struct {
		child            int
		text, title, url string
		repo             string
	}{
		{24, rootURL + " " + issURL(24), "Issue 24", issURL(24), "https://github.com/acme/work"},
		{27, issURL(27), "Issue 27", issURL(27), "https://github.com/acme/work"},
		{28, "Issue 28 https://github.com/acme/other/issues/28", "Issue 28", "https://github.com/acme/other/issues/28", "https://github.com/acme/other"},
	}
	for _, tc := range cases {
		text, title, repo, url, panel, err := r.svc.Order(SourceKey, OrderID(c.ID, tc.url))
		if err != nil || text != tc.text || title != tc.title || url != tc.url || repo != tc.repo || panel != c.ID {
			t.Errorf("Order(campaign, %d) = text %q title %q repo %q url %q panel %q err %v\nwant text %q title %q repo %q url %q panel %q",
				tc.child, text, title, repo, url, panel, err, tc.text, tc.title, tc.repo, tc.url, c.ID)
		}
	}
	if _, _, _, _, _, err := r.svc.Order("jira", "PROJ-1"); err != nil || next.last != "jira PROJ-1" {
		t.Errorf("an order of another source did not reach Next: last %q err %v", next.last, err)
	}
}

type recordingOrders struct{ last string }

func (o *recordingOrders) Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error) {
	o.last = source + " " + id
	return "", "", "", "", "", nil
}
func (o *recordingOrders) Launched(source, id string) {}

func TestOrderPrefersTheWorkRepoThenTheChildsOwn(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.do("PUT", "/api/campaigns/"+c.ID+"/work-repo", map[string]string{"repo": "https://github.com/acme/work"})
	_, _, repo, _, _, err := r.svc.Order(SourceKey, OrderID(c.ID, issURL(21)))
	if err != nil || repo != "https://github.com/acme/work" {
		t.Errorf("repo = %q err %v, want the work repo first", repo, err)
	}
}

func TestLaunchedAnOrderRereadsTheCampaign(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	before := r.gh.rootReads()
	r.svc.Launched(SourceKey, OrderID(c.ID, issURL(21)))
	eventually(t, "a reread after Launched", func() bool { return r.gh.rootReads() > before })
}

func TestLaunchHandsTheLauncherAClaimedSourceOrderWithAutopilotOff(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	code, body := r.launch(c.ID, map[string]any{"child_url": issURL(21), "harness": "claude", "dir": "/work/widgets", "autopilot": true})
	if code != http.StatusCreated {
		t.Fatalf("launch = %d %s, want 201", code, body)
	}
	got := r.launches()
	if len(got) != 1 {
		t.Fatalf("Launch called %d times, want once", len(got))
	}
	q := got[0]
	if q.Source != SourceKey || q.OrderID != OrderID(c.ID, issURL(21)) || q.Fleet != "work" || q.Harness != "claude" {
		t.Errorf("request = %+v, want source campaign, order id campaign/child URL, fleet work, harness claude", q)
	}
	if q.Skill.Kind != "commodore" || q.Autopilot {
		t.Errorf("skill %+v autopilot %v, want commodore with Autopilot false even though the body asked for it", q.Skill, q.Autopilot)
	}
}

func TestLaunchUsesTheModeRowsSkill(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	for _, tc := range []struct {
		child int
		kind  string
		name  string
	}{{24, "skill", "wayfinder"}, {27, "skill", "wayfinder"}, {28, "none", ""}} {
		if code, body := r.launch(c.ID, map[string]any{"child_url": r.get(c.ID).Root.find(tc.child).URL, "harness": "claude", "dir": "/work/x"}); code != http.StatusCreated {
			t.Fatalf("launch %d = %d %s", tc.child, code, body)
		}
	}
	got := r.launches()
	if len(got) != 3 {
		t.Fatalf("Launch called %d times, want 3", len(got))
	}
	for i, want := range []struct{ kind, name string }{{"skill", "wayfinder"}, {"skill", "wayfinder"}, {"none", ""}} {
		if got[i].Skill.Kind != want.kind || got[i].Skill.Name != want.name {
			t.Errorf("launch %d skill = %+v, want %+v", i, got[i].Skill, want)
		}
	}
}

func TestLaunchRefusesWhatIsNotLaunchable(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.mu.Lock()
	r.claims[issURL(21)] = "bold-heron"
	r.mu.Unlock()
	for _, tc := range []struct {
		name  string
		child int
		extra map[string]any
		code  int
		want  string
	}{
		{"done", 23, nil, http.StatusConflict, ""},
		{"nested parent", 26, nil, http.StatusBadRequest, ""},
		{"held by a ship", 21, nil, http.StatusConflict, "bold-heron"},
		{"blocked", 22, nil, http.StatusConflict, issURL(21)},
		{"claimed with no ship", 25, nil, http.StatusConflict, ""},
		{"not in the campaign", 99, nil, http.StatusNotFound, ""},
	} {
		body := map[string]any{"child_url": issURL(tc.child), "harness": "claude", "dir": "/work/x"}
		code, msg := r.launch(c.ID, body)
		if code != tc.code || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: launch = %d %s, want %d naming %q", tc.name, code, msg, tc.code, tc.want)
		}
	}
	if got := r.launches(); len(got) != 0 {
		t.Errorf("a refused launch still reached Launch: %v", got)
	}
	code, msg := r.launch(c.ID, map[string]any{"child_url": issURL(25), "harness": "claude", "dir": "/work/x", "start_anyway": true})
	if code != http.StatusCreated {
		t.Errorf("claimed-no-ship with start_anyway = %d %s, want 201", code, msg)
	}
}

func TestLaunchRereadsWhenTheLastReadIsOverAMinuteOld(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	closed := fixtureTree()
	closed.kids[0].closed = true
	r.gh.set(closed)
	r.clock.Advance(30 * time.Second)
	reads := r.gh.rootReads()
	if code, _ := r.launch(c.ID, map[string]any{"child_url": issURL(21), "harness": "claude", "dir": "/work/x"}); code != http.StatusCreated || r.gh.rootReads() != reads {
		t.Errorf("a 30 s old read was reread (reads %d to %d) or the launch refused (%d)", reads, r.gh.rootReads(), code)
	}
	r.clock.Advance(2 * time.Minute)
	if code, msg := r.launch(c.ID, map[string]any{"child_url": issURL(21), "harness": "claude", "dir": "/work/x"}); code != http.StatusConflict || r.gh.rootReads() <= reads {
		t.Errorf("after 2 min the launch = %d %s with %d reads, want it to reread, see the child closed and refuse with 409", code, msg, r.gh.rootReads())
	}
}

func TestOpeningTheCampaignRereadsNoMoreOftenThanEvery30Seconds(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	reads := r.gh.rootReads()
	r.clock.Advance(10 * time.Second)
	r.get(c.ID)
	holds(t, "opening the page within 30 s reread the tree", func() bool { return r.gh.rootReads() > reads })
	r.clock.Advance(25 * time.Second)
	r.get(c.ID)
	eventually(t, "a reread once 30 s passed", func() bool { return r.gh.rootReads() > reads })
}

func TestOnlyOneRereadRunsAtATime(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.gh.delay(time.Second)
	reads := r.gh.rootReads()
	r.clock.Advance(time.Minute)
	for i := 0; i < 5; i++ {
		r.get(c.ID)
	}
	time.Sleep(300 * time.Millisecond)
	if got := r.gh.rootReads() - reads; got > 1 {
		t.Errorf("%d rereads started while one was in flight, want at most 1", got)
	}
}

func TestAChildShipAnchoringOrLeavingRereadsTheCampaign(t *testing.T) {
	child := func(status string) ships.Ship {
		return ships.Ship{Name: "calm-otter", Status: status, URL: issURL(21), Source: SourceKey}
	}
	// its first issue is elsewhere: only a later one is a child of the campaign
	slice := func(status string) ships.Ship {
		other := "https://github.com/other/repo/issues/1"
		return ships.Ship{Name: "slice", Status: status, URL: other, Issues: []string{other, issURL(21)}}
	}
	for _, tc := range []struct {
		name         string
		before       []ships.Ship
		after        []ships.Ship
		wantRereread bool
	}{
		{"child ship anchors", []ships.Ship{child("underway")}, []ships.Ship{child("anchored")}, true},
		{"child ship is gone", []ships.Ship{child("underway")}, nil, true},
		{"child ship needs you", []ships.Ship{child("underway")}, []ships.Ship{child("needs_you")}, false},
		{"slice ship anchors", []ships.Ship{slice("underway")}, []ships.Ship{slice("anchored")}, true},
		{"slice ship is gone", []ships.Ship{slice("underway")}, nil, true},
		{"unrelated ship anchors", []ships.Ship{{Name: "x", Status: "underway", URL: "https://github.com/other/repo/issues/1"}}, []ships.Ship{{Name: "x", Status: "anchored", URL: "https://github.com/other/repo/issues/1"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, fixtureTree())
			r.follow(rootURL)
			r.setShips(tc.before...)
			r.svc.ShipsChanged()
			holds(t, "the baseline snapshot caused a reread", func() bool { return r.gh.rootReads() > 1 })
			r.setShips(tc.after...)
			r.svc.ShipsChanged()
			if tc.wantRereread {
				eventually(t, "a reread after the child's ship changed", func() bool { return r.gh.rootReads() > 1 })
			} else {
				holds(t, "an unrelated change caused a reread", func() bool { return r.gh.rootReads() > 1 })
			}
		})
	}
}

func TestShipsChangedDoesNotBlockOnTheReread(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.follow(rootURL)
	r.setShips(ships.Ship{Name: "calm-otter", Status: "underway", URL: issURL(21)})
	r.svc.ShipsChanged()
	r.gh.delay(time.Second)
	r.setShips()
	start := time.Now()
	r.svc.ShipsChanged()
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("ShipsChanged took %v with a slow reread, want it to return at once", d)
	}
}

func TestTickRereadsEveryFiveMinutesByTheInjectedClock(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.follow(rootURL)
	reads := r.gh.rootReads()
	r.clock.Advance(4 * time.Minute)
	r.svc.Tick()
	holds(t, "a tick before 5 min reread", func() bool { return r.gh.rootReads() > reads })
	r.clock.Advance(90 * time.Second)
	r.svc.Tick()
	eventually(t, "a tick at 5 min reread", func() bool { return r.gh.rootReads() > reads })
}

func TestAFailedRereadKeepsTheLastTreeAndShowsTheError(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	if err := writeFail(r.gh, "acme_widgets_20", "gh: HTTP 502"); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(10 * time.Minute)
	r.svc.Tick()
	eventually(t, "the read error to show", func() bool { return r.get(c.ID).ReadErr != "" })
	if got := r.get(c.ID); got.Root == nil || got.Root.find(21) == nil {
		t.Errorf("after a failed read the tree is %+v, want the last good tree kept", got.Root)
	}
}

func TestACampaignEndsWhenEveryNodeIsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		tree func() iss
		ends bool
	}{
		{"every node closed", func() iss { return closeAll(fixtureTree(), true, true) }, true},
		{"root closed, children open", func() iss { t := fixtureTree(); t.closed = true; return t }, false},
		{"leaves closed under an open root", func() iss { return closeAll(fixtureTree(), false, true) }, false},
		{"root and leaves closed, nested parent open", func() iss {
			t := closeAll(fixtureTree(), true, true)
			t.kids[5].closed = false
			return t
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, fixtureTree())
			c := r.follow(rootURL)
			r.gh.set(tc.tree())
			r.clock.Advance(10 * time.Minute)
			r.svc.Tick()
			if tc.ends {
				eventually(t, "the campaign to end", func() bool { return r.get(c.ID).Ended })
			} else {
				holds(t, "the campaign ended while work is open", func() bool { return r.get(c.ID).Ended })
			}
		})
	}
}

func closeAll(i iss, root, leaves bool) iss {
	if len(i.kids) == 0 {
		i.closed = leaves
		return i
	}
	i.closed = root
	kids := make([]iss, len(i.kids))
	for k, c := range i.kids {
		kids[k] = closeAll(c, root, leaves)
	}
	i.kids = kids
	return i
}

func writeFail(g *fakeGH, key, msg string) error {
	return writeFile(g.dir+"/resp/"+key+".fail", msg)
}

func TestCloneLaunchCarriesTheRepoInTheLaunchersIdentityForm(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.do("PUT", "/api/campaigns/"+c.ID+"/work-repo", map[string]string{"repo": "https://github.com/Acme/Work"})
	if code, body := r.launch(c.ID, map[string]any{"child_url": issURL(21), "harness": "claude", "clone": true}); code != http.StatusCreated {
		t.Fatalf("clone launch = %d %s, want 201", code, body)
	}
	if code, body := r.launch(c.ID, map[string]any{"child_url": issURL(24), "harness": "claude", "clone": true, "clone_repo": "github.com/acme/shown"}); code != http.StatusCreated {
		t.Fatalf("clone launch with clone_repo = %d %s, want 201", code, body)
	}
	got := r.launches()
	if len(got) != 2 || !got[0].Clone || got[0].CloneRepo != "github.com/acme/work" || got[1].CloneRepo != "github.com/acme/shown" {
		t.Errorf("launches = %+v, want clone with host/owner/repo %q, and the form's clone_repo passed through", got, "github.com/acme/work")
	}
}

func TestTheWorkRepoIsKeptAsItsIdentityWithoutUserinfo(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	code, body := r.do("PUT", "/api/campaigns/"+c.ID+"/work-repo", map[string]string{"repo": "https://user:fake-secret-1@github.com/acme/work?x=1"})
	if code != http.StatusOK || strings.Contains(string(body), "fake-secret-1") {
		t.Fatalf("work-repo = %d %s, want 200 with no userinfo in the answer", code, body)
	}
	if got := r.get(c.ID).WorkRepo; got != "https://github.com/acme/work" {
		t.Errorf("work repo = %q, want https://github.com/acme/work", got)
	}
}

func TestTheSameChildInTwoCampaignsLaunchesWithEachCampaignsOwnChoices(t *testing.T) {
	r := newRig(t, fixtureTree())
	a := r.follow(rootURL)
	other := iss{num: 30, labels: []string{"wayfinder:map"}, kids: []iss{{num: 21, labels: []string{"ready-for-agent"}}}}
	r.gh.set(other)
	b := r.follow(issURL(30))
	r.do("PUT", "/api/campaigns/"+a.ID+"/work-repo", map[string]string{"repo": "https://github.com/acme/worka"})
	r.do("PUT", "/api/campaigns/"+b.ID+"/work-repo", map[string]string{"repo": "https://github.com/acme/workb"})
	r.do("PUT", "/api/campaigns/"+b.ID+"/override", map[string]any{"child_url": issURL(21), "mode": "with-you", "skill": map[string]string{"kind": "none"}})

	for id, want := range map[string]struct{ kind, repo string }{a.ID: {"commodore", "https://github.com/acme/worka"}, b.ID: {"none", "https://github.com/acme/workb"}} {
		text, _, repo, _, panel, err := r.svc.Order(SourceKey, OrderID(id, issURL(21)))
		if err != nil || repo != want.repo || panel != id {
			t.Errorf("Order in campaign %s = repo %q panel %q err %v, want repo %q and its own panel", id, repo, panel, err, want.repo)
		}
		if (want.kind == "none") != strings.HasPrefix(text, "Issue 21 ") {
			t.Errorf("Order in campaign %s text = %q, want the title and URL only when the campaign's override has no skill", id, text)
		}
	}
	if code, body := r.launch(a.ID, map[string]any{"child_url": issURL(21), "harness": "claude", "dir": "/work/x"}); code != http.StatusCreated {
		t.Fatalf("launch from A = %d %s", code, body)
	}
	r.mu.Lock()
	r.claims[issURL(21)] = "calm-otter" // the ship the first launch made
	r.mu.Unlock()
	if code, body := r.launch(b.ID, map[string]any{"child_url": issURL(21), "harness": "claude", "dir": "/work/x"}); code != http.StatusConflict || !strings.Contains(body, "calm-otter") {
		t.Errorf("launch from B while a ship holds the child = %d %s, want 409 naming the ship", code, body)
	}
	got := r.launches()
	if len(got) != 1 || got[0].OrderID != OrderID(a.ID, issURL(21)) {
		t.Errorf("launches = %+v, want only A's, with A's campaign in the order id", got)
	}
}

func TestShutdownIsNotRecordedAsAReadError(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.gh.delay(2 * time.Second)
	r.clock.Advance(10 * time.Minute)
	r.svc.Tick()
	time.Sleep(200 * time.Millisecond)
	r.svc.Close()
	if got := r.get(c.ID).ReadErr; got != "" {
		t.Errorf("read error after shutdown = %q, want none", got)
	}
}

func (r *rig) list(query string) (raw string, got []campaignJSON) {
	r.t.Helper()
	code, body := r.do("GET", "/api/campaigns"+query, nil)
	var out struct {
		Campaigns []campaignJSON `json:"campaigns"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &out) != nil {
		r.t.Fatalf("GET /api/campaigns = %d %s", code, body)
	}
	return string(body), out.Campaigns
}

func TestTheListNamesEachCampaignWithoutItsTree(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.do("POST", "/api/campaigns/"+c.ID+"/unfollow", nil)
	_, got := r.list("")
	if len(got) != 1 {
		t.Fatalf("list = %+v, want one campaign", got)
	}
	g := got[0]
	if g.Title != "Issue 20" || g.Number != 20 || g.Repo != "https://github.com/acme/widgets" || g.FollowedAt == "" || g.EndedAt == "" || g.Root != nil {
		t.Errorf("list entry = %+v, want title, number, repo, followed_at, ended_at and no tree", g)
	}
	r2 := newRig(t, fixtureTree())
	r2.follow(rootURL)
	if _, got := r2.list(""); got[0].EndedAt != "" {
		t.Errorf("ended_at = %q for a followed campaign, want none", got[0].EndedAt)
	}
}

func TestReadyChildrenAreCountedByResolvedMode(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	check := func(when string, with, without int) {
		t.Helper()
		_, l := r.list("")
		for _, got := range []campaignJSON{r.get(c.ID), l[0]} {
			if got.ReadyWithYou != with || got.ReadyWithoutYou != without {
				t.Errorf("%s: ready with you %d, without %d, want %d and %d", when, got.ReadyWithYou, got.ReadyWithoutYou, with, without)
			}
		}
	}
	check("by default (28 with you; 21, 24, 27 without)", 1, 3)
	r.do("PUT", "/api/campaigns/"+c.ID+"/override", map[string]any{"child_url": issURL(21), "mode": "with-you"})
	check("after 21 is overridden to with you", 2, 2)
}

func TestNodesCarryLabelsAndTheirOverrideAgainstTheDefault(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.do("PUT", "/api/campaigns/"+c.ID+"/override", map[string]any{"child_url": issURL(21), "mode": "with-you", "skill": map[string]string{"kind": "none"}})
	got := r.get(c.ID)
	if l := got.Root.Labels; len(l) != 1 || l[0] != "wayfinder:map" {
		t.Errorf("root labels = %v, want the root's kind", l)
	}
	if l := got.Root.find(24).Labels; len(l) != 1 || l[0] != "wayfinder:research" {
		t.Errorf("child 24 labels = %v", l)
	}
	n := got.Root.find(21)
	if !n.Overridden || n.Mode != "with-you" || n.Skill.Kind != "none" || n.DefaultMode != "without-you" || n.DefaultSkill.Kind != "commodore" {
		t.Errorf("child 21 = %+v, want overridden to with-you/none over the default without-you/commodore", n)
	}
	if n := got.Root.find(24); n.Overridden || n.DefaultMode != "without-you" {
		t.Errorf("child 24 = overridden %v default %q, want not overridden and its default mode", n.Overridden, n.DefaultMode)
	}
}

func TestTheCappedBlockerStandInIsMarked(t *testing.T) {
	r := newRig(t, fixtureTree())
	tree := &github.TreeNode{Number: 20, Repo: "acme/widgets", URL: rootURL, Parent: true, Children: []*github.TreeNode{
		{Number: 21, Repo: "acme/widgets", URL: issURL(21), BlockedBy: []github.Blocker{
			{URL: issURL(21), Title: "More Blocked by lines than Admiral reads; check the issue", Capped: true},
			{URL: issURL(22), Title: "Issue 22"}}},
		{Number: 22, Repo: "acme/widgets", URL: issURL(22)}}}
	f, _, err := r.svc.Store.Follow(Followed{URL: rootURL, Fleet: "work", FollowedAt: r.clock.Now(), Tree: tree, ReadAt: r.clock.Now()})
	if err != nil {
		t.Fatal(err)
	}
	n := r.get(f.ID).Root.find(21)
	if len(n.Blockers) != 2 || !n.Blockers[0].Capped || n.Blockers[1].Capped {
		t.Errorf("blockers = %+v, want the stand-in capped and the real blocker not", n.Blockers)
	}
}

func TestTheViewSaysWhileAReadIsInFlightAndWhenItLastTried(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.gh.delay(100 * time.Millisecond) // a tree read makes several gh calls
	r.clock.Advance(10 * time.Minute)
	r.svc.Tick()
	eventually(t, "reading", func() bool { return r.get(c.ID).Reading })
	eventually(t, "the read to finish", func() bool { return !r.get(c.ID).Reading })
	want := r.clock.Now().UTC().Format(time.RFC3339)
	if got := r.get(c.ID); got.LastTried != want {
		t.Errorf("last_tried = %q, want %q", got.LastTried, want)
	}
	if err := writeFail(r.gh, "acme_widgets_20", "gh: HTTP 502"); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(10 * time.Minute)
	r.svc.Tick()
	want = r.clock.Now().UTC().Format(time.RFC3339)
	eventually(t, "a failed attempt to show in last_tried", func() bool {
		g := r.get(c.ID)
		return g.ReadErr != "" && g.LastTried == want && g.LastRead != want
	})
}

func TestTheGetThatStartsARereadAnswersReading(t *testing.T) {
	r := newRig(t, fixtureTree())
	c := r.follow(rootURL)
	r.gh.delay(100 * time.Millisecond)
	r.clock.Advance(time.Minute)
	if got := r.get(c.ID); !got.Reading {
		t.Errorf("the page open that started a reread answered reading %v, want true", got.Reading)
	}
}

func TestLastTriedSurvivesARestart(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	f := followed(t, s)
	at := time.Unix(5000, 0).UTC()
	if err := s.SetRead(f.ID, nil, at, "gh: HTTP 502"); err != nil {
		t.Fatal(err)
	}
	got, _ := openStore(t, home).Get(f.ID)
	if !got.TriedAt.Equal(at) || got.ReadError == "" {
		t.Errorf("after a restart tried %v error %q, want the failed attempt's time %v", got.TriedAt, got.ReadError, at)
	}
}

func TestASliceShipShowsOnEveryChildItHoldsUntilItIsGone(t *testing.T) {
	r := newRig(t, iss{num: 20, labels: []string{"wayfinder:map"}, kids: []iss{
		{num: 21, labels: []string{"ready-for-agent"}},
		{num: 29, labels: []string{"ready-for-agent"}},
	}})
	r.svc.Claims = func() map[string]string { return ships.ClaimsOf(r.svc.Ships()) }
	c := r.follow(rootURL)
	r.setShips(ships.Ship{Name: "slice", Status: ships.StatusUnderway, URL: issURL(21), Issues: []string{issURL(21), issURL(29)}})
	got := r.get(c.ID)
	for _, num := range []int{21, 29} {
		if n := got.Root.find(num); n == nil || n.Status != "underway" || n.Ship != "slice" {
			t.Errorf("child %d = %+v, want underway with ship slice", num, n)
		}
	}
	r.setShips()
	got = r.get(c.ID)
	for _, num := range []int{21, 29} {
		if n := got.Root.find(num); n == nil || n.Status != "ready" {
			t.Errorf("child %d = %+v, want ready again once the slice ship is gone", num, n)
		}
	}
}

func sliceBody(issues ...int) map[string]any {
	urls := make([]string, 0, len(issues))
	for _, n := range issues {
		urls = append(urls, issURL(n))
	}
	return map[string]any{"issues": urls, "harness": "claude", "dir": "/work/x"}
}

func TestLaunchASliceStartsOneCommodoreShipHoldingEveryChildAndNamingThemInItsText(t *testing.T) {
	r := newRig(t, sliceTree())
	c := r.follow(rootURL)
	body := sliceBody(32, 31)
	body["autopilot"] = true
	if code, msg := r.launch(c.ID, body); code != http.StatusCreated {
		t.Fatalf("slice launch = %d %s, want 201", code, msg)
	}
	got := r.launches()
	if len(got) != 1 {
		t.Fatalf("Launch called %d times, want once for the whole slice", len(got))
	}
	q := got[0]
	if q.Source != SourceKey || q.Fleet != "work" || q.Skill.Kind != "commodore" || q.Autopilot {
		t.Errorf("request = %+v, want source campaign, fleet work, Commodore, autopilot off", q)
	}
	if want := []string{issURL(32), issURL(31)}; strings.Join(q.Issues, " ") != strings.Join(want, " ") {
		t.Errorf("Issues = %v, want the children in the order given %v", q.Issues, want)
	}
	if want := issURL(32) + "\n" + issURL(31); strings.TrimSpace(q.Text) != want {
		t.Errorf("Text = %q, want every child URL one per line: %q", q.Text, want)
	}
	text, title, repo, url, panel, err := r.svc.Order(SourceKey, q.OrderID)
	if err != nil || text != q.Text || title == "" || url != issURL(32) || repo != "https://github.com/acme/widgets" || panel != c.ID {
		t.Errorf("Order(slice) = text %q title %q repo %q url %q panel %q err %v, want the slice's text, a title, the first child's URL, the repo and the campaign", text, title, repo, url, panel, err)
	}
	before := r.gh.rootReads()
	r.svc.Launched(SourceKey, q.OrderID)
	eventually(t, "a reread after the slice launched", func() bool { return r.gh.rootReads() > before })

	r.mu.Lock()
	r.claims[issURL(31)], r.claims[issURL(32)] = "calm-otter", "calm-otter"
	r.mu.Unlock()
	view := r.get(c.ID)
	for _, n := range []int{31, 32} {
		if ch := view.Root.find(n); ch.Status != "underway" || ch.Ship != "calm-otter" {
			t.Errorf("child %d = %s on %q, want underway with the slice's ship", n, ch.Status, ch.Ship)
		}
	}
}

func TestLaunchASliceClonesTheRepoOfItsChildren(t *testing.T) {
	r := newRig(t, sliceTree())
	c := r.follow(rootURL)
	body := sliceBody(31, 32)
	delete(body, "dir")
	body["clone"] = true
	if code, msg := r.launch(c.ID, body); code != http.StatusCreated {
		t.Fatalf("slice launch = %d %s, want 201", code, msg)
	}
	if got := r.launches(); len(got) != 1 || !got[0].Clone || got[0].CloneRepo != "github.com/acme/widgets" {
		t.Errorf("launches = %+v, want one cloning github.com/acme/widgets", got)
	}
}

func TestLaunchASliceAcceptsAChildOverriddenToCommodoreWithoutYou(t *testing.T) {
	r := newRig(t, sliceTree())
	c := r.follow(rootURL)
	r.do("PUT", "/api/campaigns/"+c.ID+"/override", map[string]any{"child_url": issURL(37), "mode": "without-you", "skill": map[string]string{"kind": "commodore"}})
	if code, msg := r.launch(c.ID, sliceBody(31, 37)); code != http.StatusCreated {
		t.Errorf("slice with an overridden child = %d %s, want 201", code, msg)
	}
}

func TestLaunchASliceRefusesWhatIsNotASlice(t *testing.T) {
	r := newRig(t, sliceTree())
	c := r.follow(rootURL)
	r.mu.Lock()
	r.claims[issURL(33)] = "bold-heron"
	r.mu.Unlock()
	both := sliceBody(31, 32)
	both["child_url"] = issURL(31)
	trailing := sliceBody(31, 32)
	trailing["issues"] = []string{issURL(31), issURL(32) + "/", issURL(31) + "/"}
	anyway := sliceBody(31, 36)
	anyway["start_anyway"] = true
	for _, tc := range []struct {
		name     string
		body     map[string]any
		code     int // 0 for any 4xx
		wantText string
	}{
		{"one issue", sliceBody(31), 400, ""},
		{"no issue", map[string]any{"issues": []string{}, "harness": "claude", "dir": "/work/x"}, 400, ""},
		{"a URL twice", sliceBody(31, 32, 31), 400, ""},
		{"a URL twice once normalised", trailing, 400, ""},
		{"child_url and issues together", both, 400, ""},
		{"not in the campaign", sliceBody(31, 99), 404, ""},
		{"a parent", sliceBody(31, 40), 400, ""},
		{"a done child", sliceBody(31, 35), 409, issURL(35)},
		{"an underway child", sliceBody(31, 33), 409, "bold-heron"},
		{"a blocked child", sliceBody(32, 34), 409, issURL(31)},
		{"a claimed child with no ship", sliceBody(31, 36), 0, issURL(36)},
		{"start_anyway does not apply to a slice", anyway, 0, issURL(36)},
		{"a With you child", sliceBody(31, 37), 0, issURL(37)},
		{"a child on another skill", sliceBody(31, 38), 0, issURL(38)},
		{"children of two repos", sliceBody(31, 39), 400, ""},
	} {
		code, msg := r.launch(c.ID, tc.body)
		if (tc.code != 0 && code != tc.code) || (tc.code == 0 && (code < 400 || code > 499)) || !strings.Contains(msg, tc.wantText) || strings.TrimSpace(msg) == "" {
			t.Errorf("%s: launch = %d %s, want %d naming %q", tc.name, code, msg, tc.code, tc.wantText)
		}
	}
	if got := r.launches(); len(got) != 0 {
		t.Errorf("a refused slice still reached Launch: %v", got)
	}
}

func TestLaunchASliceAlreadyHeldByAShipIsRefusedNamingTheShip(t *testing.T) {
	r := newRig(t, sliceTree())
	c := r.follow(rootURL)
	if code, msg := r.launch(c.ID, sliceBody(31, 32)); code != http.StatusCreated {
		t.Fatalf("first slice launch = %d %s, want 201", code, msg)
	}
	r.mu.Lock()
	r.claims[issURL(31)], r.claims[issURL(32)] = "calm-otter", "calm-otter"
	r.mu.Unlock()
	for _, tc := range []struct {
		name  string
		issue []int
	}{{"the same set in reverse", []int{32, 31}}, {"one of them with a third child", []int{32, 33}}} {
		if code, msg := r.launch(c.ID, sliceBody(tc.issue...)); code != http.StatusConflict || !strings.Contains(msg, "calm-otter") {
			t.Errorf("%s: launch = %d %s, want 409 naming calm-otter", tc.name, code, msg)
		}
	}
	if got := r.launches(); len(got) != 1 {
		t.Errorf("Launch called %d times, want only the first slice", len(got))
	}
}
