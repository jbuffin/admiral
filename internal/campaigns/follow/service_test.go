package follow

import (
	"encoding/json"
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
		{28, "Issue 28 https://github.com/acme/other/issues/28", "Issue 28", "https://github.com/acme/other/issues/28", "https://github.com/acme/work"},
	}
	for _, tc := range cases {
		text, title, repo, url, panel, err := r.svc.Order(SourceKey, tc.url)
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
	_, _, repo, _, _, err := r.svc.Order(SourceKey, issURL(21))
	if err != nil || repo != "https://github.com/acme/work" {
		t.Errorf("repo = %q err %v, want the work repo first", repo, err)
	}
}

func TestLaunchedAnOrderRereadsTheCampaign(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.follow(rootURL)
	before := r.gh.rootReads()
	r.svc.Launched(SourceKey, issURL(21))
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
	if q.Source != SourceKey || q.OrderID != issURL(21) || q.Fleet != "work" || q.Harness != "claude" {
		t.Errorf("request = %+v, want source campaign, order id the child URL, fleet work, harness claude", q)
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
	for _, tc := range []struct {
		name         string
		before       []ships.Ship
		after        []ships.Ship
		wantRereread bool
	}{
		{"child ship anchors", []ships.Ship{child("underway")}, []ships.Ship{child("anchored")}, true},
		{"child ship is gone", []ships.Ship{child("underway")}, nil, true},
		{"child ship needs you", []ships.Ship{child("underway")}, []ships.Ship{child("needs_you")}, false},
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
