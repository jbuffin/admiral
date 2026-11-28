package ships

// Answering one stop of a pane under its lock, replayed on a real tmux pane that records every byte it is sent.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func (p *answerPane) option(name string) string {
	p.t.Helper()
	out, err := exec.Command(p.tmux, "-S", p.sock, "show-options", "-p", "-v", "-t", p.pane, "@admiral-"+name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// stopID is the id the pane's last needs-you write left, and fails the test when it left none.
func (p *answerPane) stopID() string {
	p.t.Helper()
	id := p.option(sessionhost.OptStopID)
	if id == "" {
		p.t.Error("the needs-you write left no @admiral-stop-id on the pane, so no answer can be bound to the stop")
	}
	return id
}

// working is what a hook writes when the ship takes its turn again.
func (p *answerPane) working() {
	p.t.Helper()
	if err := p.s.Host.SetPaneWorking(context.Background(), p.pane); err != nil {
		p.t.Fatal(err)
	}
	if err := p.s.Host.Sync(context.Background()); err != nil {
		p.t.Fatal(err)
	}
}

// sentWith waits for the pane to have read text and returns everything it read.
func (p *answerPane) sentWith(text string) string {
	p.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b := p.sent("")
		if strings.Contains(b, text) || time.Now().After(deadline) {
			return b
		}
	}
}

func (p *answerPane) answerStop(ctx context.Context, a StopAnswer) error {
	return p.s.AnswerStop(ctx, "alpha", a)
}

func TestAnswerStopSendsTheKeysOfAPermissionPromptThatIsStillThePanesStop(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", nil)
	p.needsYou("notification:permission_prompt")
	id := p.stopID()
	if err := p.answerStop(context.Background(), StopAnswer{StopID: id, Reason: "notification:permission_prompt", Choice: "allow"}); err != nil {
		t.Fatalf("AnswerStop allow = %v, want nil", err)
	}
	if got := p.sent("1"); got != "1" {
		t.Errorf("Allow sent %q, want %q", got, "1")
	}

	p.needsYou("notification:permission_prompt")
	id2 := p.stopID()
	if id2 == id {
		t.Errorf("a second identical prompt has stop id %q, the first one's: two prompts must differ", id2)
	}
	if err := p.answerStop(context.Background(), StopAnswer{StopID: id2, Reason: "notification:permission_prompt", Choice: "deny"}); err != nil {
		t.Fatalf("AnswerStop deny = %v, want nil", err)
	}
	if got := p.sent("1\x1b"); got != "1\x1b" {
		t.Errorf("Deny sent %q after Allow's, want one more Esc", got)
	}
	if got := p.reason(); got != "stop" {
		t.Errorf("the pane's reason after Deny = %q, want stop", got)
	}
	if id3 := p.option(sessionhost.OptStopID); id3 == "" || id3 == id2 {
		t.Errorf("stop id after Deny = %q, want a new one: the pane now waits on a finished turn", id3)
	}
}

func TestAnswerStopRefusesAndSendsNothingWhenThePaneIsNoLongerAtThatStop(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", nil)
	p.needsYou("notification:permission_prompt")
	old := p.stopID()
	p.needsYou("notification:permission_prompt") // an identical prompt follows the first
	cur := p.stopID()
	ask := StopAnswer{Reason: "notification:permission_prompt", Choice: "allow"}

	for name, a := range map[string]StopAnswer{
		"an earlier identical prompt": {StopID: old, Reason: ask.Reason, Choice: "allow"},
		"another reason":              {StopID: cur, Reason: "permission:Edit", Choice: "allow"},
		"an unknown stop id":          {StopID: "never-minted", Reason: ask.Reason, Choice: "allow"},
	} {
		if err := p.answerStop(context.Background(), a); !errors.Is(err, ErrStopMoved) {
			t.Errorf("AnswerStop for %s = %v, want ErrStopMoved", name, err)
		}
	}
	p.working()
	if err := p.answerStop(context.Background(), StopAnswer{StopID: cur, Reason: ask.Reason, Choice: "allow"}); !errors.Is(err, ErrStopMoved) {
		t.Errorf("AnswerStop for a prompt the ship has left = %v, want ErrStopMoved", err)
	}
	if got := p.sent(""); got != "" {
		t.Errorf("the pane was sent %q by refused answers, want nothing", got)
	}
}

func TestAnswerStopForAShipTheHostDoesNotListIsNotFound(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", nil)
	if err := p.s.AnswerStop(context.Background(), "nobody", StopAnswer{StopID: "x", Reason: "stop", Text: "hi"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("AnswerStop for an unknown ship = %v, want ErrNotFound", err)
	}
}

func TestAnswerStopTypesTheTextOfAFinishedTurnAndTheStopIsGone(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "claude", nil)
	p.needsYou("stop")
	id := p.stopID()
	if err := p.answerStop(context.Background(), StopAnswer{StopID: id, Reason: "stop", Text: "now run the tests"}); err != nil {
		t.Fatalf("AnswerStop text = %v, want nil", err)
	}
	if got := p.sentWith("now run the tests"); !strings.Contains(got, "now run the tests") {
		t.Errorf("the pane read %q, want the text typed", got)
	}
	before := p.sent("")
	err := p.answerStop(context.Background(), StopAnswer{StopID: "stale", Reason: "stop", Text: "second"})
	if !errors.Is(err, ErrStopMoved) {
		t.Errorf("AnswerStop with another stop id = %v, want ErrStopMoved", err)
	}
	if got := p.sent(""); got != before {
		t.Errorf("a refused text added %q to the pane, want nothing", strings.TrimPrefix(got, before))
	}
}

func TestAnswerStopRefusesAnAnswerTheStopCannotTakeAndSendsNothing(t *testing.T) {
	t.Parallel()
	menu := map[string]StopAnswer{
		"ExitPlanMode":      {Reason: "permission:ExitPlanMode", Choice: "allow"},
		"AskUserQuestion":   {Reason: "permission:AskUserQuestion", Choice: "deny"},
		"an unknown choice": {Reason: "permission:Bash", Choice: "maybe"},
		"text on a prompt":  {Reason: "permission:Bash", Text: "hi"},
	}
	for name, a := range menu {
		t.Run(name, func(t *testing.T) {
			p := newAnswerPane(t, "claude", nil)
			p.needsYou(a.Reason)
			a.StopID = p.stopID()
			var refused *StopRefused
			if err := p.answerStop(context.Background(), a); !errors.As(err, &refused) {
				t.Errorf("AnswerStop = %v, want a *StopRefused naming why", err)
			}
			if got := p.sent(""); got != "" {
				t.Errorf("the pane was sent %q, want nothing", got)
			}
		})
	}
	t.Run("text a pane cannot be typed", func(t *testing.T) {
		p := newAnswerPane(t, "claude", nil)
		p.needsYou("stop")
		var refused *StopRefused
		for name, text := range map[string]string{"empty": " ", "a control byte": "a\x1bb", "a choice on a stop": ""} {
			a := StopAnswer{StopID: p.stopID(), Reason: "stop", Text: text}
			if name == "a choice on a stop" {
				a.Choice = "allow"
			}
			if err := p.answerStop(context.Background(), a); !errors.As(err, &refused) {
				t.Errorf("AnswerStop with %s = %v, want a *StopRefused", name, err)
			}
		}
		if got := p.sent(""); got != "" {
			t.Errorf("the pane was sent %q, want nothing", got)
		}
	})
}

func TestAnAnswerThroughAnyShipsRouteMarksTheStopAnsweredByTheEngineer(t *testing.T) {
	t.Parallel()
	routes := map[string]struct{ reason, path, body string }{
		"answer":    {"notification:permission_prompt", "/answer", `{"card":"permission","reason":"notification:permission_prompt","choice":"allow"}`},
		"messages":  {"stop", "/messages", `{"text":"hello"}`},
		"next":      {"stop", "/next", `{"text":"hello"}`},
		"interrupt": {"stop", "/interrupt", ``},
	}
	for name, rt := range routes {
		t.Run(name, func(t *testing.T) {
			p := newAnswerPane(t, "copilot", nil)
			p.needsYou(rt.reason)
			id := p.stopID()
			rec := httptest.NewRecorder()
			p.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/ships/alpha"+rt.path, strings.NewReader(rt.body)))
			if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
				t.Fatalf("POST %s = %d %s, want it to succeed", rt.path, rec.Code, rec.Body)
			}
			if got := p.option(sessionhost.OptStopAnswered); got != id {
				t.Errorf("@admiral-stop-answered = %q, want %q: the stop id alone, for the engineer", got, id)
			}
		})
	}
}

func TestAnAnswerUnderTheAdmiralBotTokenMarksTheStopAnsweredViaAdmiralBot(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", nil)
	p.needsYou("notification:permission_prompt")
	id := p.stopID()
	ctx := actor.WithFleet(context.Background(), "work")
	if err := p.answerStop(ctx, StopAnswer{StopID: id, Reason: "notification:permission_prompt", Choice: "allow"}); err != nil {
		t.Fatalf("AnswerStop = %v, want nil", err)
	}
	if got, want := p.option(sessionhost.OptStopAnswered), id+" admiralbot"; got != want {
		t.Errorf("@admiral-stop-answered = %q, want %q", got, want)
	}
}

func TestAnAnswerARouteRefusesMarksNoStopAnswered(t *testing.T) {
	t.Parallel()
	p := newAnswerPane(t, "copilot", nil)
	p.needsYou("notification:permission_prompt")
	code, _ := p.post(`{"card":"permission","reason":"permission:Edit","choice":"allow"}`)
	if code != http.StatusConflict {
		t.Fatalf("POST answer for another reason = %d, want 409", code)
	}
	if got := p.option(sessionhost.OptStopAnswered); got != "" {
		t.Errorf("@admiral-stop-answered = %q after a refused answer, want none", got)
	}
}
