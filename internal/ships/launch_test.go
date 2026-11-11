package ships

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func launchPost(t *testing.T, s *Service, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.serveLaunch(rec, httptest.NewRequest("POST", "/api/ships", strings.NewReader(body)))
	return rec
}

// guardedHome is a home where claude's guard probe passed in fleet work, the way the
// probe runner leaves it.
func guardedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	store, now := capabilities.Open(home), time.Now()
	if err := store.RecordVersion("work", "claude", "2.1.300", now); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("work", "claude", "2.1.300", capabilities.AutopilotGuard, capabilities.Result{Passed: true, At: now}); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestLaunchAPIRefusesWhatAutopilotModeMayNotClaim(t *testing.T) {
	held := 0
	s := &Service{
		Host: sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: not ready
		Launcher: &launcher.Launcher{Home: guardedHome(t), Config: func() *config.Config {
			return &config.Config{Resources: map[string]config.Resource{"phone": {Capacity: 1}}}
		}},
		Reserve: func(names []string) (func(), string, string) {
			if held > 0 {
				return nil, names[0], "busy"
			}
			held++
			return func() { held-- }, "", ""
		},
	}
	dir := t.TempDir()
	base := `"fleet":"work","harness":"claude","dir":"` + dir + `","skill":{"kind":"none"}`

	if rec := launchPost(t, s, `{`+base+`,"autopilot":true,"resources":["nope"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown resource = %d %s, want 400", rec.Code, rec.Body)
	}
	if rec := launchPost(t, s, `{`+strings.Replace(base, `"claude"`, `"codex"`, 1)+`,"autopilot":true}`); rec.Code != http.StatusBadRequest {
		t.Errorf("autopilot on a harness the guard does not cover = %d %s, want 400", rec.Code, rec.Body)
	}

	held++ // another ship holds the one unit
	if rec := launchPost(t, s, `{`+base+`,"resources":["phone"]}`); rec.Code != http.StatusConflict {
		t.Errorf("a busy resource = %d %s, want 409", rec.Code, rec.Body)
	}
	held--

	// The launch fails after it took the unit (ships are not known yet): the unit is given back.
	if rec := launchPost(t, s, `{`+base+`,"resources":["phone"]}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a launch before the host is ready = %d %s, want 503", rec.Code, rec.Body)
	}
	if held != 0 {
		t.Errorf("a failed launch left %d unit(s) held, want none", held)
	}
}

// The cap counts every ship underway or needing you, however it was launched; it
// refuses an autopilot launch and never an interactive one.
func TestAutopilotLaunchOverTheCapIsRefusedButAHandLaunchIsNot(t *testing.T) {
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: not ready
		Launcher: &launcher.Launcher{Home: guardedHome(t), Config: func() *config.Config { return &config.Config{MaxShips: 1} }},
	}
	s.holdUnlisted(OrderKey{"s", "o1"}, "ship-1", "p", nil)
	base := `"fleet":"work","harness":"claude","dir":"` + t.TempDir() + `","skill":{"kind":"none"}`

	if rec := launchPost(t, s, `{`+base+`,"autopilot":true}`); rec.Code != http.StatusConflict {
		t.Errorf("an autopilot launch over the cap = %d %s, want 409", rec.Code, rec.Body)
	}
	if rec := launchPost(t, s, `{`+base+`}`); rec.Code == http.StatusConflict {
		t.Errorf("an interactive launch over the cap = %d %s, want it never refused by the cap", rec.Code, rec.Body)
	}
}

// Without a passed guard probe in the launch's fleet, autopilot mode is refused with the
// same 400 the old harness list gave, nothing starts, and the Inbox says so once.
func TestAutopilotLaunchWithoutAProvenGuardIsRefusedAndNoticed(t *testing.T) {
	home := guardedHome(t) // proven in work only
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: a launch that got through would be a 503
		Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return &config.Config{} }},
	}
	dir := t.TempDir()
	body := func(fleet, harness string) string {
		return `{"fleet":"` + fleet + `","harness":"` + harness + `","dir":"` + dir + `","skill":{"kind":"none"},"autopilot":true}`
	}
	notices := func() []string {
		names, _ := filepath.Glob(filepath.Join(home, "inbox", "_admiral", "notice-*.json"))
		var titles []string
		for _, n := range names {
			raw, _ := os.ReadFile(n)
			titles = append(titles, string(raw))
		}
		return titles
	}

	for _, tc := range []struct{ fleet, harness, why string }{
		{"home", "claude", "proven in work, not in home"},
		{"work", "codex", "codex was never probed"},
	} {
		rec := launchPost(t, s, body(tc.fleet, tc.harness))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("autopilot on %s in %s (%s) = %d %s, want 400", tc.harness, tc.fleet, tc.why, rec.Code, rec.Body)
		}
		if msg := strings.ToLower(rec.Body.String()); !strings.Contains(msg, tc.harness) || !strings.Contains(msg, "guard") {
			t.Errorf("the 400 body %s must name the harness and the missing guard", rec.Body)
		}
	}
	if got := len(notices()); got != 2 {
		t.Fatalf("two refusals for two harnesses in two fleets left %d notices, want 2", got)
	}
	launchPost(t, s, body("work", "codex"))
	if got := len(notices()); got != 2 {
		t.Errorf("a repeated refusal stacked a notice: %d notices, want 2", got)
	}

	// The same launch where the guard is proven starts as before: it gets as far as the host.
	if rec := launchPost(t, s, body("work", "claude")); rec.Code == http.StatusBadRequest {
		t.Errorf("autopilot on claude in work, where the guard passed = %d %s, want it admitted", rec.Code, rec.Body)
	}
	// A hand launch never asks for the guard.
	if rec := launchPost(t, s, strings.Replace(body("home", "claude"), `,"autopilot":true`, "", 1)); rec.Code == http.StatusBadRequest {
		t.Errorf("a hand launch in home = %d %s, want it unaffected by the guard", rec.Code, rec.Body)
	}
}
