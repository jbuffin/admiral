package ships

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestLaunchAPIRefusesWhatAutopilotModeMayNotClaim(t *testing.T) {
	held := 0
	s := &Service{
		Host: sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: not ready
		Launcher: &launcher.Launcher{Config: func() *config.Config {
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
