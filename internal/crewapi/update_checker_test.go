package crewapi_test

import (
	"reflect"
	"testing"
	"time"
)

// --- the background check ---

func TestUpdateBackgroundCheckWaitsTheFirstDelayThenEveryTwentyFourHours(t *testing.T) {
	t.Setenv("ADMIRAL_CREW_UPDATE_FIRST_CHECK", "7m")
	u := newUpdatable(t, impOpts{})
	u.runChecker()

	eventually(t, "the first check to be scheduled", func() bool { return reflect.DeepEqual(u.clk.pending(), []time.Duration{7 * time.Minute}) })
	u.wantCalls("before the first check", 0)

	u.clk.advance(7 * time.Minute)
	u.clk.fire()
	eventually(t, "the first check's listing call", func() bool { return len(u.gh.listings()) == 1 })
	eventually(t, "the next check to be scheduled", func() bool { return reflect.DeepEqual(u.clk.pending(), []time.Duration{24 * time.Hour}) })
	if got := u.peekUpdate("scout"); got != "available" {
		t.Errorf("after the first check scout is %q, want available", got)
	}

	u.gh.unseed()
	u.gh.upstreamListing("hubot/crew", map[string]string{"scout": blobOf("scout-old")})
	u.clk.advance(24 * time.Hour)
	u.clk.fire()
	eventually(t, "the second check's listing call", func() bool { return len(u.gh.listings()) == 2 })
	eventually(t, "scout to read as up to date", func() bool { return u.peekUpdate("scout") == "none" })
}

func TestUpdateBackgroundCheckOfAFleetWithAShareRepoAndNoAttachedMemberMakesJustTheShareRepoCall(t *testing.T) {
	t.Setenv("ADMIRAL_CREW_UPDATE_FIRST_CHECK", "1m")
	p := newImp(t, impOpts{})
	clk := p.clockIt(t)
	p.gh.published("bosun")
	p.runChecker()
	eventually(t, "the first check to be scheduled", func() bool { return len(clk.pending()) == 1 })
	clk.fire()
	eventually(t, "the share repo listing", func() bool { return len(p.gh.listings()) == 1 })
	p.wantCalls("a check with no attached member", 1)
	if got := p.gh.listings()[0]; got != listingPath {
		t.Errorf("listed %s, want the share repo's %s", got, listingPath)
	}
}

func TestUpdateBackgroundCheckOfAFleetWithNothingToCheckMakesNoCall(t *testing.T) {
	t.Setenv("ADMIRAL_CREW_UPDATE_FIRST_CHECK", "1m")
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	clk := p.clockIt(t)
	p.member("plain", "Mine.")
	p.runChecker()
	eventually(t, "the first check to be scheduled", func() bool { return len(clk.pending()) == 1 })
	clk.fire()
	eventually(t, "the next check to be scheduled", func() bool { return reflect.DeepEqual(clk.pending(), []time.Duration{24 * time.Hour}) })
	p.wantCalls("a fleet with no attached member and no share repo", 0)
}

func TestUpdateBackgroundCheckWithNoGitHubClientMakesNoCall(t *testing.T) {
	t.Setenv("ADMIRAL_CREW_UPDATE_FIRST_CHECK", "1m")
	u := newUpdatable(t, impOpts{pubOpts: pubOpts{noGitHub: true}})
	u.runChecker()
	eventually(t, "the first check to be scheduled", func() bool { return len(u.clk.pending()) == 1 })
	u.clk.fire()
	eventually(t, "the next check to be scheduled", func() bool { return reflect.DeepEqual(u.clk.pending(), []time.Duration{24 * time.Hour}) })
	u.wantCalls("a fleet with no github.com client", 0)
}

// --- one call per repo ---

func TestUpdateCheckMakesOneListingCallPerRepoIgnoringCase(t *testing.T) {
	for name, tc := range map[string]struct {
		o     impOpts
		repos int
	}{
		"no share repo":                  {impOpts{pubOpts: pubOpts{noRepo: true}}, 2},
		"share repo is one of the repos": {impOpts{}, 2},
	} {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, tc.o)
			p.clockIt(t)
			share := shareRepo
			if tc.o.noRepo {
				share = "other/crew"
			}
			p.attach(att{name: "a", repo: "hubot/crew"})
			p.attach(att{name: "b", repo: "hubot/crew"})
			p.attach(att{name: "c", repo: "Hubot/Crew"})
			p.attach(att{name: "d", repo: share})
			p.gh.upstreamListing("hubot/crew", map[string]string{"a": blobOf("a-old"), "b": blobOf("b-old"), "c": blobOf("c-old")})
			p.gh.upstreamListing("Hubot/Crew", map[string]string{"a": blobOf("a-old"), "b": blobOf("b-old"), "c": blobOf("c-old")})
			p.gh.upstreamListing(share, map[string]string{"d": blobOf("d-old")})
			status, states := p.updates()
			wantStatus(t, "updates", status, 200, nil)
			if got := len(p.gh.listings()); got != tc.repos {
				t.Errorf("%d listing calls %v, want %d", got, p.gh.listings(), tc.repos)
			}
			if !reflect.DeepEqual(states, map[string]string{"a": "none", "b": "none", "c": "none", "d": "none"}) {
				t.Errorf("states = %v", states)
			}
		})
	}
}

func TestUpdateCheckWithADifferentShareRepoMakesOneMoreCall(t *testing.T) {
	p := newImp(t, impOpts{})
	p.clockIt(t)
	p.attach(att{name: "a", repo: "hubot/crew"})
	p.attach(att{name: "b", repo: "mate/crew"})
	p.gh.upstreamListing("hubot/crew", map[string]string{"a": blobOf("a-old")})
	p.gh.upstreamListing("mate/crew", map[string]string{"b": blobOf("b-old")})
	p.gh.published("bosun")
	status, _ := p.updates()
	wantStatus(t, "updates", status, 200, nil)
	if got := p.gh.listings(); len(got) != 3 {
		t.Errorf("%d listing calls %v, want 3 (two repos and the share repo)", len(got), got)
	}
}

func TestUpdateCheckAndThePublishedListingShareOneCallForTheShareRepo(t *testing.T) {
	p := newImp(t, impOpts{})
	p.clockIt(t)
	p.attach(att{name: "d", repo: shareRepo})
	p.gh.upstreamListing(shareRepo, map[string]string{"d": blobOf("d-old")})
	p.updates()
	before := len(p.gh.listings())
	p.rawMember("d") // GET crew reads the published names
	if got := len(p.gh.listings()); got != before {
		t.Errorf("GET crew listed the share repo again (%d calls, was %d)", got, before)
	}
}

// --- what each blob means ---

func TestUpdateCheckComparesTheBlobOnTheDefaultBranch(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	for _, n := range []string{"same", "moved", "gone"} {
		p.attach(att{name: n})
	}
	p.gh.upstreamListing("hubot/crew", map[string]string{"same": blobOf("same-old"), "moved": blobOf("moved-new"), "other": blobOf("other")})
	status, states := p.updates()
	wantStatus(t, "updates", status, 200, nil)
	want := map[string]string{"same": "none", "moved": "available", "gone": "removed"}
	if !reflect.DeepEqual(states, want) {
		t.Errorf("states = %v, want %v", states, want)
	}
	for _, c := range p.gh.calls() {
		if c.Endpoint != "repos/hubot/crew/contents/crew" {
			t.Errorf("unexpected call %s %s: the check is one listing and nothing else", c.Method, c.Endpoint)
		}
	}
}

func TestUpdateAChangedFileIsAvailableWhateverTheTagAndANewTagOnAnUnchangedFileIsNot(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	p.attach(att{name: "moved", tag: "v1"})
	p.attach(att{name: "same", tag: "v1"})
	p.gh.upstreamListing("hubot/crew", map[string]string{"moved": blobOf("moved-new"), "same": blobOf("same-old")})
	p.gh.ok("GET", "repos/hubot/crew/tags?per_page=100", []map[string]any{{"name": "v9", "commit": map[string]any{"sha": newSHA}}})
	_, states := p.updates()
	if states["moved"] != "available" || states["same"] != "none" {
		t.Errorf("states = %v, want moved available and same none", states)
	}
	for _, c := range p.gh.calls() {
		if c.Endpoint != "repos/hubot/crew/contents/crew" {
			t.Errorf("the check made %s %s; a tag never matters", c.Method, c.Endpoint)
		}
	}
}

func TestUpdateADetachedMemberAndAMemberWithNoSidecarAreNotChecked(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	p.attach(att{name: "edited"})
	writeFile(t, p.crewDir()+"/edited.md", memberFile("edited", "")+"\nEdited by hand.\n")
	p.member("plain", "Mine.")
	status, states := p.updates()
	wantStatus(t, "updates", status, 200, nil)
	if len(states) != 0 {
		t.Errorf("states = %v, want none: a detached member and a plain one are not checked", states)
	}
	p.wantCalls("a fleet with nothing attached", 0)
	for _, n := range []string{"edited", "plain"} {
		if got := p.updateOf(n); got != "none" {
			t.Errorf("%s update = %q, want none", n, got)
		}
	}
}

func TestUpdateAFleetWithNoAttachedMemberAnswersEmptyAndCallsNothingEvenWithAShareRepo(t *testing.T) {
	p := newImp(t, impOpts{})
	p.clockIt(t)
	p.member("plain", "Mine.")
	status, out := p.getJSON("/updates")
	wantStatus(t, "updates", status, 200, out)
	if m, ok := out["updates"].(map[string]any); !ok || len(m) != 0 {
		t.Errorf("updates = %v, want {}", out["updates"])
	}
	p.wantCalls("no attached member", 0)
}

// --- failures ---

func TestUpdateANotFoundRepoIsUnknownNotRemovedWhileThePublishedListingReadsItAsNothingPublished(t *testing.T) {
	p := newImp(t, impOpts{})
	p.clockIt(t)
	p.attach(att{name: "d", repo: shareRepo})
	p.gh.answer("GET", listingPath, 404, nil, "")
	status, states := p.updates()
	wantStatus(t, "updates", status, 200, nil)
	if states["d"] != "unknown" {
		t.Errorf("states = %v, want d unknown, not removed", states)
	}
	p.gh.healthyRepo()
	status, out := p.getJSON("/sharing")
	wantStatus(t, "sharing", status, 200, out)
	if out["published_error"] != nil && out["published_error"] != "" {
		t.Errorf("published_error = %v, want none: a missing crew/ folder is nothing published", out["published_error"])
	}
	if names, _ := out["published"].([]any); len(names) != 0 {
		t.Errorf("published = %v, want none", names)
	}
}

func TestUpdateARateLimitedListingIsUnknownAndTheNextCheckRetries(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	clk := p.clockIt(t)
	p.attach(att{name: "moved"})
	p.gh.rateLimited("hubot/crew")
	_, states := p.updates()
	if states["moved"] != "unknown" {
		t.Errorf("states = %v, want moved unknown", states)
	}
	if got := p.updateOf("moved"); got != "unknown" {
		t.Errorf("GET crew shows %q, want unknown", got)
	}

	p.gh.unseed()
	p.gh.upstreamListing("hubot/crew", map[string]string{"moved": blobOf("moved-new")})
	clk.advance(6 * time.Minute)
	_, states = p.updates()
	if states["moved"] != "available" {
		t.Errorf("after the retry states = %v, want moved available", states)
	}
}

// --- reuse ---

func TestUpdateAResultYoungerThanFiveMinutesIsReusedAndAnOlderOneIsNot(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	clk := p.clockIt(t)
	p.attach(att{name: "moved"})
	p.gh.upstreamListing("hubot/crew", map[string]string{"moved": blobOf("moved-new")})

	_, first := p.updates()
	clk.advance(4*time.Minute + 59*time.Second)
	p.gh.unseed()
	p.gh.upstreamListing("hubot/crew", map[string]string{"moved": blobOf("moved-old")})
	_, second := p.updates()
	if first["moved"] != "available" || second["moved"] != "available" {
		t.Errorf("inside five minutes: %v then %v, want the first result reused", first, second)
	}
	if got := len(p.gh.listings()); got != 1 {
		t.Errorf("%d listing calls inside five minutes, want 1", got)
	}

	clk.advance(2 * time.Second)
	_, third := p.updates()
	if third["moved"] != "none" || len(p.gh.listings()) != 2 {
		t.Errorf("after five minutes: %v with %d calls, want a fresh check saying none", third, len(p.gh.listings()))
	}
}

func TestUpdateGetCrewReadsTheLastResultWhateverItsAgeAndCallsNothing(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	clk := p.clockIt(t)
	p.attach(att{name: "moved"})
	p.attach(att{name: "gone"})
	p.gh.upstreamListing("hubot/crew", map[string]string{"moved": blobOf("moved-new")})
	p.updates()
	calls := len(p.gh.calls())
	clk.advance(72 * time.Hour)
	if got := p.updateOf("moved"); got != "available" {
		t.Errorf("moved = %q three days on, want the last result, available", got)
	}
	if got := p.updateOf("gone"); got != "removed" {
		t.Errorf("gone = %q, want removed", got)
	}
	if got := len(p.gh.calls()); got != calls {
		t.Errorf("GET crew made %d gh calls", got-calls)
	}
}

func TestUpdateAnAttachedMemberWithNoResultYetIsUnknownAndOthersAreNone(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	p.attach(att{name: "fresh"})
	p.attach(att{name: "edited"})
	writeFile(t, p.crewDir()+"/edited.md", memberFile("edited", "")+"\nEdited by hand.\n")
	p.member("plain", "Mine.")
	for name, want := range map[string]string{"fresh": "unknown", "edited": "none", "plain": "none"} {
		if got := p.updateOf(name); got != want {
			t.Errorf("%s update = %q, want %q", name, got, want)
		}
	}
	p.wantCalls("GET crew with no share repo", 0)
}

func TestUpdateGetCrewCallsNothingForABotEither(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	p.clockIt(t)
	p.attach(att{name: "moved"})
	p.gh.upstreamListing("hubot/crew", map[string]string{"moved": blobOf("moved-new")})
	p.updates()
	calls := len(p.gh.calls())
	status, body := p.asBot("GET", "")
	if status != 200 {
		t.Fatalf("a bot's GET crew: %d %s", status, body)
	}
	if got := parse(t, body); got == nil {
		t.Fatal("no JSON")
	}
	if got := len(p.gh.calls()); got != calls {
		t.Errorf("a bot's GET crew made %d gh calls", got-calls)
	}
}

func TestUpdateCheckWorksWithSharingOff(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{off: true}})
	p.clockIt(t)
	p.attach(att{name: "moved"})
	p.gh.upstreamListing("hubot/crew", map[string]string{"moved": blobOf("moved-new")})
	status, states := p.updates()
	wantStatus(t, "updates", status, 200, nil)
	if states["moved"] != "available" {
		t.Errorf("states = %v", states)
	}
}

func TestUpdateCheckWithNoGitHubSourceOrAnotherHostIs409AndCallsNothing(t *testing.T) {
	for name, o := range map[string]pubOpts{"no source": {noGitHub: true}, "other host": {host: "ghe.example.com"}} {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{pubOpts: o})
			p.clockIt(t)
			p.attach(att{name: "moved"})
			status, out := p.getJSON("/updates")
			wantStatus(t, "updates", status, 409, out)
			wantText(t, "updates", out, connectText)
			p.wantCalls("a fleet with no github.com client", 0)
		})
	}
}
