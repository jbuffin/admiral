package ships

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/launcher"
)

type lineCatcher struct {
	mu    sync.Mutex
	fleet []string
	lines []LaunchLine
}

func (c *lineCatcher) post(fleet string, l LaunchLine) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fleet, c.lines = append(c.fleet, fleet), append(c.lines, l)
	return nil
}

func (c *lineCatcher) got() ([]string, []LaunchLine) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.fleet...), append([]LaunchLine(nil), c.lines...)
}

// launchBody posts a launch to the route, as AdmiralBot in the work chat when asBot, else as the engineer, and returns the new ship's session.
func (r *wakeRig) launchBody(t *testing.T, asBot bool, extra string) string {
	t.Helper()
	mux := http.NewServeMux()
	r.s.Routes(mux)
	body := `{"fleet":"work","harness":"claude","dir":` + strconv.Quote(r.checkout) + `,"model":"opus","text":"read the logs"` + extra + `}`
	req := httptest.NewRequest("POST", "/api/ships", strings.NewReader(body))
	if asBot {
		req = req.WithContext(actor.WithFleet(context.Background(), "work"))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("launch = %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Ship struct{ Session, Name string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Ship.Session == "" {
		t.Fatalf("launch answer %s: %v", rec.Body, err)
	}
	return got.Ship.Session
}

func TestAChatLaunchPostsOneLaunchLineWithTheShipsKindTheReasonAndTheLaunchTextAsWritten(t *testing.T) {
	for _, c := range []struct{ name, extra, kind string }{
		{"a quick ship", `,"skill":{"kind":"none"},"autopilot":true,"reason":"to read the AWS logs"`, "quick_ship"},
		{"a scout", `,"skill":{"kind":"none"},"autopilot":true,"scout":true,"reason":"to read the AWS logs"`, "scout"},
		{"a Commodore ship", `,"skill":{"kind":"commodore"},"autopilot":true,"reason":"to read the AWS logs"`, "commodore"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newWakeRig(t, 0)
			if err := capabilities.Open(r.home).Record("work", "claude", "2.1.300", capabilities.ScoutGuard, capabilities.Result{Passed: true, At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			caught := &lineCatcher{}
			r.s.PostLaunchLine = caught.post
			session := r.launchBody(t, true, c.extra)
			fleets, lines := caught.got()
			if len(lines) != 1 {
				t.Fatalf("launch lines = %+v, want exactly one", lines)
			}
			want := LaunchLine{Ship: session, Kind: c.kind, Reason: "to read the AWS logs", Text: "read the logs"}
			if fleets[0] != "work" || lines[0].Ship != want.Ship || lines[0].Kind != want.Kind || lines[0].Reason != want.Reason || lines[0].Text != want.Text {
				t.Errorf("launch line = %s %+v, want fleet work and %+v", fleets[0], lines[0], want)
			}
		})
	}
}

func TestALaunchThatIsNotAChatLaunchAddsNoLaunchLine(t *testing.T) {
	r := newWakeRig(t, 0)
	caught := &lineCatcher{}
	r.s.PostLaunchLine = caught.post
	// the one chat launch shows the catcher is wired; the others must add nothing to it
	r.launchBody(t, true, `,"skill":{"kind":"none"},"autopilot":true,"reason":"to read the AWS logs"`)
	r.launchBody(t, false, `,"skill":{"kind":"none"},"reason":"ignored"`)
	// a commitment's or campaign's launch carries admiralbot's via but is not made on the engineer's instruction
	req := launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Skill: launcher.Skill{Kind: "none"},
		Text: "read the logs", Via: actor.Via, Reason: "to read the AWS logs", CommitmentKey: "commitment:c1"}
	if _, lerr := r.s.Launch(actor.WithFleet(context.Background(), "work"), req); lerr != nil {
		t.Fatalf("Launch: %v", lerr)
	}
	if _, lines := caught.got(); len(lines) != 1 {
		t.Errorf("launch lines = %+v, want only the chat launch's", lines)
	}
}
