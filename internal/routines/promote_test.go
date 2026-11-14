// Promote over the service with fake stop and launch seams, a temp home and a real restricted clone.
package routines_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

const (
	restrictedShip = "calm-otter"
	promotedShip   = "swift-heron"
	eventCanary    = "canary-event-text-9f3a"
	reportCanary   = "canary-report-text-9f3a"
	commitCanary   = "canary-commit-message-9f3a"
)

type seams struct {
	mu       sync.Mutex
	log      []string
	requests []launcher.Request
	refuse   *launcher.Error
}

func (s *seams) stop(session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, "stop "+session)
	return nil
}

func (s *seams) launch(_ context.Context, req launcher.Request) (routines.ShipRef, *launcher.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, "launch")
	s.requests = append(s.requests, req)
	if s.refuse != nil {
		return routines.ShipRef{}, s.refuse
	}
	return routines.ShipRef{Session: promotedShip, Status: "underway"}, nil
}

func (s *seams) launches() []launcher.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]launcher.Request(nil), s.requests...)
}

func (s *seams) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

func promoGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func promoWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type promoRig struct {
	*api
	seams   *seams
	dir     string
	routine routines.Routine
	other   routines.Routine
	base    string
}

// newPromoRig has a routine whose Dir is a real repo with two commits, and a service with both seams; withLaunch false leaves the Launch seam nil.
func newPromoRig(t *testing.T, withLaunch bool) *promoRig {
	t.Helper()
	p := &promoRig{api: &api{t: t, rig: newRig(t)}, seams: &seams{}, dir: t.TempDir()}
	promoGit(t, p.dir, "init", "-q", "-b", "main")
	promoWrite(t, filepath.Join(p.dir, "a.txt"), "a\n")
	promoGit(t, p.dir, "add", "a.txt")
	promoGit(t, p.dir, "commit", "-q", "-m", "a")
	promoWrite(t, filepath.Join(p.dir, "b.txt"), "b\n")
	promoGit(t, p.dir, "add", "b.txt")
	promoGit(t, p.dir, "commit", "-q", "-m", "b")
	p.base = promoGit(t, p.dir, "rev-parse", "HEAD")
	svc := &routines.Service{Store: p.rig.store, Stop: p.seams.stop}
	if withLaunch {
		svc.Launch = p.seams.launch
	}
	mux := http.NewServeMux()
	svc.Routes(mux)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	mine := launchRoutine()
	mine.Do.Launch.Dir = p.dir
	p.routine = p.rig.create("work", mine)
	second := launchRoutine()
	second.Name = "Other sweep"
	second.Do.Launch.Dir = p.dir
	p.other = p.rig.create("work", second)
	return p
}

// clone is the restricted ship's real clone under the home, made the way the launcher makes it.
func (p *promoRig) clone(ship string) string {
	p.t.Helper()
	dir, err := launcher.GitCheckouts{}.Make(p.rig.home, ship, p.dir)
	if err != nil {
		p.t.Fatal(err)
	}
	return dir
}

// commitIn commits a file in the restricted clone and returns the new tip.
func (p *promoRig) commitIn(clone, file, content, message string) string {
	p.t.Helper()
	promoWrite(p.t, filepath.Join(clone, file), content)
	promoGit(p.t, clone, "add", "-f", "--", file)
	promoGit(p.t, clone, "commit", "-q", "-m", message)
	return promoGit(p.t, clone, "rev-parse", "HEAD")
}

func (p *promoRig) record(t routines.Trigger) routines.Trigger {
	p.t.Helper()
	got, err := p.rig.store.AppendTrigger("work", t)
	if err != nil {
		p.t.Fatal(err)
	}
	return got
}

// draftFix is a launched restricted Draft fix record whose event text is a canary.
func (p *promoRig) draftFix(ship string) routines.Trigger {
	return p.record(routines.Trigger{Routine: p.routine.ID, Outcome: routines.Launched, Ship: ship, WorkerLaunched: true,
		Restricted: standing.DraftFix, Tier: "T1", Event: eventCanary, Action: "A3"})
}

func (p *promoRig) promote(routine, id string) (int, map[string]any) {
	p.t.Helper()
	return p.do("POST", "/api/fleets/work/routines/"+routine+"/runs/"+id+"/promote", "")
}

func (p *promoRig) get(id string) map[string]any {
	p.t.Helper()
	code, body := p.do("GET", "/api/fleets/work/routines/"+p.routine.ID+"/runs/"+id, "")
	if code != http.StatusOK {
		p.t.Fatalf("GET record = %d %v", code, body)
	}
	return body
}

// ready is a Draft fix record with a clone holding one local commit, the usual Promote.
func (p *promoRig) ready() (rec routines.Trigger, clone, tip string) {
	p.t.Helper()
	rec = p.draftFix(restrictedShip)
	clone = p.clone(restrictedShip)
	promoWrite(p.t, filepath.Join(clone, ".admiral", "report.md"), reportCanary+"\n")
	promoWrite(p.t, filepath.Join(clone, ".admiral", "untrusted", "event.md"), eventCanary+"\n")
	tip = p.commitIn(clone, "fix.txt", "the fix\n", commitCanary)
	return rec, clone, tip
}

func (p *promoRig) mustPromote(id string) map[string]any {
	p.t.Helper()
	code, body := p.promote(p.routine.ID, id)
	if code != http.StatusOK {
		p.t.Fatalf("promote = %d %v, want 200", code, body)
	}
	return body
}

func (p *promoRig) wantRefused(code int, body map[string]any, status int, reason string) {
	p.t.Helper()
	wantError(p.t, code, body, status)
	if msg, _ := body["error"].(string); !strings.Contains(msg, reason) {
		p.t.Errorf("error = %q, want it to say %q", msg, reason)
	}
	if got := p.seams.launches(); len(got) != 0 {
		p.t.Errorf("a refused promote launched %d ships", len(got))
	}
}

func samePathAs(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func TestPromoteStopsTheRestrictedShipThenLaunchesANormalAutopilotShipFromItsWork(t *testing.T) {
	p := newPromoRig(t, true)
	rec, clone, tip := p.ready()
	body := p.mustPromote(rec.ID)
	if body["promoted_ship"] != promotedShip {
		t.Fatalf("body = %v, want promoted_ship %s", body, promotedShip)
	}
	if got := p.seams.calls(); len(got) != 2 || got[0] != "stop "+restrictedShip || got[1] != "launch" {
		t.Fatalf("calls = %v, want the stop of %s, then one launch", got, restrictedShip)
	}
	req := p.seams.launches()[0]
	if !req.Autopilot || req.Unattended || req.By != "promote" || req.WorkerLaunched || req.RoutineKey != "" || req.Restricted != "" {
		t.Errorf("request = %+v, want autopilot, attended, By promote, not worker-launched, no routine key, not restricted", req)
	}
	if req.Fleet != "work" || req.Harness != "claude" || req.Dir != p.dir {
		t.Errorf("request = %+v, want the routine's fleet, harness and dir %s", req, p.dir)
	}
	if req.StartFrom == nil || !samePathAs(t, req.StartFrom.Clone, clone) || req.StartFrom.Branch != "admiral/"+restrictedShip {
		t.Errorf("StartFrom = %+v, want clone %s on admiral/%s", req.StartFrom, clone, restrictedShip)
	}
	if req.EventText != "" || req.Template != "" {
		t.Errorf("request carries event text %q or template %q", req.EventText, req.Template)
	}
	lower := strings.ToLower(req.Prompt)
	for _, want := range []string{strings.ToLower(rec.ID), "admiral/" + restrictedShip, p.base[:7], tip[:7], "review"} {
		if !strings.Contains(lower, want) {
			t.Errorf("order lacks %q:\n%s", want, req.Prompt)
		}
	}
	for _, canary := range []string{eventCanary, reportCanary, commitCanary, "fix.txt"} {
		if strings.Contains(req.Prompt, canary) {
			t.Errorf("order carries %q from the untrusted work:\n%s", canary, req.Prompt)
		}
	}
}

func TestAPromotedRecordShowsThePromotedShipAndKeepsItsOwnShip(t *testing.T) {
	p := newPromoRig(t, true)
	rec, _, _ := p.ready()
	before, err := p.rig.store.Records("work")
	if err != nil {
		t.Fatal(err)
	}
	p.mustPromote(rec.ID)
	got := p.get(rec.ID)
	if got["promoted"] != true || got["promoted_ship"] != promotedShip || got["ship"] != restrictedShip {
		t.Errorf("record = %v, want promoted, promoted_ship %s and ship still %s", got, promotedShip, restrictedShip)
	}
	after, err := p.rig.store.Records("work")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("records = %d, want %d: the mark is not a record of its own", len(after), len(before))
	}
}

func TestAPromotedRecordSurvivesAReopenOfTheStore(t *testing.T) {
	p := newPromoRig(t, true)
	rec, _, _ := p.ready()
	p.mustPromote(rec.ID)
	fresh := &routines.Store{Home: p.rig.home, Fleets: p.rig.store.Fleets, Now: p.rig.store.Now}
	recs, err := fresh.Records("work")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.ID == rec.ID {
			if !r.Promoted || r.PromotedShip != promotedShip || r.Ship != restrictedShip {
				t.Errorf("record = %+v, want promoted to %s with ship %s", r, promotedShip, restrictedShip)
			}
			return
		}
	}
	t.Fatalf("record %s is not in %+v", rec.ID, recs)
}

func TestASecondPromoteAnswersTheFirstShipAndLaunchesAndStopsNothing(t *testing.T) {
	p := newPromoRig(t, true)
	rec, _, _ := p.ready()
	p.mustPromote(rec.ID)
	body := p.mustPromote(rec.ID)
	if body["promoted_ship"] != promotedShip {
		t.Errorf("second promote = %v, want the first ship %s", body, promotedShip)
	}
	if got := p.seams.calls(); len(got) != 2 {
		t.Errorf("calls = %v, want only the first promote's stop and launch", got)
	}
}

func TestPromoteRefusesWhatIsNotARestrictedDraftFixLaunchOfThisRoutine(t *testing.T) {
	cases := map[string]struct {
		rec    func(p *promoRig) routines.Trigger
		status int
		reason string
	}{
		"an Investigate ship": {func(p *promoRig) routines.Trigger {
			r := p.draftFix(restrictedShip)
			return p.record(routines.Trigger{Routine: p.routine.ID, Outcome: routines.Launched, Ship: r.Ship, WorkerLaunched: true, Restricted: standing.Investigate, Tier: "T1", Action: "A3"})
		}, http.StatusBadRequest, "Draft fix"},
		"an A4 normal event ship": {func(p *promoRig) routines.Trigger {
			return p.record(routines.Trigger{Routine: p.routine.ID, Outcome: routines.Launched, Ship: restrictedShip, WorkerLaunched: true, Tier: "T1", Action: "A4"})
		}, http.StatusBadRequest, "Draft fix"},
		"a hand launch": {func(p *promoRig) routines.Trigger {
			return p.record(routines.Trigger{Routine: p.routine.ID, Outcome: routines.Launched, Ship: restrictedShip, Restricted: standing.DraftFix, Tier: "T1"})
		}, http.StatusBadRequest, "launch"},
		"a proposal": {func(p *promoRig) routines.Trigger {
			return p.record(routines.Trigger{Routine: p.routine.ID, Outcome: routines.Proposed, Tier: "T1"})
		}, http.StatusBadRequest, "launch"},
		"another routine's record": {func(p *promoRig) routines.Trigger {
			return p.record(routines.Trigger{Routine: p.other.ID, Outcome: routines.Launched, Ship: restrictedShip, WorkerLaunched: true, Restricted: standing.DraftFix, Tier: "T1"})
		}, http.StatusBadRequest, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPromoRig(t, true)
			clone := p.clone(restrictedShip)
			p.commitIn(clone, "fix.txt", "the fix\n", "fix")
			rec := c.rec(p)
			code, body := p.promote(p.routine.ID, rec.ID)
			p.wantRefused(code, body, c.status, c.reason)
			if got := p.seams.calls(); len(got) != 0 {
				t.Errorf("calls = %v, want none: a refusal stops nothing", got)
			}
		})
	}
}

func TestPromoteAnswers404ForAnUnknownRecordOrRoutine(t *testing.T) {
	p := newPromoRig(t, true)
	code, body := p.promote(p.routine.ID, "no-such-record")
	wantError(t, code, body, http.StatusNotFound)
	code, body = p.promote("no-such-routine", "no-such-record")
	wantError(t, code, body, http.StatusNotFound)
	if got := p.seams.calls(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
}

func TestPromoteRefusesAnUndoneRecordAndOneWhoseCloneIsGone(t *testing.T) {
	p := newPromoRig(t, true)
	rec, clone, _ := p.ready()
	if code, body := p.do("POST", "/api/fleets/work/routines/"+p.routine.ID+"/runs/"+rec.ID+"/undo", ""); code != http.StatusOK {
		t.Fatalf("undo = %d %v", code, body)
	}
	p.seams.mu.Lock()
	p.seams.log = nil
	p.seams.mu.Unlock()
	code, body := p.promote(p.routine.ID, rec.ID)
	p.wantRefused(code, body, http.StatusBadRequest, "undone")

	gone := p.draftFix("brisk-heron")
	code, body = p.promote(p.routine.ID, gone.ID)
	p.wantRefused(code, body, http.StatusBadRequest, "clone")
	if exists(clone) {
		t.Errorf("%s is back", clone)
	}
}

func TestPromoteRefusesNothingToPromoteAndCommitsThatTouchAdmiralBeforeStoppingTheShip(t *testing.T) {
	p := newPromoRig(t, true)
	rec := p.draftFix(restrictedShip)
	clone := p.clone(restrictedShip)
	code, body := p.promote(p.routine.ID, rec.ID)
	p.wantRefused(code, body, http.StatusBadRequest, "nothing to promote")
	if got := p.seams.calls(); len(got) != 0 {
		t.Fatalf("calls = %v, want none: the work is read before the ship is stopped", got)
	}

	p.commitIn(clone, ".admiral/untrusted/event.md", "smuggled\n", "add")
	promoGit(t, clone, "rm", "-q", "-r", "--", ".admiral")
	promoGit(t, clone, "commit", "-q", "-m", "tidy")
	code, body = p.promote(p.routine.ID, rec.ID)
	p.wantRefused(code, body, http.StatusBadRequest, "commits touch .admiral/")
	if got := p.seams.calls(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
}

func TestPromoteRefusesAClonesChangedAlternatesBeforeStoppingTheShip(t *testing.T) {
	p := newPromoRig(t, true)
	rec, clone, _ := p.ready()
	alt := filepath.Join(clone, ".git", "objects", "info", "alternates")
	f, err := os.OpenFile(alt, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(t.TempDir() + "/objects\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	code, body := p.promote(p.routine.ID, rec.ID)
	p.wantRefused(code, body, http.StatusBadRequest, "alternates")
	if got := p.seams.calls(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
}

func TestALaunchRefusalAnswersItsOwnStatusWithItsReasonAndRecordsNothing(t *testing.T) {
	p := newPromoRig(t, true)
	rec, _, _ := p.ready()
	p.seams.refuse = &launcher.Error{Status: http.StatusTooManyRequests, Msg: "the global cap of 4 ships is reached"}
	code, body := p.promote(p.routine.ID, rec.ID)
	wantError(t, code, body, http.StatusTooManyRequests)
	if msg, _ := body["error"].(string); !strings.Contains(msg, "global cap") {
		t.Errorf("error = %q, want the launcher's reason", msg)
	}
	if got := p.get(rec.ID); got["promoted"] == true || got["promoted_ship"] != nil {
		t.Errorf("record = %v, want no promotion recorded", got)
	}
	p.seams.refuse = nil
	if body := p.mustPromote(rec.ID); body["promoted_ship"] != promotedShip {
		t.Errorf("retry = %v, want it to launch", body)
	}
}

func TestALauncherSetupFailureAnswersItsOwn500NotA409(t *testing.T) {
	p := newPromoRig(t, true)
	rec, _, _ := p.ready()
	p.seams.refuse = &launcher.Error{Status: http.StatusInternalServerError, Msg: "could not start the ship from the restricted work"}
	code, body := p.promote(p.routine.ID, rec.ID)
	wantError(t, code, body, http.StatusInternalServerError)
}

func TestPromoteRefusesHarnessConfigAndSymlinksBeforeStoppingTheShip(t *testing.T) {
	p := newPromoRig(t, true)
	rec := p.draftFix(restrictedShip)
	clone := p.clone(restrictedShip)
	p.commitIn(clone, ".claude/settings.json", "{}\n", "settings")
	code, body := p.promote(p.routine.ID, rec.ID)
	p.wantRefused(code, body, http.StatusBadRequest, "touches harness config")
	if msg, _ := body["error"].(string); strings.Contains(msg, "settings.json") {
		t.Errorf("error = %q, want the rule and no file names", msg)
	}
	promoGit(t, clone, "reset", "-q", "--hard", "HEAD~1")
	if err := os.Symlink("/etc/hosts", filepath.Join(clone, "link")); err != nil {
		t.Fatal(err)
	}
	code, body = p.promote(p.routine.ID, rec.ID)
	p.wantRefused(code, body, http.StatusBadRequest, "adds a symlink")
	if got := p.seams.calls(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
}

func TestPromoteWithNoLaunchSeamIsA500AndRecordsNothing(t *testing.T) {
	p := newPromoRig(t, false)
	rec, _, _ := p.ready()
	code, body := p.promote(p.routine.ID, rec.ID)
	wantError(t, code, body, http.StatusInternalServerError)
	if got := p.get(rec.ID); got["promoted"] == true {
		t.Errorf("record = %v, want no promotion recorded", got)
	}
}

func TestUndoOfAPromotedRecordIsRefusedAndLeavesTheCloneAndTheRecord(t *testing.T) {
	p := newPromoRig(t, true)
	rec, clone, _ := p.ready()
	p.mustPromote(rec.ID)
	code, body := p.do("POST", "/api/fleets/work/routines/"+p.routine.ID+"/runs/"+rec.ID+"/undo", "")
	wantError(t, code, body, http.StatusBadRequest)
	if msg, _ := body["error"].(string); !strings.Contains(msg, "promoted") {
		t.Errorf("error = %q, want it to say promoted", msg)
	}
	if !exists(clone) {
		t.Error("the refused undo removed the clone")
	}
	if got := p.get(rec.ID); got["undone"] == true {
		t.Errorf("record = %v, want it not undone", got)
	}
}
