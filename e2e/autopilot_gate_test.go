package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
)

const refusalTitle = "Couldn't launch unattended on Claude: guard not proven"

// gateNotices are the Inbox notices about a refused unattended launch.
func gateNotices(t *testing.T, in *fleetInst) []noticeItem {
	t.Helper()
	var out []noticeItem
	for _, n := range in.notices(t) {
		if strings.Contains(n.Title, "guard not proven") {
			out = append(out, n)
		}
	}
	return out
}

func (w *world) expectRefused(fleet string) {
	w.t.Helper()
	req := w.autopilotLaunch("claude", "gate-"+fleet, nil)
	req["fleet"] = fleet
	code, body := w.post(req)
	if code != http.StatusBadRequest {
		w.t.Fatalf("autopilot launch in %s = %d, want 400: %s", fleet, code, body)
	}
	if low := strings.ToLower(body); !strings.Contains(low, "claude") || !strings.Contains(low, "guard") {
		w.t.Errorf("the 400 body %q must name the harness and the missing guard", body)
	}
	for _, s := range w.in.ships(w.t) {
		if s.Name == "gate-"+fleet {
			w.t.Errorf("the refused launch created ship %q", s.Name)
		}
	}
}

func TestAutopilotGate_ProvenGuardLaunchesAndOthersAreRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard("work")
	w.seedGuard("home", "claude", false)
	w.writeCue("wait go", "exit 0")
	w.start()

	w.launch(w.autopilotLaunch("claude", "ok", nil))
	w.expectRefused("home")
	w.expectRefused("claudeonly") // never probed
	if n := gateNotices(t, w.in); len(n) != 2 {
		t.Errorf("two fleets refused left %d notices, want 2: %+v", len(n), n)
	}
}

func TestAutopilotGate_RefusalWritesOneNoticeThatComesBackAfterADismissal(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	store := capabilities.Open(w.home)
	if err := store.RecordVersion("home", "claude", fakeVersion, time.Now()); err != nil {
		t.Fatal(err)
	}
	w.writeCue("wait go", "exit 0")
	w.start()

	w.expectRefused("home")
	n := gateNotices(t, w.in)
	if len(n) != 1 {
		t.Fatalf("one refusal left %d notices, want 1", len(n))
	}
	if n[0].Title != refusalTitle || !strings.Contains(n[0].Notice.Body, "Home") {
		t.Errorf("notice = %q / %q, want the title %q and a body naming the fleet Home", n[0].Title, n[0].Notice.Body, refusalTitle)
	}

	w.expectRefused("home")
	if got := len(gateNotices(t, w.in)); got != 1 {
		t.Errorf("a second refusal left %d notices, want 1", got)
	}

	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/inbox/"+n[0].Session+"/"+n[0].ID+"/remove", nil)
	if code/100 != 2 {
		t.Fatalf("dismissing the notice = %d: %s", code, body)
	}
	w.expectRefused("home")
	if got := len(gateNotices(t, w.in)); got != 1 {
		t.Errorf("a refusal after the dismissal left %d notices, want 1", got)
	}
}

func TestAutopilotGate_AHandLaunchNeedsNoProof(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.start()
	w.launch(w.dockLaunch("claude", "hand", "go"))
	if n := gateNotices(t, w.in); len(n) != 0 {
		t.Errorf("a hand launch wrote %d refusal notices", len(n))
	}
}

func TestAutopilotGate_ACrewLaunchFollowsTheFleetsGuard(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard("work")
	w.writeCue("hook working")
	w.start()
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	w.waitFile(ship.Session, "cues-done")
	dir := w.checkout("anywhere")
	crew := func(name string) cliResult {
		return w.admiralWith(cliOpts{env: []string{"ADMIRAL_SHIP=" + ship.Session, "ADMIRAL_AUTOPILOT=1"}},
			"crew", "launch", "--dir", dir, "--name", name, "--", "hi")
	}
	w.expectExit(crew("mate"), 0, "crew launch while the guard is proven")

	// The guard is lost (the harness moved to an unprobed version): the next crew launch is refused.
	if err := capabilities.Open(w.home).RecordVersion("work", "claude", "9.9.9", time.Now()); err != nil {
		t.Fatal(err)
	}
	r := crew("mate-two")
	w.expectExit(r, 1, "crew launch after the guard stopped being proven")
	if low := strings.ToLower(r.stderr); !strings.Contains(low, "claude") || !strings.Contains(low, "guard") {
		t.Errorf("stderr = %q, want it to name the harness and the guard", r.stderr)
	}
	for _, p := range w.crewPanes(ship.Session) {
		if p.crew == "mate-two" {
			t.Errorf("the refused crew launch left a window %q", p.crew)
		}
	}
	if n := gateNotices(t, w.in); len(n) != 1 {
		t.Errorf("the refused crew launch left %d notices, want 1", len(n))
	}
}
