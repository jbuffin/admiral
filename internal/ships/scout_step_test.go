package ships

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

// scoutRig is a live ship, saltpaw, in fleet work launched as a scout or as a plain autopilot ship, with the run loop going.
func scoutRig(t *testing.T, scout bool) *liveRig {
	t.Helper()
	r := newWakeRig(t, 0)
	req := launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Name: "Saltpaw", Session: "saltpaw",
		Prompt: "go", Skill: launcher.Skill{Kind: "none"}, Autopilot: true, Scout: scout}
	if _, lerr := r.s.LaunchOrder(req); lerr != nil {
		t.Fatalf("LaunchOrder = %v", lerr)
	}
	r.started(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	l := &liveRig{nextRig: &nextRig{wakeRig: r, mux: mux}, gh: &openGH{open: map[string]bool{livePR7: true}}, stamp: time.Now()}
	r.s.GitHub = func(string) moor.GitHub { return l.gh }
	r.s.baselineAll()
	go r.s.Run(r.ctx)
	return l
}

func TestAScoutIsStoppedWhenItsTurnEndsAndTheLogReadsItSailed(t *testing.T) {
	t.Parallel()
	r := scoutRig(t, true)
	r.turn(t, "working", "")
	r.turn(t, "needs_you", "stop")
	r.gone(t, "the scout to be stopped")
	waitUntil(t, "the stopped record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "stopped")) == 1 })
	if got := outcomeLines(t, r.home, "saltpaw", "stopped"); got[0]["scout"] != true || got[0]["returned"] != nil {
		t.Errorf("stopped = %v, want the scout mark and no returned mark", got[0])
	}
	for _, e := range []string{"moored", "returning"} {
		if got := outcomeLines(t, r.home, "saltpaw", e); len(got) != 0 {
			t.Errorf("%s records = %v, want none for a scout's end", e, got)
		}
	}
}

func TestAScoutIsLeftAloneAtAQuestionOrAPermission(t *testing.T) {
	t.Parallel()
	r := scoutRig(t, true)
	r.turn(t, "working", "")
	r.turn(t, "needs_you", "permission:Bash")
	holdsFor(t, settleHold(), "the scout was stopped at a permission", func() bool { return r.live(t) })
}

func TestAPlainAutopilotShipIsNotStoppedAtItsStop(t *testing.T) {
	t.Parallel()
	r := scoutRig(t, false)
	r.turn(t, "working", "")
	r.turn(t, "needs_you", "stop")
	holdsFor(t, settleHold(), "an autopilot ship that is not a scout was stopped", func() bool { return r.live(t) })
	if got := outcomeLines(t, r.home, "saltpaw", "stopped"); len(got) != 0 {
		t.Errorf("stopped records = %v, want none", got)
	}
}

func TestAScoutWhoseReportNamesAnOpenPRIsStoppedNotMoored(t *testing.T) {
	t.Parallel()
	r := scoutRig(t, true)
	r.turn(t, "working", "")
	r.writeReport(t, "done", livePR7)
	r.turn(t, "needs_you", "stop")
	r.gone(t, "the scout to be stopped")
	waitUntil(t, "the stopped record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "stopped")) == 1 })
	if _, err := os.Stat(r.mooredFile()); err == nil {
		t.Error("the scout was moored: it has a moored record")
	}
	if got := outcomeLines(t, r.home, "saltpaw", "moored"); len(got) != 0 {
		t.Errorf("moored records = %v, want none", got)
	}
}

func TestAShipsJSONSaysWhenItIsAScout(t *testing.T) {
	t.Parallel()
	for _, scout := range []bool{true, false} {
		r := scoutRig(t, scout)
		_, raw, ok := r.wire(t)
		if !ok {
			t.Fatal("the ship is not listed")
		}
		var got bool
		if v, has := raw["scout"]; has {
			_ = json.Unmarshal(v, &got)
		}
		if got != scout {
			t.Errorf("ship JSON scout = %s, want %v", raw["scout"], scout)
		}
	}
}

func TestHandToCommodoreIsRefusedOnAScout(t *testing.T) {
	t.Parallel()
	r := scoutRig(t, true)
	r.turn(t, "working", "")
	before := r.pane(t)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/saltpaw/commodore", strings.NewReader(`{"kind":"other","text":"carry on"}`)))
	msg := strings.ToLower(wantError(t, rec, http.StatusBadRequest, "a scout cannot be handed on"))
	if !strings.Contains(msg, "scout") {
		t.Errorf("refusal = %q, want it to say a scout cannot be handed on", msg)
	}
	if after := r.pane(t); after != before {
		t.Errorf("the refused hand reached the pane:\n%s", after)
	}
	if sh, _ := r.s.find("saltpaw"); sh.Options["handing"] != "" {
		t.Error("the refused hand still marked the ship")
	}
}
