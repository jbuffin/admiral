package ships

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// nextRig is a running ship, saltpaw, in fleet work whose pane echoes what is typed at it.
type nextRig struct {
	*wakeRig
	mux *http.ServeMux
}

func newNextRig(t *testing.T) *nextRig {
	t.Helper()
	r := newWakeRig(t, 0)
	r.launchRound(t, 0)
	r.started(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	return &nextRig{wakeRig: r, mux: mux}
}

// turn sets what the ship's hooks would: its state and the reason it needs you.
func (r *nextRig) turn(t *testing.T, state, reason string) {
	t.Helper()
	args := []string{"-S", r.s.Launcher.Socket, "set-option", "-p", "-t", "saltpaw", "@admiral-state", state}
	if out, err := exec.Command(r.s.Launcher.Tmux, args...).CombinedOutput(); err != nil {
		t.Fatalf("set @admiral-state: %v: %s", err, out)
	}
	args = []string{"-S", r.s.Launcher.Socket, "set-option", "-p", "-t", "saltpaw", "@admiral-reason", reason}
	if reason == "" {
		args = []string{"-S", r.s.Launcher.Socket, "set-option", "-p", "-u", "-t", "saltpaw", "@admiral-reason"}
	}
	if out, err := exec.Command(r.s.Launcher.Tmux, args...).CombinedOutput(); err != nil {
		t.Fatalf("set @admiral-reason: %v: %s", err, out)
	}
	waitUntil(t, "the host to see the turn", func() bool {
		_ = r.s.Host.Sync(context.Background())
		for _, sess := range r.s.Host.Sessions() {
			if sess.Name == "saltpaw" {
				return sess.State == state && sess.Reason == reason
			}
		}
		return false
	})
}

func (r *nextRig) pane(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(r.s.Launcher.Tmux, "-S", r.s.Launcher.Socket, "capture-pane", "-p", "-t", "saltpaw").CombinedOutput()
	if err != nil {
		t.Fatalf("capture-pane: %v: %s", err, out)
	}
	return string(out)
}

func (r *nextRig) next(session, body string, fleet string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/ships/"+session+"/next", strings.NewReader(body))
	if fleet != "" {
		req = req.WithContext(actor.WithFleet(context.Background(), fleet))
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

// wantError asserts the status and a JSON {"error": ...} body, so the mux's own plain-text 404 can't pass.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, why string) string {
	t.Helper()
	var body struct{ Error string }
	if rec.Code != status || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
		t.Fatalf("%s: code %d, body %q; want %d with a JSON error", why, rec.Code, rec.Body, status)
	}
	return body.Error
}

func TestNextTypesTheTextAtAShipsPromptOnceItsTurnHasEnded(t *testing.T) {
	r := newNextRig(t)
	r.turn(t, "needs_you", "stop")

	rec := r.next("saltpaw", `{"text":"now update the docs"}`, "work")
	if rec.Code != http.StatusOK {
		t.Fatalf("next = %d %s, want 200", rec.Code, rec.Body)
	}
	waitUntil(t, "the text at the prompt", func() bool { return strings.Contains(r.pane(t), "now update the docs") })

	var got []map[string]any
	waitUntil(t, "the resumed record", func() bool { got = outcomeLines(t, r.home, "saltpaw", "resumed"); return len(got) == 1 })
	if got[0]["via"] != "admiralbot" {
		t.Errorf("resumed record = %v, want via admiralbot", got[0])
	}
}

func TestNextRefusesAShipWhoseTurnHasNotEnded(t *testing.T) {
	for _, c := range []struct{ name, state, reason string }{
		{"mid-turn", "working", ""},
		{"waiting on an approval", "needs_you", "permission:Bash"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newNextRig(t)
			r.turn(t, c.state, c.reason)
			rec := r.next("saltpaw", `{"text":"typed too soon"}`, "work")
			wantError(t, rec, http.StatusConflict, c.name)
			if strings.Contains(r.pane(t), "typed too soon") {
				t.Error("the text reached the pane of a ship whose turn had not ended")
			}
			if got := outcomeLines(t, r.home, "saltpaw", "resumed"); len(got) != 0 {
				t.Errorf("resumed records = %v, want none for a refused text", got)
			}
		})
	}
}

func TestNextRefusesTextThatCannotBeTypedAtAPrompt(t *testing.T) {
	for _, c := range []struct{ name, text string }{
		{"a control character", "fix\x01it"},
		{"only whitespace", "  \n "},
		{"more than a message holds", strings.Repeat("x", sessionhost.MaxPaste+1)},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newNextRig(t)
			r.turn(t, "needs_you", "stop")
			body, _ := json.Marshal(map[string]string{"text": c.text})
			wantError(t, r.next("saltpaw", string(body), "work"), http.StatusBadRequest, c.name)
			if got := outcomeLines(t, r.home, "saltpaw", "resumed"); len(got) != 0 {
				t.Errorf("resumed records = %v, want none for a refused text", got)
			}
		})
	}
}

func TestNextIsNotFoundForAnUnknownShipOrACrewBoat(t *testing.T) {
	r := newNextRig(t)
	r.turn(t, "needs_you", "stop")
	if out, err := exec.Command(r.s.Launcher.Tmux, "-S", r.s.Launcher.Socket, "new-window", "-d", "-t", "saltpaw", "-n", "boat").CombinedOutput(); err != nil {
		t.Fatalf("new-window: %v: %s", err, out)
	}
	if out, err := exec.Command(r.s.Launcher.Tmux, "-S", r.s.Launcher.Socket, "set-option", "-w", "-t", "saltpaw:boat", "@admiral-crew-name", "boat").CombinedOutput(); err != nil {
		t.Fatalf("set @admiral-crew-name: %v: %s", err, out)
	}
	for _, session := range []string{"nosuchship", "boat"} {
		wantError(t, r.next(session, `{"text":"go"}`, "work"), http.StatusNotFound, session)
	}
}

func TestNextTellsYouToWakeAMooredShip(t *testing.T) {
	f := mooredService(t, nil)
	session := f.moor(t, "Saltpaw", "work", prSeven)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/"+session+"/next", strings.NewReader(`{"text":"go"}`)))
	if msg := wantError(t, rec, http.StatusConflict, "a moored ship"); !strings.Contains(strings.ToLower(msg), "wake") {
		t.Errorf("error %q, want it to say to wake the ship instead", msg)
	}
}

func TestNextIsNotFoundForAShipNoServiceKnows(t *testing.T) {
	f := mooredService(t, nil)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/nosuchship/next", strings.NewReader(`{"text":"go"}`)))
	wantError(t, rec, http.StatusNotFound, "an unknown ship")
}

func TestAWakeByAnActorStampsTheWokenRecordViaAdmiralbot(t *testing.T) {
	for _, c := range []struct{ name, fleet, via string }{{"AdmiralBot", "work", "admiralbot"}, {"the engineer", "", ""}} {
		t.Run(c.name, func(t *testing.T) {
			r := newWakeRig(t, 0)
			r.moored(t, 0)
			mux := http.NewServeMux()
			r.s.Routes(mux)
			req := httptest.NewRequest("POST", "/api/ships/saltpaw/wake", strings.NewReader(`{"text":"also the docs"}`))
			if c.fleet != "" {
				req = req.WithContext(actor.WithFleet(context.Background(), c.fleet))
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("wake = %d %s, want 201", rec.Code, rec.Body)
			}
			r.started(t, 1)
			got := r.logged(t, "woken")
			if len(got) != 1 {
				t.Fatalf("woken records = %v, want one", got)
			}
			if via, _ := got[0]["via"].(string); via != c.via {
				t.Errorf("woken record via = %q, want %q", via, c.via)
			}
		})
	}
}

func TestNextRefusesAnActorsHarnessCommandButTypesItsOrdinaryText(t *testing.T) {
	for _, text := range []string{"/compact", "  /clear", "!rm -rf x", "\n!ls"} {
		t.Run(text, func(t *testing.T) {
			r := newNextRig(t)
			r.turn(t, "needs_you", "stop")
			body, _ := json.Marshal(map[string]string{"text": text})
			msg := wantError(t, r.next("saltpaw", string(body), "work"), http.StatusBadRequest, text)
			if !strings.Contains(msg, "harness command") {
				t.Errorf("error %q, want it to name the harness command", msg)
			}
			if got := outcomeLines(t, r.home, "saltpaw", "resumed"); len(got) != 0 {
				t.Errorf("resumed records = %v, want none", got)
			}
		})
	}
	t.Run("the engineer's own slash command", func(t *testing.T) {
		r := newNextRig(t)
		r.turn(t, "needs_you", "stop")
		if rec := r.next("saltpaw", `{"text":"/compact"}`, ""); rec.Code != http.StatusOK {
			t.Fatalf("next = %d %s, want 200", rec.Code, rec.Body)
		}
	})
	t.Run("ordinary text with a slash inside", func(t *testing.T) {
		r := newNextRig(t)
		r.turn(t, "needs_you", "stop")
		if rec := r.next("saltpaw", `{"text":"edit a/b.go"}`, "work"); rec.Code != http.StatusOK {
			t.Fatalf("next = %d %s, want 200", rec.Code, rec.Body)
		}
	})
}

func TestAnActorsWakeTextNeverLeadsThePrompt(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	req := httptest.NewRequest("POST", "/api/ships/saltpaw/wake", strings.NewReader(`{"text":"/clear"}`)).
		WithContext(actor.WithFleet(context.Background(), "work"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("wake = %d %s, want 201", rec.Code, rec.Body)
	}
	argv, _ := r.started(t, 1)
	if got := prompt(argv); !strings.HasPrefix(got, "/commodore resume run-7 ") || !strings.HasSuffix(got, " /clear") {
		t.Errorf("prompt = %q, want the ship's resume text first, so the actor's text is its argument", got)
	}
}
