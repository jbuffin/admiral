package ships

import (
	"net/http"
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

// scoutHome is guardedHome plus a passed scout guard probe for claude in work.
func scoutHome(t *testing.T) string {
	t.Helper()
	home := guardedHome(t)
	if err := capabilities.Open(home).Record("work", "claude", "2.1.300", capabilities.ScoutGuard, capabilities.Result{Passed: true, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return home
}

func scoutService(t *testing.T, home string, maxShips int) *Service {
	t.Helper()
	fl := fleets(t)
	return &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: a launch that gets through is a 503
		Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return &config.Config{Fleets: fl, MaxShips: maxShips} }},
	}
}

func scoutBody(t *testing.T, harness, extra string) string {
	t.Helper()
	return `{"fleet":"work","harness":"` + harness + `","dir":"` + t.TempDir() + `","skill":{"kind":"none"},"text":"find it","autopilot":true,"scout":true` + extra + `}`
}

func TestAScoutIsRefusedWhereItsGuardIsNotProven(t *testing.T) {
	for _, c := range []struct {
		name, home, harness string
	}{
		{"autopilot proven, scout guard never probed", "", "claude"},
		{"codex", "scout", "codex"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := guardedHome(t)
			if c.home == "scout" {
				home = scoutHome(t)
			}
			rec := launchPost(t, scoutService(t, home, 5), scoutBody(t, c.harness, ""))
			msg := strings.ToLower(rec.Body.String())
			if rec.Code != http.StatusBadRequest || !strings.Contains(msg, c.harness) || !strings.Contains(msg, "scout") {
				t.Errorf("scout on %s = %d %s, want 400 naming the harness and the scout guard", c.harness, rec.Code, rec.Body)
			}
		})
	}
}

func TestAScoutWithBothPassesIsAdmitted(t *testing.T) {
	rec := launchPost(t, scoutService(t, scoutHome(t), 5), scoutBody(t, "claude", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("scout on claude with both passes = %d %s, want it admitted up to the host that is not ready (503)", rec.Code, rec.Body)
	}
}

func TestAScoutWithoutAutopilotIsRefused(t *testing.T) {
	body := strings.Replace(scoutBody(t, "claude", ""), `"autopilot":true,`, "", 1)
	rec := launchPost(t, scoutService(t, scoutHome(t), 5), body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(rec.Body.String()), "autopilot") {
		t.Errorf("scout without autopilot = %d %s, want 400 naming autopilot", rec.Code, rec.Body)
	}
}

func TestAScoutTakesNoSkillMemberIssuesSourceOrderOrClone(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "widget")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitcfg := "[remote \"origin\"]\n\turl = https://github.com/acme/widget.git\n"
	if err := os.WriteFile(filepath.Join(checkout, ".git", "config"), []byte(gitcfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, body string }{
		{"a skill", strings.Replace(scoutBody(t, "claude", ""), `"kind":"none"`, `"kind":"skill","name":"tdd"`, 1)},
		{"commodore", strings.Replace(scoutBody(t, "claude", ""), `"kind":"none"`, `"kind":"commodore"`, 1)},
		{"a member", scoutBody(t, "claude", `,"member":"scribe"`)},
		{"issues", scoutBody(t, "claude", `,"issues":["https://github.com/acme/widget/issues/1"]`)},
		{"a source", scoutBody(t, "claude", `,"source":"s"`)},
		{"an order id", scoutBody(t, "claude", `,"order_id":"o1"`)},
		{"a clone", `{"fleet":"work","harness":"claude","skill":{"kind":"none"},"autopilot":true,"scout":true,"clone":true,"text":"see https://github.com/acme/widget/issues/1"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := scoutService(t, scoutHome(t), 5)
			s.Launcher.Config = func() *config.Config {
				return &config.Config{MaxShips: 5, Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{root},
					Harnesses: fleets(t)[0].Harnesses}}}
			}
			rec := launchPost(t, s, c.body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(rec.Body.String()), "scout") {
				t.Errorf("scout with %s = %d %s, want 400 saying a scout takes none", c.name, rec.Code, rec.Body)
			}
		})
	}
}

func TestAScoutMayHoldResources(t *testing.T) {
	s := scoutService(t, scoutHome(t), 5)
	s.Launcher.Config = func() *config.Config {
		return &config.Config{MaxShips: 5, Fleets: fleets(t), Resources: map[string]config.Resource{"phone": {Capacity: 1}}}
	}
	s.Reserve = func(names []string) (func(), string, string) { return func() {}, "", "" }
	rec := launchPost(t, s, scoutBody(t, "claude", `,"resources":["phone"]`))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("scout with a resource = %d %s, want it admitted (503: host not ready)", rec.Code, rec.Body)
	}
}

func TestACappedFleetNeverBlocksAScoutAndStillBlocksAutopilot(t *testing.T) {
	s := scoutService(t, scoutHome(t), 1)
	s.holdUnlisted(OrderKey{"s", "o1"}, "ship-1", "p", nil)
	if rec := launchPost(t, s, scoutBody(t, "claude", "")); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("scout over the cap = %d %s, want admitted (503: host not ready)", rec.Code, rec.Body)
	}
	plain := strings.Replace(scoutBody(t, "claude", ""), `,"scout":true`, "", 1)
	if rec := launchPost(t, s, plain); rec.Code != http.StatusConflict {
		t.Errorf("autopilot over the cap = %d %s, want 409", rec.Code, rec.Body)
	}
}

func TestACodexScoutOverTheCapGetsItsGuardRefusalNotTheCap(t *testing.T) {
	s := scoutService(t, scoutHome(t), 1)
	s.holdUnlisted(OrderKey{"s", "o1"}, "ship-1", "p", nil)
	rec := launchPost(t, s, scoutBody(t, "codex", ""))
	if msg := strings.ToLower(rec.Body.String()); rec.Code != http.StatusBadRequest || !strings.Contains(msg, "scout") {
		t.Errorf("codex scout over the cap = %d %s, want the scout 400", rec.Code, rec.Body)
	}
}
