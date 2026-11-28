package crewapi_test

import (
	"os"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/crewapi"
)

func TestUpdateAFleetWithNoAttachedMemberAndNoGitHubClientAnswersEmptyAndCallsNothing(t *testing.T) {
	for name, o := range map[string]pubOpts{"no client": {noGitHub: true}, "other host": {host: "ghe.example.com"}} {
		t.Run(name, func(t *testing.T) {
			p := newImp(t, impOpts{pubOpts: o})
			p.clockIt(t)
			p.member("plain", "Mine.")
			status, out := p.getJSON("/updates")
			wantStatus(t, "updates", status, 200, out)
			if m, ok := out["updates"].(map[string]any); !ok || len(m) != 0 {
				t.Errorf("updates = %v, want {}", out["updates"])
			}
			p.wantCalls("no attached member", 0)
		})
	}
}

func TestUpdateAResultThatHoldsUnknownIsNotReusedInsideFiveMinutes(t *testing.T) {
	p := newImp(t, impOpts{pubOpts: pubOpts{noRepo: true}})
	clk := p.clockIt(t)
	p.attach(att{name: "moved"})
	p.gh.rateLimited("hubot/crew")
	if _, states := p.updates(); states["moved"] != "unknown" {
		t.Fatalf("states = %v, want moved unknown", states)
	}

	p.gh.unseed()
	p.gh.upstreamListing("hubot/crew", map[string]string{"moved": blobOf("moved-new")})
	clk.advance(time.Minute)
	if _, states := p.updates(); states["moved"] != "available" {
		t.Errorf("a minute after a failed listing states = %v, want a fresh check saying available", states)
	}
}

func TestUpdateAStateSetAfterACheckBeganSurvivesTheCheck(t *testing.T) {
	t.Parallel()
	got := crewapi.StoreKeepsASetStateOverAnOlderCheck()
	if got["a"] != "none" || got["b"] != "available" {
		t.Errorf("states = %v, want a none (set after the check began) and b available", got)
	}
	got = crewapi.StoreTakesALaterCheckOverAnEarlierSet()
	if got["a"] != "available" {
		t.Errorf("states = %v, want a check that began later to win", got)
	}
}

func TestUpdateApplyIsAChangedConflictWhenTheSidecarWasRetargetedAfterTheFetch(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	pv := u.previewOK("scout")
	release := crewapi.HoldWrites(t)
	type reply struct {
		status int
		out    map[string]any
	}
	done := make(chan reply, 1)
	go func() {
		status, out := u.apply("scout", pv, nil)
		done <- reply{status, out}
	}()
	eventually(t, "Apply to fetch the file and its tag", func() bool {
		return len(u.gh.find("GET", "repos/hubot/crew/tags?per_page=100")) > 1
	})
	u.attach(att{name: "scout", repo: "evil/crew", text: oldScout, tag: "v1"})
	release()
	r := <-done
	wantStatus(t, "apply", r.status, 409, r.out)
	if r.out["kind"] != "changed" {
		t.Errorf("kind = %v, want changed", r.out["kind"])
	}
	wantText(t, "apply", r.out, changedText)
	if u.memberBytes("scout") != oldScout {
		t.Error("Apply wrote bytes fetched from one repo under a sidecar that names another")
	}
	if got := u.sidecarOf("scout")["repo"]; got != "evil/crew" {
		t.Errorf("sidecar repo = %v, want it left as the engineer's tool set it", got)
	}
}

func TestUpdateApplyThroughASymlinkedCrewDirectoryIs409AndWritesNothing(t *testing.T) {
	u := newUpdatable(t, impOpts{})
	pv := u.previewOK("scout")
	behind := u.crewDir() + "-real"
	if err := os.Rename(u.crewDir(), behind); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(behind, u.crewDir()); err != nil {
		t.Fatal(err)
	}
	status, out := u.apply("scout", pv, nil)
	wantStatus(t, "apply", status, 409, out)
	wantText(t, "apply", out, "nothing was changed")
	if got := readFile(t, behind+"/scout.md"); got != oldScout {
		t.Errorf("member behind the link = %q, want it untouched", got)
	}
}
