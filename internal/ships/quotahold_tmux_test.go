package ships

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/standing"
	"github.com/jbuffin/admiral/internal/usage"
)

// quotaClock is the quota store's clock and the reset the held window comes back at.
type quotaClock struct {
	now   time.Time
	reset time.Time
}

// overTheLine gives work the Work preset's line of 90 and its claude login a primary window at used percent.
func (r *wakeRig) overTheLine(t *testing.T, used float64) *quotaClock {
	t.Helper()
	c := &quotaClock{now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
	c.reset = c.now.Add(2 * time.Hour)
	r.s.Launcher.Config().Fleets[0].Limits = standing.PresetLimits(standing.Work)
	r.s.Quota = usage.NewStore(func() time.Time { return c.now })
	r.s.Quota.SetQuota(r.s.dockConfig("work", "claude"), usage.Quota{
		Primary: &usage.Window{UsedPercent: used, WindowMinutes: 300, ResetsAt: c.reset.Unix()},
	}, c.now)
	return c
}

func TestAnUnattendedLaunchOverTheQuotaLineIsRefusedWithAWaitThatNamesTheHold(t *testing.T) {
	r := newWakeRig(t, 3)
	r.overTheLine(t, 93)
	_, lerr := r.s.Launch(context.Background(), r.keyReq(func(q *launcher.Request) { q.Autopilot, q.Unattended = true, true }))
	if lerr == nil || lerr.Wait == "" || !strings.Contains(strings.ToLower(lerr.Wait), "quota") || !strings.Contains(lerr.Wait, "93%") {
		t.Fatalf("Launch = %+v, want a refusal that waits and says it was held for quota at 93%%", lerr)
	}
	if n := len(r.s.List()); n != 0 {
		t.Errorf("%d ships after the held launch, want none", n)
	}
}

func TestAFeedbackRoundOverTheQuotaLineShowsTheHoldAsTheWaitingTextAndWakesOnceTheWindowResets(t *testing.T) {
	r := newWakeRig(t, 3)
	g := r.polling()
	g.set(wakePR, aliceReview())
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)
	clock := r.overTheLine(t, 93)

	r.s.PollMoored(context.Background())

	if got := r.waiting(); !strings.Contains(strings.ToLower(got), "quota") {
		t.Errorf("waiting = %q, want the quota hold named", got)
	}
	if !r.stillMoored(t) {
		t.Fatal("the ship woke over the quota line")
	}
	if st := r.state(t); len(st.PRs) != 0 {
		t.Errorf("wake state = %+v while held, want it unchanged", st)
	}

	clock.now = clock.reset
	r.s.PollMoored(context.Background())

	if r.stillMoored(t) {
		t.Fatal("the ship is still moored after the window reset")
	}
	r.started(t, 1)
}

func TestAProposalRoundOverTheQuotaLineLaunchesBecauseTheEngineerAcceptedIt(t *testing.T) {
	r := newWakeRig(t, 3)
	g := r.polling()
	g.set(wakePR, openWith(nil, strangerComment("C1", "stranger-1", "please look again", 30*time.Minute)))
	r.mooredResume(t, 0, "/commodore resume run-7", time.Hour)
	r.s.PollMoored(context.Background())
	p := only(t, proposals(t, r.home))
	r.overTheLine(t, 93)

	if _, lerr := r.s.LaunchProposal(context.Background(), p.Proposal); lerr != nil {
		t.Fatalf("LaunchProposal over the quota line = %v, want it started", lerr)
	}
	r.started(t, 1)
}

func TestAHandWakeOverTheQuotaLineLaunches(t *testing.T) {
	r := newWakeRig(t, 3)
	r.moored(t, 0)
	r.overTheLine(t, 100)
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatalf("Wake over the quota line = %v, want it started", lerr)
	}
	r.started(t, 1)
}

func TestAHandLaunchWithAutopilotTickedOverTheQuotaLineLaunches(t *testing.T) {
	r := newWakeRig(t, 3)
	r.overTheLine(t, 100)
	r.mustLaunch(t, func(q *launcher.Request) { q.Autopilot = true })
}

func TestAnAdmiralBotLaunchOnAnInstructionWithAutopilotOverTheQuotaLineLaunches(t *testing.T) {
	r := newWakeRig(t, 3)
	r.overTheLine(t, 100)
	r.mustLaunch(t, func(q *launcher.Request) { q.Autopilot, q.Via = true, "admiralbot" })
}

func TestAClientCannotMarkALaunchUnattendedThroughTheLaunchRoute(t *testing.T) {
	r := newWakeRig(t, 3)
	r.overTheLine(t, 100)
	body := `{"fleet":"work","harness":"claude","dir":` + `"` + r.checkout + `"` + `,"model":"opus","prompt":"go","skill":{"kind":"none"},"autopilot":true,"unattended":true,"Unattended":true}`
	if rec := launchPost(t, r.s, body); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/ships = %d %s, want 201: nothing a client sends marks a launch unattended", rec.Code, rec.Body)
	}
}
