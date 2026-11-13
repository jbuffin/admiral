package ships

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

func (r *nextRig) returnToPort(session string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/"+session+"/return", nil))
	return rec
}

func (r *nextRig) afloat() bool {
	_, ok := r.s.find("saltpaw")
	return ok
}

func TestReturnToPortWaitsForTheTurnThenStopsAfterTheCleanUpTurn(t *testing.T) {
	r := newNextRig(t)
	r.turn(t, "working", "")

	if rec := r.returnToPort("saltpaw"); rec.Code != http.StatusOK {
		t.Fatalf("return = %d %s, want 200", rec.Code, rec.Body)
	}
	r.s.advanceReturns()
	if strings.Contains(r.pane(t), "Return to port") {
		t.Fatal("the clean-up prompt reached a ship mid-turn")
	}
	if sh, _ := r.s.find("saltpaw"); !r.s.ship(sh).Returning {
		t.Error("the ship does not show it is returning to port")
	}

	r.turn(t, "needs_you", "stop")
	r.s.advanceReturns()
	waitUntil(t, "the clean-up prompt at the ship's prompt", func() bool { return strings.Contains(r.pane(t), "Return to port") })
	r.s.settleWG.Wait()
	if !r.afloat() {
		t.Fatal("the ship was stopped before its clean-up turn ran")
	}

	r.turn(t, "needs_you", "stop")
	r.s.advanceReturns()
	waitUntil(t, "the ship to be stopped", func() bool { return !r.afloat() })
	if got := outcomeLines(t, r.home, "saltpaw", "stopped"); len(got) != 1 || got[0]["returned"] != true {
		t.Errorf("stopped records = %v, want one marked returned", got)
	}
}

func TestReturnToPortWaitsOnTheEngineerDuringTheCleanUpTurn(t *testing.T) {
	r := newNextRig(t)
	r.turn(t, "needs_you", "stop")
	if rec := r.returnToPort("saltpaw"); rec.Code != http.StatusOK {
		t.Fatalf("return = %d %s, want 200", rec.Code, rec.Body)
	}
	waitUntil(t, "the clean-up prompt at the ship's prompt", func() bool { return strings.Contains(r.pane(t), "Return to port") })
	r.s.settleWG.Wait()

	r.turn(t, "needs_you", "permission:Bash")
	r.s.advanceReturns()
	r.s.settleWG.Wait()
	if !r.afloat() {
		t.Fatal("the ship was stopped while its clean-up turn waited on an approval")
	}
}

func TestReturnToPortRefusesAShipThatIsNotThere(t *testing.T) {
	r := newNextRig(t)
	wantError(t, r.returnToPort("nosuchship"), http.StatusNotFound, "an unknown ship")
}

func TestAMooredShipWhosePRsAreMergedIsWokenToCleanUpAndStoppedAfterThatTurn(t *testing.T) {
	r := newWakeRig(t, 0)
	g := &pollGitHub{}
	g.set(wakePR, moor.Status{State: "MERGED"})
	r.s.GitHub = func(string) moor.GitHub { return g }
	r.moored(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	n := &nextRig{wakeRig: r, mux: mux}

	r.s.PollMoored(context.Background())

	argv, _ := r.started(t, 1)
	if got := prompt(argv); got != "/commodore resume run-7 "+ReturnPrompt {
		t.Errorf("prompt = %q, want the resume text, then the clean-up prompt", got)
	}
	if o := r.options(t, "saltpaw"); o["round"] != "2" || o["returning"] != "sent" {
		t.Errorf("options = %v, want round 2 woken to return to port", o)
	}
	if rec := n.returnToPort("saltpaw"); rec.Code != http.StatusOK {
		t.Errorf("return on a ship already returning = %d %s, want 200", rec.Code, rec.Body)
	}

	n.turn(t, "needs_you", "stop")
	r.s.advanceReturns()
	waitUntil(t, "the ship to be stopped", func() bool { return !n.afloat() })
	r.s.settleWG.Wait()
	if got := r.logged(t, "unmoored"); len(got) != 1 || got[0]["reason"] != "merged" {
		t.Errorf("unmoored events = %v, want one for the merged PR", got)
	}
	if got := r.logged(t, "round"); len(got) != 1 || got[0]["end"] != "sailed" {
		t.Errorf("round events = %v, want the round ended sailed", got)
	}
	if _, err := os.Stat(filepath.Join(r.home, "moored", "saltpaw.json")); err == nil {
		t.Error("the ship was moored again")
	}
}

func TestReturnToPortRefusesALiveShipThatCarriesAPR(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0)
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}
	r.started(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	n := &nextRig{wakeRig: r, mux: mux}
	wantError(t, n.returnToPort("saltpaw"), http.StatusConflict, "a woken ship with an open PR")
	if sh, _ := r.s.find("saltpaw"); !r.s.ship(sh).CarriesPRs {
		t.Error("the woken ship does not say it carries a PR")
	}
}
